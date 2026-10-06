// Package limitalert tells a chat (a bot's Discord channel or Telegram chat)
// when an AI connection's usage window (Claude's 5-hour, weekly…) passes a
// threshold: once a cycle, and once more near the end (95%).
package limitalert

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Settings is where alerts go, and from how full (percent; 0 = 80).
type Settings struct {
	ChannelID string `json:"channel_id"`
	ChatID    string `json:"chat_id"`
	Threshold int    `json:"threshold"`
}

const key = "limit_alert"

func Load(ctx context.Context, st storage.Store) Settings {
	var s Settings
	_, _ = st.Settings().Get(ctx, key, &s)
	if s.Threshold <= 0 || s.Threshold > 99 {
		s.Threshold = 80
	}
	return s
}

func Save(ctx context.Context, st storage.Store, s Settings) error {
	return st.Settings().Set(ctx, key, s)
}

// Notify sends text to a bot's chat.
type Notify func(ctx context.Context, channelID, chatID, text string) error

// Alerter checks windows as they are reported.
type Alerter struct {
	store  storage.Store
	notify Notify
	mu     sync.Mutex // runs ending together: one of them tells
}

func New(st storage.Store, n Notify) *Alerter { return &Alerter{store: st, notify: n} }

// WindowNames are how people read the windows.
var WindowNames = map[string]string{"five_hour": "giới hạn 5 giờ", "seven_day": "giới hạn tuần (mọi model)"} // i18n-ignore

// Check tells the chat when a window (utilization 0..1, resetting then) passes
// the threshold, or 95%, for the first time this cycle.
func (a *Alerter) Check(ctx context.Context, providerID, providerName, window string, util float64, resetsAt time.Time) {
	s := Load(ctx, a.store)
	if s.ChannelID == "" || s.ChatID == "" || a.notify == nil {
		return
	}
	pct := int(util*100 + 0.5)
	level := 0
	switch {
	case pct >= 95:
		level = 95
	case pct >= s.Threshold:
		level = s.Threshold
	default:
		return
	}
	seen := "limit_alerted/" + providerID + "/" + window + "/" + strconv.FormatInt(resetsAt.Unix()/60, 10)
	var told int
	a.mu.Lock()
	if _, _ = a.store.Settings().Get(ctx, seen, &told); told >= level {
		a.mu.Unlock()
		return
	}
	_ = a.store.Settings().Set(ctx, seen, level)
	a.mu.Unlock()
	name := WindowNames[window]
	if name == "" {
		name = "giới hạn " + window // i18n-ignore
	}
	text := fmt.Sprintf("⚠️ %s: %s đã dùng %d%%, reset lúc %s.", providerName, name, pct, resetsAt.Local().Format("15:04 02/01")) // i18n-ignore
	if level >= 95 {
		text += " Sắp hết: job mới có thể dừng giữa chừng." // i18n-ignore
	}
	_ = a.notify(ctx, s.ChannelID, s.ChatID, text)
}
