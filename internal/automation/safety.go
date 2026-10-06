package automation

import (
	"regexp"
	"strings"
)

// Finding is one thing the safety check flagged in a skill or agent.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // refuse | warn
	Message  string `json:"message"`
	Line     int    `json:"line"`
	Excerpt  string `json:"excerpt"`
}

type rule struct {
	id, severity, message string
	re                    *regexp.Regexp
}

// A short, unambiguous refuse list; everything debatable is a warning the
// admin can accept.
var rules = []rule{
	{"pipe_to_shell", "refuse", "Tải script từ mạng rồi chạy thẳng bằng shell", regexp.MustCompile(`(?i)(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z)?sh\b`)},
	{"decode_exec", "refuse", "Giải mã rồi chạy lệnh ẩn", regexp.MustCompile(`(?i)base64\s+(-d|--decode)[^\n]*\|\s*(ba|z)?sh`)},
	{"wipe", "refuse", "Xóa toàn bộ thư mục gốc hoặc home", regexp.MustCompile(`(?i)rm\s+-[a-z]*r[a-z]*f?\s+(/|~|\$HOME)(\s|$|/\*)`)},
	{"exfil_secrets", "refuse", "Đọc khóa SSH/credential rồi gửi ra ngoài", regexp.MustCompile(`(?i)(\.ssh/|id_rsa|\.aws/credentials|\.netrc)[^\n]*(curl|wget|nc\s)`)},
	{"exfil_env", "refuse", "Gửi biến môi trường ra ngoài", regexp.MustCompile(`(?i)(printenv|\benv\b|/proc/self/environ)[^\n]*\|\s*(curl|wget|nc)\b`)},
	{"prompt_injection", "warn", "Câu lệnh yêu cầu bỏ qua hướng dẫn trước đó", regexp.MustCompile(`(?i)ignore (all )?(previous|prior) instructions|bỏ qua (mọi )?hướng dẫn`)},
	{"force_push", "warn", "Có lệnh git push --force", regexp.MustCompile(`(?i)git\s+push\s+[^\n]*(--force|-f\b)`)},
	{"sudo", "warn", "Có lệnh sudo", regexp.MustCompile(`(?i)(^|\s)sudo\s`)},
	{"network", "warn", "Có lệnh gọi mạng (curl/wget)", regexp.MustCompile(`(?i)\b(curl|wget)\s+https?://`)},
	{"bash_tool", "warn", "Cho phép công cụ Bash", regexp.MustCompile(`(?i)^(allowed-tools|tools)\s*:.*\bBash\b`)},
	{"destructive_git", "warn", "Có lệnh git reset --hard / clean -fd", regexp.MustCompile(`(?i)git\s+(reset\s+--hard|clean\s+-[a-z]*f)`)},
}

// CheckContent scans text (SKILL.md, scripts, agent prompts).
func CheckContent(files map[string]string) []Finding {
	out := []Finding{}
	for name, content := range files {
		for i, line := range strings.Split(content, "\n") {
			for _, r := range rules {
				if r.re.MatchString(line) {
					ex := strings.TrimSpace(line)
					if len(ex) > 160 {
						ex = ex[:160] + "…"
					}
					out = append(out, Finding{Rule: r.id, Severity: r.severity, Message: r.message + " (" + name + ")", Line: i + 1, Excerpt: ex})
				}
			}
		}
	}
	return out
}

// Verdict summarises findings: refuse blocks, warn needs consent.
func Verdict(findings []Finding) (refuse, warn bool) {
	for _, f := range findings {
		switch f.Severity {
		case "refuse":
			refuse = true
		case "warn":
			warn = true
		}
	}
	return
}
