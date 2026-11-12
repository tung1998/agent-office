package composio

import (
	"context"
	"encoding/json"

	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
)

// settingKey holds the office's Composio connection, sealed: its API key, and
// the subscription office pointed at one of its webhooks with the secret
// that signs the deliveries.
const settingKey = "composio"

// Conf is the office's Composio connection.
type Conf struct {
	APIKey         string `json:"api_key"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	WebhookSecret  string `json:"webhook_secret,omitempty"`
	AutomationID   string `json:"automation_id,omitempty"` // the webhook its URL points at
	ProjectID      string `json:"project_id,omitempty"`
}

// Load reads the connection (zero when there is none).
func Load(ctx context.Context, st storage.Store, box *secrets.Box) (Conf, error) {
	var sealed string
	var c Conf
	if ok, err := st.Settings().Get(ctx, settingKey, &sealed); err != nil || !ok || sealed == "" {
		return c, err
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(plain), &c)
}

// Save writes it sealed; a zero Conf removes it.
func Save(ctx context.Context, st storage.Store, box *secrets.Box, c Conf) error {
	if c == (Conf{}) {
		return st.Settings().Set(ctx, settingKey, "")
	}
	raw, _ := json.Marshal(c)
	sealed, err := box.Seal(string(raw))
	if err != nil {
		return err
	}
	return st.Settings().Set(ctx, settingKey, sealed)
}
