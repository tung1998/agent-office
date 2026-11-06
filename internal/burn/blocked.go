package burn

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
)

// A technical error (ADR-152) is the machine's, not the piece's: an AI
// connection signed out or refused (401/403), its service down (5xx), the
// network. Every piece would fail the same way, so none is failed for it:
// the piece stays where it was (no try counted), the Burn says so in its
// chat and its bot, and waits ("blocked"), trying again every blockedRetry
// until a turn goes through, or at once when the person resumes it.

// blockedRetry: how long a blocked Burn waits before it tries again.
const blockedRetry = 15 * time.Minute

// technicalErr: how the AI CLIs, their APIs and the network say a call did
// not get through (not what the agent answered).
var technicalErr = regexp.MustCompile(`(?i)(status|error|code)[: ]+(401|403|5\d\d)\b|\b(401|403) (unauthori[sz]ed|forbidden)|unauthori[sz]ed|invalidated oauth|oauth token|could not be refreshed|token_expired|sign(ing)? in again|not logged in|chưa đăng nhập|connection (refused|reset)|no such host|i/o timeout|tls handshake|network is unreachable|service unavailable|bad gateway|gateway timeout|overloaded|executable file not found|failed to read prompt|not valid utf-8`)

// technical: msg is a technical error, not the piece's.
func technical(err error) bool {
	if err == nil {
		return false
	}
	// a passing failure of the AI service the CLI gave up on (a 529 retried
	// to its end, a dropped stream: ADR-260) is not the piece's either
	return errors.Is(err, llm.ErrNeedsLogin) || technicalErr.MatchString(err.Error()) || chat.Transient(err)
}

func technicalText(msg string) bool {
	return msg != "" && (technicalErr.MatchString(msg) || chat.Transient(errors.New(msg)))
}

// block puts the Burn to wait after a technical error: said once (chat and
// bot), then tried again after blockedRetry. Draining, it stays draining
// and only waits.
func (s *Service) block(ctx context.Context, projectID, why string) {
	ctx = context.WithoutCancel(ctx)
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil || !b.Active() {
		return
	}
	first := b.State != "blocked" && b.WaitingUntil == nil
	until := time.Now().Add(blockedRetry)
	if b.State != "draining" {
		b.State = "blocked"
	}
	b.WaitingUntil = &until
	if _, err := s.store.Burn().SaveSession(ctx, b); err != nil {
		return
	}
	if first {
		s.alert(ctx, b, "⚠️ **Burn tạm dừng: lỗi kỹ thuật** (không phải lỗi của việc, việc giữ nguyên, không tính lượt thử): "+oneLine(why, 400)+
			"\nSửa xong (vd đăng nhập lại kết nối AI) rồi bấm **Thử lại ngay**, hoặc Burn tự thử lại lúc "+until.Local().Format("15:04")+".")
	}
}

// blocked: the Burn waits on a technical error; its loop does the waiting
// (and a resume wakes it), so a step does not hold its piece meanwhile.
func (s *Service) blocked(ctx context.Context, b storage.BurnSession) bool {
	cur, err := s.store.Burn().SessionByID(ctx, b.ID)
	return err == nil && (cur.State == "blocked" || cur.WaitingUntil != nil && cur.WaitingUntil.After(time.Now()))
}

// unblock: a turn went through, the blocked Burn works again.
func (s *Service) unblock(ctx context.Context, projectID string) {
	ctx = context.WithoutCancel(ctx)
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil || b.State != "blocked" {
		return
	}
	b.State, b.WaitingUntil = "running", nil
	if _, err := s.store.Burn().SaveSession(ctx, b); err == nil {
		s.alert(ctx, b, "✅ Đã chạy lại được: Burn tiếp tục.")
	}
}

// alert says text in the Burn's chat and its bot's.
func (s *Service) alert(ctx context.Context, b storage.BurnSession, text string) {
	s.say(ctx, b, text)
	if s.notify == nil || b.NotifyChannelID == "" || b.NotifyChatID == "" {
		return
	}
	go func() {
		if err := s.notify(ctx, b.NotifyChannelID, b.NotifyChatID, text); err != nil {
			slog.Warn("burn: alert to the bot", "project", b.ProjectID, "err", err)
		}
	}()
}
