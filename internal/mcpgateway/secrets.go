package mcpgateway

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Sealer encrypts the servers' secrets (the office's secrets.Box).
type Sealer interface {
	Seal(plain string) (string, error)
	Open(enc string) (string, error)
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName: lower-case letters, digits and "-", the way tools show up as
// mcp__<name>__*; "office" is the office's own server.
func ValidName(name string) bool {
	return len(name) <= 40 && nameRe.MatchString(name) && name != "office"
}

// SealMap encrypts m as JSON ("" when empty).
func SealMap(b Sealer, m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return b.Seal(string(raw))
}

// OpenMap decrypts what SealMap made.
func OpenMap(b Sealer, enc string) (map[string]string, error) {
	out := map[string]string{}
	if enc == "" {
		return out, nil
	}
	raw, err := b.Open(enc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Mask hides a secret value: only its last 4 characters show, and only when
// it is long enough that they give nothing away.
func Mask(v string) string {
	if len(v) < 12 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

// MaskMap masks every value of m.
func MaskMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = Mask(v)
	}
	return out
}

// MergeHeaders applies an edit to the stored headers: a key with an empty
// value keeps its stored value, keys not in edit are dropped.
func MergeHeaders(old, edit map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range edit {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if v = strings.TrimSpace(v); v == "" {
			if o, ok := old[k]; ok {
				out[k] = o
			}
			continue
		}
		out[k] = v
	}
	return out
}
