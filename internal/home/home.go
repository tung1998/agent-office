// Package home locates the office data directory.
//
// Two install modes:
//   - local:  <project>/.office — the office manages that project only.
//   - global: ~/.agent-office   — one office manages repos anywhere on the machine.
package home

import (
	"os"
	"path/filepath"
)

// EnvHome overrides the data directory.
const EnvHome = "OFFICE_HOME"

// DirName is the per-project data directory.
const DirName = ".office"

// Mode is local or global.
type Mode string

const (
	Local  Mode = "local"
	Global Mode = "global"
)

// Home is a resolved data directory.
type Home struct {
	Dir         string // data directory
	Mode        Mode
	ProjectRoot string // local mode: the project owning Dir
}

// DB is the SQLite path.
func (h Home) DB() string { return filepath.Join(h.Dir, "office.db") }

// SecretKey is the encryption key file.
func (h Home) SecretKey() string { return filepath.Join(h.Dir, "secret.key") }

// Resolve picks the data directory: explicit flag, OFFICE_HOME, the nearest
// ancestor of cwd holding .office/, else the global ~/.agent-office.
func Resolve(flag, cwd string) (Home, error) {
	if flag != "" {
		return fromDir(flag)
	}
	if env := os.Getenv(EnvHome); env != "" {
		return fromDir(env)
	}
	if root := FindLocal(cwd); root != "" {
		return Home{Dir: filepath.Join(root, DirName), Mode: Local, ProjectRoot: root}, nil
	}
	return GlobalHome()
}

// GlobalHome is ~/.agent-office.
func GlobalHome() (Home, error) {
	u, err := os.UserHomeDir()
	if err != nil {
		return Home{}, err
	}
	return Home{Dir: filepath.Join(u, ".agent-office"), Mode: Global}, nil
}

// LocalHome is <root>/.office.
func LocalHome(root string) (Home, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Home{}, err
	}
	return Home{Dir: filepath.Join(abs, DirName), Mode: Local, ProjectRoot: abs}, nil
}

// FindLocal walks up from dir looking for a .office directory with a database.
func FindLocal(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, DirName, "office.db")); err == nil {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

func fromDir(dir string) (Home, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Home{}, err
	}
	h := Home{Dir: abs, Mode: Global}
	if filepath.Base(abs) == DirName {
		h.Mode, h.ProjectRoot = Local, filepath.Dir(abs)
	}
	return h, nil
}
