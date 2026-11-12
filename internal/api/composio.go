package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/tung1998/agent-office/internal/composio"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// The office's Composio connection (ADR-282): an API key (sealed), the
// trigger types to pick a webhook's app events from, and one press to point
// the Composio project's webhook subscription at a webhook of office, whose
// secret then signs every delivery there. Admin only: the key reaches every
// app the Composio project connects.

func (s *server) composioRoutes(mux *http.ServeMux) {
	admin := func(h http.HandlerFunc) http.Handler { return s.requireRole(storage.RoleAdmin, h) }
	mux.Handle("GET /api/composio", admin(s.composioGet))
	mux.Handle("PUT /api/composio", admin(s.composioPut))
	mux.Handle("DELETE /api/composio", admin(s.composioDelete))
	mux.Handle("GET /api/composio/triggers", admin(s.composioTriggers))
	mux.Handle("POST /api/automations/{id}/composio", admin(s.composioSubscribe))
}

// composioBase: tests point the client at a fake Composio.
var composioBase = ""

func (s *server) composioBox() *secrets.Box {
	if s.cfg.Providers == nil {
		return nil
	}
	return s.cfg.Providers.Box()
}

func (s *server) composioConf(w http.ResponseWriter, r *http.Request) (composio.Conf, bool) {
	box := s.composioBox()
	if box == nil {
		writeError(w, http.StatusServiceUnavailable, "office chưa có khoá mã hoá")
		return composio.Conf{}, false
	}
	c, err := composio.Load(r.Context(), s.cfg.Store, box)
	if err != nil {
		s.internal(w, r, err)
		return c, false
	}
	return c, true
}

func (s *server) composioGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.composioConf(w, r)
	if !ok {
		return
	}
	out := map[string]any{"connected": c.APIKey != "", "key_hint": secrets.Hint(c.APIKey), "automation_id": c.AutomationID,
		"project_id": c.ProjectID, "signed": c.WebhookSecret != ""}
	if c.AutomationID != "" {
		if a, err := s.cfg.Store.Automations().Get(r.Context(), c.AutomationID); err == nil {
			out["automation_name"] = a.Name
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT {api_key}: checked with Composio first; a new key forgets the
// subscription of the old one.
func (s *server) composioPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIKey string `json:"api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.APIKey = strings.TrimSpace(in.APIKey)
	if in.APIKey == "" {
		writeError(w, http.StatusBadRequest, "hãy dán API key của Composio")
		return
	}
	cur, ok := s.composioConf(w, r)
	if !ok {
		return
	}
	if _, err := (composio.Client{Key: in.APIKey, Base: composioBase}).TriggerTypes(r.Context(), []string{"gmail"}, 1); err != nil {
		s.composioError(w, err)
		return
	}
	c := composio.Conf{APIKey: in.APIKey}
	if cur.APIKey == in.APIKey {
		c = cur
	}
	if err := composio.Save(r.Context(), s.cfg.Store, s.composioBox(), c); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "composio.connect", "composio", map[string]any{"key": secrets.Hint(in.APIKey)})
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "key_hint": secrets.Hint(in.APIKey)})
}

// DELETE: office forgets the key (the subscription stays at Composio, its
// deliveries then checked by the webhook's token only).
func (s *server) composioDelete(w http.ResponseWriter, r *http.Request) {
	if s.composioBox() == nil {
		writeError(w, http.StatusServiceUnavailable, "office chưa có khoá mã hoá")
		return
	}
	if err := composio.Save(r.Context(), s.cfg.Store, s.composioBox(), composio.Conf{}); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "composio.disconnect", "composio", nil)
	w.WriteHeader(http.StatusNoContent)
}

// GET ?toolkit=gmail,slack: the trigger types to pick from.
func (s *server) composioTriggers(w http.ResponseWriter, r *http.Request) {
	c, ok := s.composioConf(w, r)
	if !ok {
		return
	}
	if c.APIKey == "" {
		writeError(w, http.StatusConflict, "chưa nối Composio: dán API key ở trang Tự động hoá")
		return
	}
	var kits []string
	for _, k := range strings.Split(r.URL.Query().Get("toolkit"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kits = append(kits, k)
		}
	}
	list, err := (composio.Client{Key: c.APIKey, Base: composioBase}).TriggerTypes(r.Context(), kits, 300)
	if err != nil {
		s.composioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": list})
}

// POST /api/automations/{id}/composio {base_url}: the webhook gets a new
// token, and the Composio project's subscription points at its URL; the
// secret Composio signs with is kept, so only signed deliveries run it.
func (s *server) composioSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseURL string `json:"base_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		writeError(w, http.StatusBadRequest, "Composio cần địa chỉ HTTPS công khai của office (ví dụ qua tunnel)")
		return
	}
	c, ok := s.composioConf(w, r)
	if !ok {
		return
	}
	if c.APIKey == "" {
		writeError(w, http.StatusConflict, "chưa nối Composio: dán API key trước")
		return
	}
	a, err := s.cfg.Store.Automations().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	defer s.lockAutomations(a.ProjectID)()
	if a, err = s.cfg.Store.Automations().Get(r.Context(), a.ID); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if a.Source != "webhook" || a.Config.PullRequest || a.Config.Auth != "query" {
		writeError(w, http.StatusBadRequest, "chỉ nối được webhook nguồn \"Sự kiện từ app\" (token trong URL)")
		return
	}
	secret, hash := trigger.NewSecret()
	hook := base.String() + "/hooks/" + a.ID + "?" + url.Values{firstNonEmptyStr(a.Config.AuthName, "token"): {secret}}.Encode()
	sub, err := (composio.Client{Key: c.APIKey, Base: composioBase}).Subscribe(r.Context(), hook)
	if err != nil {
		s.composioError(w, err)
		return
	}
	// the new token only once Composio has the URL carrying it
	if err := s.cfg.Store.Automations().UpdateSecretHash(r.Context(), a.ID, hash); err != nil {
		s.internal(w, r, err)
		return
	}
	c.SubscriptionID, c.WebhookSecret, c.AutomationID, c.ProjectID = sub.ID, sub.Secret, a.ID, a.ProjectID
	if err := composio.Save(r.Context(), s.cfg.Store, s.composioBox(), c); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "composio.subscribe", a.ID, map[string]any{"subscription": sub.ID, "base_url": base.String()})
	writeJSON(w, http.StatusOK, map[string]any{"subscription_id": sub.ID, "signed": sub.Secret != "", "url": base.String() + "/hooks/" + a.ID})
}

func (s *server) composioError(w http.ResponseWriter, err error) {
	if errors.Is(err, composio.ErrUnauthorized) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}
