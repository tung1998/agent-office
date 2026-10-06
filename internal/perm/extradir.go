package perm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sensitiveHomeDirs are home subfolders that hold credentials or secrets: an
// extra dir may never be one of these, or anything under them (ADR-074).
var sensitiveHomeDirs = []string{".ssh", ".aws", ".gnupg", ".config", ".kube", ".docker", ".netrc", "Library/Keychains"}

// agentOfficeHome is office's own data folder when a project has no local
// ".office" of its own (cmd/office/init.go): office.db, secret.key and every
// project's data office manages live there — never an extra dir (ADR-074).
const agentOfficeHome = ".agent-office"

// CheckExtraDir validates an extra read directory an agent or an automation's
// override asks for (ADR-074 security fix): it must be a real, existing
// directory, resolved through symlinks, and outside anywhere that would hand
// over the machine or its secrets — "/", the user's home (and anything above
// it), the project's own folder (and anything above it), ~/.agent-office or
// any ".office" folder on the path (this project's or another's — office's
// own data and secrets), and credential folders under home (~/.ssh, ~/.aws, …).
func CheckExtraDir(projectPath, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("thư mục trống")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("thư mục đọc thêm phải là đường dẫn tuyệt đối: %s", dir)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("thư mục không tồn tại: %s", dir)
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("thư mục không tồn tại: %s", dir)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if realHome, err := filepath.EvalSymlinks(home); err == nil {
			home = realHome
		}
	}
	if real == string(filepath.Separator) {
		return fmt.Errorf("không được dùng thư mục gốc (/): %s", dir)
	}
	if home != "" {
		if real == home || isAncestor(real, home) {
			return fmt.Errorf("không được dùng thư mục home của người dùng hay thư mục cha của nó: %s", dir)
		}
		for _, sub := range sensitiveHomeDirs {
			sensitive := filepath.Join(home, sub)
			if real == sensitive || isAncestor(sensitive, real) {
				return fmt.Errorf("không được dùng thư mục chứa khóa/thông tin đăng nhập (~/%s): %s", sub, dir)
			}
		}
		if officeHome := filepath.Join(home, agentOfficeHome); real == officeHome || isAncestor(officeHome, real) {
			return fmt.Errorf("không được dùng thư mục dữ liệu office (~/%s): %s", agentOfficeHome, dir)
		}
	}
	if projectPath != "" {
		if realProject, err := filepath.EvalSymlinks(filepath.Clean(projectPath)); err == nil {
			if real == realProject || isAncestor(real, realProject) {
				return fmt.Errorf("không được dùng thư mục project hay thư mục cha của project: %s", dir)
			}
		}
	}
	// a ".office" folder anywhere on the path — this project's or another
	// project's office manages — always holds data/secrets, never a safe read
	if hasOfficeDirComponent(real) {
		return fmt.Errorf("không được dùng thư mục .office (dữ liệu/khóa bí mật của office): %s", dir)
	}
	return nil
}

// hasOfficeDirComponent reports whether any path component is named
// ".office" — a project's own or another project's (ADR-074): both hold
// office's data and are never a safe extra read dir.
func hasOfficeDirComponent(path string) bool {
	for p := filepath.Clean(path); ; {
		if filepath.Base(p) == ".office" {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// FilterValidExtraDirs re-checks dirs against CheckExtraDir right before a
// run uses them (ADR-074 security fix, TOCTOU): a dir is validated once at
// save time, but a symlink can be repointed afterwards (e.g. to ~/.ssh)
// without the stored path changing — so every run re-resolves and drops
// anything now invalid instead of trusting what was true when it was saved.
func FilterValidExtraDirs(projectPath string, dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if CheckExtraDir(projectPath, d) == nil {
			out = append(out, d)
		}
	}
	return out
}

// isAncestor reports whether child is dir itself or lies anywhere under it.
func isAncestor(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
