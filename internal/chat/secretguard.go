package chat

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// homeSecrets: what in the person's home lets whoever reads it into code and
// production (SSH and cloud keys, git and registry tokens). No agent reads
// them, the administrator's included (ADR-227): a prompt injection in a web
// page or a ticket must not reach them.
var homeSecrets = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".azure", ".config/gcloud", ".config/gh",
	".docker/config.json", ".netrc", ".git-credentials", ".npmrc", ".pypirc",
	".bitbucket", ".password-store",
}

// homeDir is the person's home (a var: tests point it elsewhere).
var homeDir = func() string { h, _ := os.UserHomeDir(); return h }

// homeSecretDenies: Claude Code deny rules for the runs that are not
// administrator (an absolute path takes a second leading "/").
func homeSecretDenies() []string {
	h := homeDir()
	if h == "" {
		return nil
	}
	var out []string
	for _, s := range homeSecrets {
		p := "/" + filepath.ToSlash(filepath.Join(h, s))
		out = append(out, "Read("+p+")", "Read("+p+"/**)")
	}
	return out
}

// secretPath: why p (a file tool's path, absolute or relative to cwd) may not
// be touched, "" when it may.
func secretPath(p, cwd string) string {
	h := homeDir()
	if p == "" || h == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		p = filepath.Join(h, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	for _, t := range []string{filepath.Clean(p), realPath(p)} {
		for _, s := range homeSecrets {
			root := filepath.Join(h, s)
			if t == root || strings.HasPrefix(t, root+string(filepath.Separator)) {
				return "không đọc/sửa ~/" + s + " (khóa và token của máy)"
			}
		}
	}
	return ""
}

// secretInCommand: a shell command that names one of homeSecrets (as ~/x,
// $HOME/x or the full path). Best effort on the text.
func secretInCommand(cmd string) string {
	h := homeDir()
	for _, s := range homeSecrets {
		for _, pre := range []string{"~/", "$HOME/", "${HOME}/", h + "/"} {
			if pre == "/" {
				continue
			}
			if i := strings.Index(cmd, pre+s); i >= 0 {
				end := cmd[i+len(pre+s):]
				if end == "" || strings.ContainsAny(end[:1], "/ \t\n'\";|&)") {
					return "không đọc ~/" + s + " (khóa và token của máy)"
				}
			}
		}
	}
	return ""
}

var (
	// a host that is not this machine
	remoteURL = regexp.MustCompile(`(?i)\b(?:https?|ftp)://(?:[^/\s'"@]*@)?([^/\s:'"]+)`)
	// host[:port][/path] with no scheme, as curl takes it
	bareHost = regexp.MustCompile(`^((?:[A-Za-z0-9-]+\.)+[A-Za-z]{2,}|\d+\.\d+\.\d+\.\d+|localhost)(?::\d+)?(?:/\S*)?$`)
	// curl/wget flags that send data
	uploadFlag = regexp.MustCompile(`(?:^|\s)(?:-d|--data(?:-[a-z]+)?|-F|--form(?:-string)?|-T|--upload-file|--post-data|--post-file|--body-data|--body-file|-X\s*(?:POST|PUT|PATCH)|--request\s+(?:POST|PUT|PATCH))(?:[\s=]|$|['"@])`)
	// scp/rsync/sftp to user@host: or host:path
	remoteCopy = regexp.MustCompile(`(?:^|\s)[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:|(?:^|\s)[A-Za-z0-9.-]+\.[A-Za-z]{2,}:`)
)

func localHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.") || h == "0.0.0.0" || strings.HasSuffix(h, ".localhost")
}

// exfiltrates: why a shell command may send this machine's data out (an
// upload with curl/wget to another host, a raw socket, a copy to a remote
// host, a push to a URL rather than a named remote), "" when it does not
// look like it (ADR-227). Best effort on the text; a person runs it if needed.
func exfiltrates(cmd string) string {
	split := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "`", "\n", "$(", "\n")
	for _, seg := range strings.Split(split.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		i := 0
		for i < len(f) && (strings.Contains(f[i], "=") || f[i] == "sudo" || f[i] == "env" || f[i] == "command" || f[i] == "exec" || f[i] == "(") {
			i++
		}
		if i >= len(f) {
			continue
		}
		prog, rest := filepath.Base(f[i]), strings.Join(f[i+1:], " ")
		switch prog {
		case "nc", "ncat", "netcat", "socat", "telnet":
			return prog + " mở kết nối thô ra ngoài"
		case "curl", "wget":
			if !uploadFlag.MatchString(" " + rest) {
				continue
			}
			for _, m := range remoteURL.FindAllStringSubmatch(rest, -1) {
				if !localHost(m[1]) {
					return prog + " gửi dữ liệu tới " + m[1]
				}
			}
			for _, a := range f[i+1:] { // a host with no scheme: evil.example/x
				if m := bareHost.FindStringSubmatch(strings.Trim(a, `"'`)); m != nil && !localHost(m[1]) {
					return prog + " gửi dữ liệu tới " + m[1]
				}
			}
		case "scp", "sftp", "rsync":
			if remoteCopy.MatchString(" " + rest) {
				return prog + " chép file sang máy khác"
			}
		case "git":
			j := i + 1
			for j < len(f) && strings.HasPrefix(f[j], "-") {
				if f[j] == "-C" || f[j] == "-c" {
					j++
				}
				j++
			}
			if j < len(f) && f[j] == "push" {
				for _, a := range f[j+1:] {
					if strings.HasPrefix(a, "-") {
						continue
					}
					if strings.Contains(a, "://") || remoteCopy.MatchString(" "+a) {
						return "git push tới một URL lạ (chỉ push lên remote đã cấu hình)"
					}
					break
				}
			}
		}
	}
	return ""
}
