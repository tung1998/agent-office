// Package repos registers codebases for agent-office to manage.
package repos

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Info is what can be learned about a repo without reading its code.
type Info struct {
	Name        string
	Path        string
	GitRemote   string
	Description string
}

// Detect inspects dir (made absolute and symlink-resolved).
func Detect(dir string) (Info, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Info{}, err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Info{}, err
	}
	if !st.IsDir() {
		return Info{}, &os.PathError{Op: "detect", Path: abs, Err: os.ErrInvalid}
	}
	info := Info{Path: abs, Name: filepath.Base(abs)}
	if name, desc := fromPackageJSON(abs); name != "" {
		info.Name, info.Description = name, desc
	} else if name := fromComposer(abs); name != "" {
		info.Name = name
	} else if mod := fromGoMod(abs); mod != "" {
		info.Name = filepath.Base(mod)
	}
	info.GitRemote = gitRemote(abs)
	return info, nil
}

func fromPackageJSON(dir string) (string, string) {
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return "", ""
	}
	return p.Name, p.Description
}

func fromComposer(dir string) string {
	var p struct {
		Name string `json:"name"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return p.Name
}

func fromGoMod(dir string) string {
	f, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

var remoteRe = regexp.MustCompile(`(?m)^\[remote "origin"\][^\[]*?url\s*=\s*(\S+)`)

// gitRemote reads origin from .git/config and strips any credentials in it.
func gitRemote(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return ""
	}
	m := remoteRe.FindSubmatch(raw)
	if m == nil {
		return ""
	}
	url := string(m[1])
	if i := strings.Index(url, "://"); i >= 0 {
		if at := strings.Index(url[i+3:], "@"); at >= 0 {
			url = url[:i+3] + url[i+3+at+1:]
		}
	}
	return url
}
