package officetools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/transfer"
)

// SetProjects lets the office assistant create, export and import projects
// (ADR-170); home is the office's folder (exports/, imports/ under it).
func (t *Toolbox) SetProjects(tr *transfer.Service, home string) { t.transfer, t.home = tr, home }

const answerOnlyProjects = "Trợ lý office đang ở chế độ \"Chỉ trả lời\": không tạo, xuất hay nhập project được. Nhờ người dùng chuyển sang \"Hỗ trợ quản lý office\" (Trợ lý office → Cài đặt)."

// projectTools are the assistant's tools to make and carry projects.
func (t *Toolbox) projectTools() []Tool {
	if t.transfer == nil {
		return nil
	}
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	include := map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": transfer.Includes},
		"description": "What the pack carries: config (agents, permissions, workflows), chats, memory (agents' notes). Default config. Code goes by git only."}
	return []Tool{
		{Name: "propose_create_project", Description: "PROPOSE a new project (the person approves on a card): clone url, or register an existing folder (path), or neither for a new empty git repo named after name. The folder goes in " + t.transfer.CloneRoot() + " unless path is given.",
			Schema: obj(map[string]any{"name": str("Project name"), "description": str("What the project is"), "url": str("Git URL to clone (optional)"),
				"path": str("Existing folder on this machine, absolute (optional)"), "dir": str("Folder name for a clone or a new repo (optional)"), "reason": str("Why")}, "name", "reason")},
		{Name: "export_project", Description: "Export a project as a pack (.zip) in the office's exports folder and return its path: to keep, to send, or to import in another office. Its git remote goes with no credentials; never local paths or secrets.",
			Schema: obj(map[string]any{"project": str("Project: id or name (see projects)"), "include": include}, "project")},
		{Name: "propose_import_project", Description: "PROPOSE importing a project pack (.zip from export_project): source is a file path on this machine or an http(s) link. The new project clones the pack's git remote (else url/path/a new empty repo), then takes what include picks. The person approves on a card.",
			Schema: obj(map[string]any{"source": str("Pack: absolute file path or http(s) link"), "name": str("Project name (default: the pack's)"),
				"url": str("Git URL to clone instead of the pack's (optional)"), "path": str("Existing folder instead of cloning (optional)"),
				"dir": str("Folder name (optional)"), "include": include, "reason": str("Why")}, "source", "reason")},
	}
}

// projectCall runs a project tool; ok=false: not one of them.
func (t *Toolbox) projectCall(ctx context.Context, sc Scope, name string, raw json.RawMessage) (string, bool, bool) {
	if t.transfer == nil || (name != "propose_create_project" && name != "export_project" && name != "propose_import_project") {
		return "", false, false
	}
	if sc.AnswerOnly {
		return answerOnlyProjects, true, true
	}
	var in struct {
		Project, Source, Name, Description, URL, Path, Dir, Reason string
		Include                                                    []string
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "Tham số không hợp lệ: " + err.Error(), true, true
		}
	}
	var (
		out string
		err error
	)
	switch name {
	case "export_project":
		out, err = t.exportProject(ctx, sc, in.Project, in.Include)
	case "propose_create_project":
		out, err = t.proposeProject(ctx, sc, "create_project", storage.NewProject{Name: in.Name, Description: in.Description, URL: in.URL, Path: in.Path, Dir: in.Dir}, in.Reason)
	case "propose_import_project":
		var file string
		if file, err = t.fetchPack(ctx, in.Source); err == nil {
			out, err = t.proposeProject(ctx, sc, "import_project", storage.NewProject{Name: in.Name, URL: in.URL, Path: in.Path, Dir: in.Dir, Pack: file, Include: in.Include}, in.Reason)
		}
	}
	if err != nil {
		return err.Error(), true, true
	}
	return out, false, true
}

func (t *Toolbox) exportProject(ctx context.Context, sc Scope, ref string, include []string) (string, error) {
	p, err := t.findProject(ctx, sc, ref)
	if err != nil {
		return "", err
	}
	pack, err := t.transfer.ExportPack(ctx, p.ID, include, nil)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.zip", transfer.DirName(p.Name), time.Now().Format("20060102-150405"))
	path, err := t.writePack(ctx, "exports", name, func(w io.Writer) error { return transfer.WriteZip(w, pack) })
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported %s (%s: %d chats, %d notes) to %s\nImport it with propose_import_project (source = this path, or a link to the file). Code is not inside: the new project clones %s.",
		p.Name, strings.Join(pack.Include, ", "), len(pack.Chats), len(pack.Memories), path, orNone(pack.Project.GitRemote)), nil
}

func orNone(s string) string {
	if s == "" {
		return "nothing (no git remote: an empty repo)"
	}
	return s
}

// writePack writes a file under the office's dir (exports/, imports/).
func (t *Toolbox) writePack(_ context.Context, dir, name string, write func(io.Writer) error) (string, error) {
	if t.home == "" {
		return "", errors.New("không có thư mục office để ghi file")
	}
	d := filepath.Join(t.home, dir)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(d, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	return path, f.Close()
}

// fetchPack is the pack at source: a file here, or a link downloaded to imports/.
func (t *Toolbox) fetchPack(ctx context.Context, source string) (string, error) {
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		if !filepath.IsAbs(source) {
			return "", errors.New("source phải là đường dẫn tuyệt đối tới file .zip hoặc link http(s)")
		}
		_, err := transfer.ReadPackFile(source)
		return source, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("không tải được %s: %w", source, err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("không tải được %s: %s", source, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, transfer.MaxPack+1))
	if err != nil {
		return "", err
	}
	return t.savePack(ctx, data)
}

// savePack keeps a downloaded pack in imports/ (what the card shows is what
// the approval imports).
func (t *Toolbox) savePack(ctx context.Context, data []byte) (string, error) {
	if len(data) > transfer.MaxPack {
		return "", errors.New("gói project quá lớn")
	}
	if _, err := transfer.ReadPack(data); err != nil {
		return "", err
	}
	return t.writePack(ctx, "imports", time.Now().Format("20060102-150405.000000000")+".zip", func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func (t *Toolbox) proposeProject(ctx context.Context, sc Scope, kind string, np storage.NewProject, reason string) (string, error) {
	if t.actions == nil {
		return "", errors.New("không đề xuất được ở đây")
	}
	a, err := t.actions.Propose(ctx, sc, kind, "", reason, storage.ActionArgs{NewProject: &np})
	if err != nil {
		return "", err
	}
	if a.Status == "done" {
		return "Done: " + a.Detail, nil
	}
	return fmt.Sprintf("Created approval card %q (id %s): nothing is made until the person approves. Tell the person.", a.Target, a.ID), nil
}
