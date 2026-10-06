package trigger

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// NewSecret makes a webhook secret and the hash stored in place of it.
func NewSecret() (secret, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret)
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// deliveryHeaders carry a sender's id for one delivery, used to drop repeats.
var deliveryHeaders = []string{"Idempotency-Key", "X-Request-Id", "X-GitHub-Delivery", "X-Request-UUID", "X-Atlassian-Webhook-Identifier"}

// Webhook serves POST /hooks/{id} (start a run) and GET /hooks/{id}/jobs/{job}
// (its state), both with the automation's token. A missing automation, one
// that is off, not a webhook, or a wrong token all answer 404.
func (r *Runner) Webhook() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, "/hooks/"), "/"), "/")
		a, err := r.store.Automations().Get(req.Context(), parts[0])
		if err != nil || a.Source != "webhook" || !a.Enabled || !validToken(a, req) {
			hookJSON(w, http.StatusNotFound, map[string]any{"error": "không tìm thấy"})
			return
		}
		switch {
		case req.Method == http.MethodGet && len(parts) == 3 && parts[1] == "jobs":
			j, err := r.store.Jobs().Get(req.Context(), parts[2])
			if err != nil || j.OriginID != a.ID {
				hookJSON(w, http.StatusNotFound, map[string]any{"error": "không tìm thấy"})
				return
			}
			hookJSON(w, http.StatusOK, map[string]any{"job_id": j.ID, "status": j.Status, "error_code": j.ErrorCode, "error": j.Error,
				"conversation_id": j.ConversationID, "task_id": j.TaskID, "cost_usd": j.CostUSD, "finished_at": j.FinishedAt})
		case req.Method == http.MethodPost && len(parts) == 1:
			r.receive(w, req, a)
		default:
			hookJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "phương thức không hỗ trợ"})
		}
	})
}

func validToken(a storage.Automation, req *http.Request) bool {
	var token string
	switch a.Config.Auth {
	case "header":
		token = req.Header.Get(firstNonEmpty(a.Config.AuthName, "X-Office-Token"))
	case "query":
		token = req.URL.Query().Get(firstNonEmpty(a.Config.AuthName, "token"))
	default:
		token, _ = strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	}
	if token == "" || a.Config.SecretHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashSecret(strings.TrimSpace(token))), []byte(a.Config.SecretHash)) == 1
}

func (r *Runner) receive(w http.ResponseWriter, req *http.Request, a storage.Automation) {
	limit := int64(maxPayload)
	if a.Config.PullRequest { // GitHub's PR events are large; only a compact PR is kept
		limit = 4 << 20
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, limit))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		hookJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "payload quá lớn"})
		return
	}
	if err != nil {
		hookJSON(w, http.StatusBadRequest, map[string]any{"error": "không đọc được payload"})
		return
	}
	dedupe := ""
	if a.Config.PullRequest {
		pr, ok, why := ParsePR(req.Header, body)
		if !ok { // closed, merged, a comment, a ping…: nothing to review
			hookJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": why})
			return
		}
		body, _ = json.Marshal(pr)
		dedupe = "pr:" + pr.Number + ":" + firstNonEmpty(pr.Head, pr.Event) // one review per commit
	}
	for _, h := range deliveryHeaders {
		if dedupe != "" {
			break
		}
		if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
			dedupe = h + ":" + v
			break
		}
	}
	if dedupe == "" { // no header, no PR key: hash the body (stable even when empty, so repeats dedupe too)
		sum := sha256.Sum256(body)
		dedupe = "body:" + hex.EncodeToString(sum[:])[:16]
	}
	debounce := ""
	if a.Limits.DebounceSeconds > 0 {
		var payload any
		if json.Unmarshal(body, &payload) == nil && a.Limits.DebounceKey != "" {
			debounce = lookup(payload, strings.Split(a.Limits.DebounceKey, "."))
		}
		if debounce == "" {
			debounce = "*" // no key: every delivery folds into one run
		}
	}
	j, status, err := r.Enqueue(req.Context(), a, "webhook", string(body), dedupe, debounce)
	if err != nil {
		hookJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chưa nhận được, thử lại sau"})
		return
	}
	code := http.StatusAccepted
	if status == "duplicate" {
		code = http.StatusOK
	}
	if status == "queued" {
		r.StartReady(context.WithoutCancel(req.Context()), time.Now().UTC())
	}
	hookJSON(w, code, map[string]any{"status": status, "job_id": j.ID})
}

func hookJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
