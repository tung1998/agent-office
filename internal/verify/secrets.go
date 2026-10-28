package verify

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// secretRules: what a key or token looks like, by its own shape (ADR-227),
// the way gitleaks finds them; no model, no tool to install.
var secretRules = []struct {
	name string
	re   *regexp.Regexp
	fake func(m []string) bool // an example value, not a secret
}{
	{"khóa riêng (private key)", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY(?: BLOCK)?-----`), nil},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), nil},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{50,})\b`), nil},
	{"GitLab token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`), nil},
	{"Bitbucket token", regexp.MustCompile(`\bATBB[A-Za-z0-9_=-]{28,}\b`), nil},
	{"Atlassian token", regexp.MustCompile(`\bATATT3[A-Za-z0-9_=-]{40,}\b`), nil},
	{"Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`), nil},
	{"Anthropic key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`), nil},
	{"OpenAI key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20}T3BlbkFJ[A-Za-z0-9_-]{20,}\b`), nil},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), nil},
	{"Stripe key", regexp.MustCompile(`\b[sr]k_live_[0-9A-Za-z]{20,}\b`), nil},
	{"Telegram bot token", regexp.MustCompile(`\b[0-9]{8,10}:AA[0-9A-Za-z_-]{33}\b`), nil},
	{"Discord bot token", regexp.MustCompile(`\b[MNO][A-Za-z0-9_-]{23,25}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,38}\b`), nil},
	{"chuỗi kết nối có mật khẩu", regexp.MustCompile(`\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqps?)://[^\s:/@'"]+:([^\s@'"$<{]{6,})@`), placeholder},
}

// placeholder: the passwords docs and tests use as examples.
func placeholder(m []string) bool {
	switch strings.ToLower(m[len(m)-1]) {
	case "password", "postgres", "secret", "changeme", "example", "passw0rd", "mysql", "redis", "test", "testpass", "testing":
		return true
	}
	return false
}

// maxScan caps the bytes read of one file (a minified bundle, a dump).
const maxScan = 2 << 20

// Secrets scans the files of a diff in dir for keys and tokens (ADR-227):
// "" when none, else where (file:line and what kind, never the value).
// A line with "gitleaks:allow" is skipped, as gitleaks does.
func Secrets(dir string, files []string) string {
	var found []string
	for _, f := range files {
		p := filepath.Join(dir, f)
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxScan {
			continue // deleted, a folder, or too big
		}
		fh, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64<<10), maxScan)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if strings.Contains(line, "gitleaks:allow") {
				continue
			}
			for _, r := range secretRules {
				if m := r.re.FindStringSubmatch(line); m != nil && (r.fake == nil || !r.fake(m)) {
					found = append(found, fmt.Sprintf("- `%s:%d`: %s", f, n, r.name))
					break
				}
			}
		}
		fh.Close()
		if len(found) >= 20 {
			break
		}
	}
	if len(found) == 0 {
		return ""
	}
	return "Có secret trong diff (gỡ ra, để vào biến môi trường hoặc trình quản lý mật khẩu; ví dụ giả thì thêm `gitleaks:allow` cuối dòng):\n" + strings.Join(found, "\n")
}
