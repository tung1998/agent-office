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
}

// withProjects adds the projects an account other than an admin sees.
func (s *server) withProjects(ctx context.Context, u storage.User) (userDTO, error) {
	d := toDTO(u)
	if u.Role == storage.RoleAdmin {
		return d, nil
	}
	ids, err := s.cfg.Store.Users().Projects(ctx, u.ID)
	d.Projects = ids
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
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	// build: which office is running, shown under the sidebar for everyone
	d, err := s.withProjects(r.Context(), userFrom(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": d, "build": s.build})
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
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		d, err := s.withProjects(r.Context(), u)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string   `json:"email"`
		Name     string   `json:"name"`
		Role     string   `json:"role"`
		Password string   `json:"password"`
		Projects []string `json:"projects"` // what a manager or member sees (ADR-224)
	}
	if !decode(w, r, &in) {
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
		Disabled   *bool     `json:"disabled"`
		DiscordID  *string   `json:"discord_id"`
		TelegramID *string   `json:"telegram_id"`
		Role       *string   `json:"role"`
		Projects   *[]string `json:"projects"` // what a manager or member sees (ADR-224)
	}
	if !decode(w, r, &in) {
		return
	}
	actor := "human:" + userFrom(r).Email
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
