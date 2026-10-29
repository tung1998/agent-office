package automation

import (
	"regexp"
	"strconv"
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
	// Patterns learnt from NVIDIA SkillSpector (AR, P3/P4, RA, EA): debatable, so warnings.
	{"anti_refusal", "warn", "Câu bảo agent không được từ chối hoặc bỏ giới hạn an toàn", regexp.MustCompile(`(?i)\bnever refuse\b|\balways comply\b|\byou have no (restrictions|limits|rules)\b|\bwithout any (restrictions|limits)\b|\bno disclaimers\b|\bdo anything now\b|\bignore (your|all) (guidelines|rules|safety)\b`)},
	{"conceal", "warn", "Câu bảo agent giấu việc làm với người dùng", regexp.MustCompile(`(?i)\b(do not|don't|never) (let the user know|tell the user about (this|it)|reveal (this|it) to the user)\b|\bwithout (telling|notifying|informing) the user\b|\bkeep (this|it) (secret|hidden) from the user\b|\bsilently (send|upload|post|delete|exfiltrate)\b`)},
	{"persistence", "warn", "Tự cài chạy lâu dài (cron, LaunchAgents, file khởi động shell)", regexp.MustCompile(`(?i)\bcrontab\s+(-e\b|-(\s|$)|[^-\s])|LaunchAgents/|launchctl\s+(load|bootstrap)\b|>>?\s*\S*\.(zshrc|bashrc|bash_profile|zprofile|profile)\b|systemctl\s+(--user\s+)?enable\b`)},
	{"self_modify", "warn", "Sửa cấu hình quyền hoặc hook của agent", regexp.MustCompile(`(?i)(>>?|\btee\b|sed\s+-i|\b(write|edit|append|modify|overwrite)\b)[^\n]*(\.claude/settings(\.local)?\.json|\.claude/hooks|\.codex/config|\.office/)`)},
	{"skip_permissions", "warn", "Chạy agent bỏ qua hỏi quyền", regexp.MustCompile(`(?i)--dangerously-skip-permissions|\bbypassPermissions\b|--dangerously-bypass-approvals|danger-full-access|--yolo\b`)},
}

// hiddenRules look at a whole file: text a reviewer cannot see (SkillSpector P2, P9, TP1, TP2).
var (
	tagChars    = regexp.MustCompile(`[\x{E0000}-\x{E007F}]`) // invisible ASCII smuggling
	bidiChars   = regexp.MustCompile(`[\x{202A}-\x{202E}\x{2066}-\x{2069}]`)
	zeroWidth   = regexp.MustCompile(`[\x{200B}\x{200C}\x{2060}\x{FEFF}]|[^\x{1F000}-\x{1FAFF}\x{2600}-\x{27BF}\x{FE0F}]\x{200D}`)
	htmlComment = regexp.MustCompile(`(?s)<!--(.*?)-->`)
	commentBad  = regexp.MustCompile(`(?i)\bignore (all |the )?(previous|prior|above)\b|\bsystem prompt\b|\byou (must|should) (now )?(send|upload|run|execute|read)\b|\bdo not tell\b|\b(curl|wget)\b|\b(password|token|secret|api key|credential)s?\b|bỏ qua`)
	padding     = regexp.MustCompile(`[ \t]{200,}\S|(\n[ \t]*){40,}\S`)
)

// emojiTag keeps a subdivision flag (🏴 + tag characters) from counting as smuggling.
var emojiTag = regexp.MustCompile(`\x{1F3F4}[\x{E0020}-\x{E007F}]+`)

func hiddenFindings(name, content string) []Finding {
	out := []Finding{}
	add := func(id, severity, message string, at int) {
		line := strings.Count(content[:at], "\n") + 1
		ex := strings.TrimSpace(strings.Split(content[at:], "\n")[0])
		ex = strconv.QuoteToGraphic(ex)
		if len(ex) > 160 {
			ex = ex[:160] + "…"
		}
		out = append(out, Finding{Rule: id, Severity: severity, Message: message + " (" + name + ")", Line: line, Excerpt: ex})
	}
	blanked := emojiTag.ReplaceAllStringFunc(content, func(m string) string { return strings.Repeat(" ", len(m)) })
	if loc := tagChars.FindStringIndex(blanked); loc != nil {
		add("hidden_tag_chars", "refuse", "Có ký tự Unicode ẩn (tag) giấu chữ người duyệt không thấy", loc[0])
	}
	if loc := bidiChars.FindStringIndex(content); loc != nil {
		add("bidi_override", "warn", "Có ký tự đảo chiều văn bản (RTL/LTR override) làm chữ hiện khác thật", loc[0])
	}
	if loc := zeroWidth.FindStringIndex(content); loc != nil {
		add("zero_width", "warn", "Có ký tự vô hình (zero-width)", loc[0])
	}
	for _, m := range htmlComment.FindAllStringSubmatchIndex(content, -1) {
		if commentBad.MatchString(content[m[2]:m[3]]) {
			add("html_comment", "warn", "Comment HTML ẩn có lời dặn agent", m[0])
			break
		}
	}
	if loc := padding.FindStringIndex(content); loc != nil {
		add("whitespace_padding", "warn", "Chuỗi khoảng trắng dài đẩy chữ ra khỏi tầm nhìn", loc[0])
	}
	return out
}

// CleanToolText strips what a reader cannot see (hidden characters, HTML
// comments) from an MCP tool description and says whether it looked like an
// attempt to steer the agent (SkillSpector TP1–TP3).
func CleanToolText(s string) (string, bool) {
	flagged := len(hiddenFindings("", s)) > 0
	s = htmlComment.ReplaceAllString(s, "")
	s = tagChars.ReplaceAllString(s, "")
	s = bidiChars.ReplaceAllString(s, "")
	s = strings.NewReplacer("\u200b", "", "\u200c", "", "\u2060", "", "\ufeff", "").Replace(s)
	for _, r := range rules {
		switch r.id {
		case "prompt_injection", "anti_refusal", "conceal", "exfil_secrets", "exfil_env", "pipe_to_shell":
			if r.re.MatchString(s) {
				flagged = true
			}
		}
	}
	return s, flagged
}

// CheckContent scans text (SKILL.md, scripts, agent prompts).
func CheckContent(files map[string]string) []Finding {
	out := []Finding{}
	for name, content := range files {
		out = append(out, hiddenFindings(name, content)...)
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
