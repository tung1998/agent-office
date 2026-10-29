package embed

import (
	"cmp"
	"context"
	"os"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// Env names a server directly; it wins over the setting.
const (
	EnvURL   = "OFFICE_EMBED_URL"
	EnvKey   = "OFFICE_EMBED_KEY"
	EnvModel = "OFFICE_EMBED_MODEL"
)

const settingKey = "embed"

// DefaultModel is what an OpenAI connection embeds with when none is set.
const DefaultModel = "text-embedding-3-small"

// Settings: which AI connection embeds the notes. Provider "" = the first
// enabled OpenAI connection with a key; Off = none even then.
type Settings struct {
	Off      bool   `json:"off,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

func Load(ctx context.Context, st storage.Store) Settings {
	var s Settings
	_, _ = st.Settings().Get(ctx, settingKey, &s)
	return s
}

func Save(ctx context.Context, st storage.Store, s Settings) error {
	return st.Settings().Set(ctx, settingKey, s)
}

// Resolver finds the client each round: the env, else the setting's
// connection, else the first OpenAI one; nil = off. key opens a
// connection's API key.
func Resolver(st storage.Store, key func(storage.Provider) (string, error)) func(context.Context) *Client {
	return func(ctx context.Context) *Client {
		if u := strings.TrimSpace(os.Getenv(EnvURL)); u != "" {
			return newClient(u, strings.TrimSpace(os.Getenv(EnvKey)), cmp.Or(strings.TrimSpace(os.Getenv(EnvModel)), DefaultModel))
		}
		s := Load(ctx, st)
		if s.Off {
			return nil
		}
		list, err := st.Providers().List(ctx)
		if err != nil {
			return nil
		}
		for _, p := range list {
			if !p.Enabled || s.Provider != "" && p.ID != s.Provider && p.Name != s.Provider {
				continue
			}
			url, model := Endpoint(p, s.Model)
			if url == "" || model == "" {
				continue
			}
			k, err := key(p)
			if err != nil {
				k = "" // a local server (Ollama) needs none; OpenAI does
			}
			if k == "" && p.Kind == storage.ProviderOpenAI {
				continue
			}
			return newClient(url, k, model)
		}
		return nil
	}
}

// Endpoint is where connection p embeds and with what model ("" = it
// cannot: only OpenAI and OpenAI-compatible connections, the latter with a
// model named).
func Endpoint(p storage.Provider, model string) (url, m string) {
	switch p.Kind {
	case storage.ProviderOpenAI:
		return cmp.Or(p.BaseURL, "https://api.openai.com/v1"), cmp.Or(model, DefaultModel)
	case storage.ProviderOpenAICompatible:
		return p.BaseURL, model
	}
	return "", ""
}
