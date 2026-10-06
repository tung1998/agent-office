// Package ops runs and watches what a project needs to run: dev servers,
// builds and tests (like pm2), and later its containers and health checks.
package ops

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Suggestion is a command found in the project, not yet managed.
type Suggestion struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Cwd         string `json:"cwd"`  // relative to the project
	Kind        string `json:"kind"` // service | job
	Source      string `json:"source"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended"` // the usual ones: dev, start, build, test
}

// Compose is a docker compose file found in the project.
type Compose struct {
	File string `json:"file"` // relative to the project
}

// Detection is what a scan found.
type Detection struct {
	PackageManager string       `json:"package_manager,omitempty"`
	Suggestions    []Suggestion `json:"suggestions"`
	Compose        []Compose    `json:"compose"`
}

// long-running scripts; everything else runs to completion
var serviceScript = regexp.MustCompile(`^(dev|start|serve|preview|watch)([:_-].*)?$|:(dev|watch|serve)$`)

var recommended = map[string]bool{"dev": true, "start": true, "build": true, "test": true, "lint": true, "preview": true}

// PackageManager picks the tool from the lockfile.
func PackageManager(dir string) string {
	for _, c := range []struct{ file, pm string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lockb", "bun"}, {"bun.lock", "bun"}, {"package-lock.json", "npm"},
	} {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.pm
		}
	}
	return "npm"
}

func runScript(pm, name string) string {
	switch pm {
	case "npm":
		if name == "start" || name == "test" {
			return "npm " + name
		}
		return "npm run " + name
	case "bun":
		return "bun run " + name
	default:
		return pm + " " + name
	}
}

// Detect scans a project folder (and one level of sub-packages, for monorepos).
func Detect(root string) Detection {
	d := Detection{Suggestions: []Suggestion{}, Compose: []Compose{}}
	dirs := []string{"."}
	for _, pat := range []string{"apps/*", "packages/*", "services/*", "frontend", "backend", "web", "api", "server", "dashboard", "ui", "client"} {
		matches, _ := filepath.Glob(filepath.Join(root, pat))
		for _, m := range matches {
			if st, err := os.Stat(filepath.Join(m, "package.json")); err == nil && !st.IsDir() {
				rel, _ := filepath.Rel(root, m)
				dirs = append(dirs, rel)
			}
		}
	}
	for _, rel := range dirs {
		dir := filepath.Join(root, rel)
		prefix := ""
		if rel != "." {
			prefix = rel + ": "
		}
		if pkg := readPackage(filepath.Join(dir, "package.json")); pkg != nil {
			pm := PackageManager(dir)
			if rel == "." {
				d.PackageManager = pm
			}
			names := make([]string, 0, len(pkg))
			for n := range pkg {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				// lifecycle hooks are not something to run by hand
				if strings.HasPrefix(n, "pre") || strings.HasPrefix(n, "post") || n == "prepare" || n == "install" {
					continue
				}
				kind := "job"
				if serviceScript.MatchString(n) {
					kind = "service"
				}
				d.Suggestions = append(d.Suggestions, Suggestion{
					Name: prefix + n, Command: runScript(pm, n), Cwd: rel, Kind: kind,
					Source: filepath.Join(rel, "package.json"), Description: pkg[n], Recommended: recommended[n] && rel == ".",
				})
			}
		}
		for _, t := range makeTargets(filepath.Join(dir, "Makefile")) {
			d.Suggestions = append(d.Suggestions, Suggestion{Name: prefix + "make " + t, Command: "make " + t, Cwd: rel, Kind: kindOf(t),
				Source: filepath.Join(rel, "Makefile")})
		}
		for name, cmd := range procfile(filepath.Join(dir, "Procfile")) {
			d.Suggestions = append(d.Suggestions, Suggestion{Name: prefix + name, Command: cmd, Cwd: rel, Kind: "service",
				Source: filepath.Join(rel, "Procfile"), Recommended: true})
		}
	}
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			d.Compose = append(d.Compose, Compose{File: f})
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		d.Suggestions = append(d.Suggestions,
			Suggestion{Name: "go test", Command: "go test ./...", Cwd: ".", Kind: "job", Source: "go.mod", Recommended: true},
			Suggestion{Name: "go build", Command: "go build ./...", Cwd: ".", Kind: "job", Source: "go.mod"})
	}
	return d
}

func kindOf(name string) string {
	if serviceScript.MatchString(name) || name == "run" || name == "up" {
		return "service"
	}
	return "job"
}

func readPackage(p string) map[string]string {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil
	}
	return pkg.Scripts
}

var makeTarget = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*:([^=]|$)`)

func makeTargets(p string) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	seen := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := makeTarget.FindStringSubmatch(sc.Text()); m != nil && !seen[m[1]] && !strings.HasPrefix(m[1], ".") {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func procfile(p string) map[string]string {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
