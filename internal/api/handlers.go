package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

type userDTO struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	MustChange  bool       `json:"must_change"` // the default admin, not set up yet
	DiscordID   string     `json:"discord_id"`  // its own ids on the bots' platforms ("" = none)
	TelegramID  string     `json:"telegram_id"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
	// Projects: the projects it sees, when not an admin (ADR-224)
	Projects []string `json:"projects,omitempty"`
	// Offices: the other offices it reaches, when not an admin (ADR-239)
	Offices officeAccess `json:"offices,omitempty"`
}

// withProjects adds the projects an account other than an admin sees.
func (s *server) withProjects(ctx context.Context, u storage.User) (userDTO, error) {
	d := toDTO(u)
	if u.Role == storage.RoleAdmin {
		return d, nil
	}
	ids, err := s.cfg.Store.Users().Projects(ctx, u.ID)
	d.Projects = ids
	d.Offices = s.officesOf(ctx, u).acc
	return d, err
}

func toDTO(u storage.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role), Disabled: u.Disabled, MustChange: u.MustChange,
		DiscordID: u.DiscordID, TelegramID: u.TelegramID, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.cfg.Version})
}

// authStatus is public: the login page uses it to explain how to get in when
// the office has no account yet, or only the default admin (admin / admin).
func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.cfg.Store.Users().Count(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	_, pending := s.cfg.Auth.PendingDefault(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"has_users": n > 0, "default_admin": pending})
}

// setupAccount: the default admin sets its real email, name and password.
func (s *server) setupAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.cfg.Auth.SetupAccount(r.Context(), userFrom(r).ID, in.Email, in.Name, in.Password, sessionFrom(r).ID)
	switch {
	case errors.Is(err, auth.ErrSetUp):
		writeError(w, http.StatusConflict, "Tài khoản đã được thiết lập")
	case errors.Is(err, auth.ErrInvalidEmail):
		writeError(w, http.StatusBadRequest, "Email không hợp lệ")
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, http.StatusConflict, "Email đã tồn tại")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Mật khẩu cần ít nhất %d ký tự", auth.MinPasswordLen))
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"user": toDTO(u)})
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := s.cfg.Auth.Login(r.Context(), in.Email, in.Password, auth.ClientMeta{IP: s.clientIP(r), UserAgent: r.UserAgent()})
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "Email hoặc mật khẩu không đúng")
		return
	case errors.Is(err, auth.ErrThrottled):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "Đăng nhập sai quá nhiều lần, thử lại sau 15 phút")
		return
	case errors.Is(err, auth.ErrBusy):
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "Đang có nhiều lượt đăng nhập, thử lại sau ít giây")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.setCookie(w, r, res.Token, s.cfg.Auth.SessionTTL())
	writeJSON(w, http.StatusOK, map[string]any{"user": toDTO(res.User)})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(SessionCookie); err == nil {
		if err := s.cfg.Auth.Logout(r.Context(), ck.Value); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	s.clearCookie(w, r)
	s.setNamedCookie(w, r, backCookie, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	// build: which office is running, shown under the sidebar for everyone
	d, err := s.withProjects(r.Context(), userFrom(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"user": d, "build": s.build}
	// viewing as this account: the admin who is, for the banner
	if by := sessionFrom(r).ImpersonatorID; by != "" {
		if a, err := s.cfg.Store.Users().GetByID(r.Context(), by); err == nil {
			out["viewed_by"] = map[string]string{"email": a.Email, "name": a.Name}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// viewAs: an admin opens the office as another account, read-only; its own
// session waits in backCookie for endViewAs.
func (s *server) viewAs(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(SessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	res, err := s.cfg.Auth.ViewAs(r.Context(), userFrom(r), sessionFrom(r), r.PathValue("id"), auth.ClientMeta{IP: s.clientIP(r), UserAgent: r.UserAgent()})
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case errors.Is(err, auth.ErrCannotView):
		writeError(w, http.StatusBadRequest, "Chỉ xem được với tư cách tài khoản không phải admin, đang hoạt động và đã thiết lập")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	s.setNamedCookie(w, r, backCookie, ck.Value, int(time.Until(sessionFrom(r).ExpiresAt).Seconds()))
	s.setCookie(w, r, res.Token, time.Until(res.Session.ExpiresAt))
	writeJSON(w, http.StatusOK, map[string]any{"user": toDTO(res.User)})
}

// endViewAs closes the view and logs the admin back in with its own session;
// when that is gone, the browser is just logged out.
func (s *server) endViewAs(w http.ResponseWriter, r *http.Request) {
	var view, back string
	if ck, err := r.Cookie(SessionCookie); err == nil {
		view = ck.Value
	}
	if ck, err := r.Cookie(backCookie); err == nil {
		back = ck.Value
	}
	s.setNamedCookie(w, r, backCookie, "", -1)
	admin, err := s.cfg.Auth.EndView(r.Context(), view, back)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	s.setCookie(w, r, back, s.cfg.Auth.SessionTTL())
	writeJSON(w, http.StatusOK, map[string]any{"user": toDTO(admin)})
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.cfg.Auth.ChangePassword(r.Context(), userFrom(r).ID, in.OldPassword, in.NewPassword, sessionFrom(r).ID)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusBadRequest, "Mật khẩu hiện tại không đúng")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.cfg.Store.Users().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	me := userFrom(r)
	sc, err := s.scope(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	oc := s.officesOf(r.Context(), me)
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		if me.Role != storage.RoleAdmin && u.Role != storage.RoleMember { // a manager runs the members (ADR-239)
			continue
		}
		d, err := s.withProjects(r.Context(), u)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if me.Role != storage.RoleAdmin { // only what it gives them shows
			d.Projects, d.Offices = shownProjects(d.Projects, sc), shownOffices(d.Offices, oc)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "offices": s.officeChoices(r)})
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string       `json:"email"`
		Name     string       `json:"name"`
		Role     string       `json:"role"`
		Password string       `json:"password"`
		Projects []string     `json:"projects"` // what a manager or member sees (ADR-224)
		Offices  officeAccess `json:"offices"`  // the other offices it reaches (ADR-239)
	}
	if !decode(w, r, &in) {
		return
	}
	me := userFrom(r)
	if me.Role != storage.RoleAdmin {
		if in.Role != string(storage.RoleMember) {
			writeError(w, http.StatusForbidden, "chỉ tạo được tài khoản member")
			return
		}
		sc, err := s.scope(r)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for _, id := range in.Projects {
			if !sc.has(id) {
				writeError(w, http.StatusBadRequest, errNotYours.Error())
				return
			}
		}
	}
	offices, err := s.cleanOffices(r.Context(), s.officesOf(r.Context(), me), in.Offices)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.cfg.Auth.CreateUser(r.Context(), auth.NewUser{Email: in.Email, Name: in.Name, Role: storage.Role(in.Role), Password: in.Password}, "human:"+userFrom(r).Email)
	if err == nil && len(in.Projects) > 0 && u.Role != storage.RoleAdmin {
		err = s.cfg.Auth.SetProjects(r.Context(), u.ID, in.Projects, "human:"+userFrom(r).Email)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "project không tồn tại")
			return
		}
	}
	if err == nil && len(offices) > 0 && u.Role != storage.RoleAdmin {
		err = s.cfg.Store.Settings().Set(r.Context(), officesKey(u.ID), offices)
	}
	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, http.StatusConflict, "Email đã tồn tại")
	case errors.Is(err, auth.ErrInvalidEmail), errors.Is(err, auth.ErrInvalidRole), errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		d, err := s.withProjects(r.Context(), u)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"user": d})
	}
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Disabled   *bool         `json:"disabled"`
		DiscordID  *string       `json:"discord_id"`
		TelegramID *string       `json:"telegram_id"`
		Role       *string       `json:"role"`
		Projects   *[]string     `json:"projects"` // what a manager or member sees (ADR-224)
		Offices    *officeAccess `json:"offices"`  // the other offices it reaches (ADR-239)
	}
	if !decode(w, r, &in) {
		return
	}
	actor := "human:" + userFrom(r).Email
	// a manager runs members only, and gives only what it has (ADR-239);
	// all is checked before anything changes
	target, ok := s.memberTarget(w, r, id)
	if !ok {
		return
	}
	by := s.officesOf(r.Context(), userFrom(r))
	var offices officeAccess
	if in.Offices != nil {
		set, err := s.cleanOffices(r.Context(), by, *in.Offices)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		offices = mergeOffices(s.officesOf(r.Context(), target).acc, set, by)
	}
	if userFrom(r).Role != storage.RoleAdmin {
		if in.Role != nil && *in.Role != string(storage.RoleMember) {
			writeError(w, http.StatusForbidden, "chỉ admin đổi được vai trò")
			return
		}
		in.Role = nil
		if in.Projects != nil {
			sc, err := s.scope(r)
			if err != nil {
				s.internal(w, r, err)
				return
			}
			for _, pid := range *in.Projects {
				if !sc.has(pid) {
					writeError(w, http.StatusBadRequest, errNotYours.Error())
					return
				}
			}
			old, err := s.cfg.Store.Users().Projects(r.Context(), id)
			if err != nil {
				s.internal(w, r, err)
				return
			}
			merged := mergeProjects(old, *in.Projects, sc)
			in.Projects = &merged
		}
	}
	if offices != nil {
		if err := s.cfg.Store.Settings().Set(r.Context(), officesKey(id), offices); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if in.Role != nil {
		if id == userFrom(r).ID && storage.Role(*in.Role) != storage.RoleAdmin {
			writeError(w, http.StatusBadRequest, "Không thể tự hạ quyền tài khoản của mình")
			return
		}
		err := s.cfg.Auth.SetRole(r.Context(), id, storage.Role(*in.Role), actor)
		switch {
		case errors.Is(err, auth.ErrInvalidRole):
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "user not found")
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
	}
	if in.Projects != nil {
		err := s.cfg.Auth.SetProjects(r.Context(), id, *in.Projects, actor)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusBadRequest, "project hoặc tài khoản không tồn tại")
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
	}
	if in.DiscordID != nil || in.TelegramID != nil { // linking the account to its bot ids
		cur, err := s.cfg.Store.Users().GetByID(r.Context(), id)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		d, tg := cur.DiscordID, cur.TelegramID
		if in.DiscordID != nil {
			d = *in.DiscordID
		}
		if in.TelegramID != nil {
			tg = *in.TelegramID
		}
		err = s.cfg.Auth.SetChatIDs(r.Context(), id, d, tg, "human:"+userFrom(r).Email)
		switch {
		case errors.Is(err, auth.ErrInvalidChatID):
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case errors.Is(err, auth.ErrChatIDTaken):
			writeError(w, http.StatusConflict, err.Error())
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
	}
	if in.Disabled != nil {
		if *in.Disabled && id == userFrom(r).ID {
			writeError(w, http.StatusBadRequest, "Không thể tự vô hiệu hóa tài khoản của mình")
			return
		}
		err := s.cfg.Auth.SetDisabled(r.Context(), id, *in.Disabled, "human:"+userFrom(r).Email)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
	}
	u, err := s.cfg.Store.Users().GetByID(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	d, err := s.withProjects(r.Context(), u)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": d})
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.memberTarget(w, r, r.PathValue("id")); !ok {
		return
	}
	err := s.cfg.Auth.ResetPassword(r.Context(), r.PathValue("id"), in.Password, "human:"+userFrom(r).Email)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
