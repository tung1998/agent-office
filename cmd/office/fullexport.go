package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/tung1998/agent-office/internal/fullexport"
)

// EnvExportPassphrase gives the passphrase without a prompt (scripts); never a flag:
// a flag stays in the shell's history.
const EnvExportPassphrase = "OFFICE_EXPORT_PASSPHRASE"

// exportPassphrase reads the passphrase from EnvExportPassphrase or the
// terminal (twice when it makes a new export).
func exportPassphrase(twice bool) (string, error) {
	if v := os.Getenv(EnvExportPassphrase); v != "" {
		return v, nil
	}
	if !interactive() {
		return "", fmt.Errorf("cần passphrase: chạy trong terminal hoặc đặt %s", EnvExportPassphrase)
	}
	ask := func(q string) (string, error) {
		fmt.Fprint(os.Stderr, q)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	p, err := ask(fmt.Sprintf("Passphrase (ít nhất %d ký tự, mất là không mở được bản export): ", fullexport.MinPassphrase))
	if err != nil || !twice {
		return p, err
	}
	again, err := ask("Nhập lại passphrase: ")
	if err != nil {
		return "", err
	}
	if again != p {
		return "", errors.New("hai lần nhập passphrase không khớp")
	}
	return p, nil
}

// runFullExport writes the whole office to file (ADR-274).
func runFullExport(ctx context.Context, file string) error {
	if file == "" {
		file = "office-" + time.Now().Format("20060102-150405") + ".tar.zst.age"
	}
	pass, err := exportPassphrase(true)
	if err != nil {
		return err
	}
	if len([]rune(pass)) < fullexport.MinPassphrase {
		return fmt.Errorf("passphrase cần ít nhất %d ký tự", fullexport.MinPassphrase)
	}
	return withApp(ctx, func(a *app) error {
		repos, err := a.store.Repos().List(ctx)
		if err != nil {
			return err
		}
		refs := make([]fullexport.ProjectRef, 0, len(repos))
		for _, r := range repos {
			refs = append(refs, fullexport.ProjectRef{ID: r.ID, Name: r.Name, Path: r.Path})
		}
		snap, ok := a.store.(interface {
			Backup(ctx context.Context, dest string) error
		})
		if !ok {
			return errors.New("storage hiện tại không hỗ trợ chụp database")
		}
		tmp := file + ".part"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		m, err := fullexport.Export(ctx, f, fullexport.ExportOptions{
			HomeDir: a.home.Dir, ProjectRoot: a.home.ProjectRoot, OfficeVersion: version, KeyPath: a.home.SecretKey(),
			Snapshot: func(dest string) error { return snap.Backup(ctx, dest) }, Projects: refs, Passphrase: pass,
			Log: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
		})
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, file); err != nil {
			return err
		}
		st, _ := os.Stat(file)
		fmt.Printf("✓ Đã xuất toàn bộ office → %s (%.1f MB, mã hóa bằng passphrase)\n", file, float64(st.Size())/(1<<20))
		for _, p := range m.Projects {
			fmt.Printf("  %-10s %s%s\n", p.Kind, p.Name, projectExtra(p))
		}
		fmt.Println("File này mở được mọi API key và token của office: giữ như mật khẩu, không đưa lên git hay chat.")
		return nil
	})
}

func projectExtra(p fullexport.Project) string {
	var parts []string
	if p.Bundle && p.Kind == fullexport.KindRemote {
		parts = append(parts, "có commit chưa push")
	}
	if p.Patch {
		parts = append(parts, "có thay đổi chưa commit")
	}
	if p.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d file chưa track", p.Untracked))
	}
	if p.Files > 0 {
		parts = append(parts, fmt.Sprintf("%d file", p.Files))
	}
	if p.Note != "" {
		parts = append(parts, p.Note)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// runFullImport restores a whole office from file (ADR-274). The office here
// must not be running: its database is replaced.
func runFullImport(ctx context.Context, file, mode, projectsDir string, force, yes bool) error {
	h, err := resolveHome()
	if err != nil {
		return err
	}
	if mode == "" {
		mode = fullexport.ModeMove
	}
	switch mode {
	case fullexport.ModeMove:
		fmt.Println("Chế độ CHUYỂN MÁY: bot, tự động hóa và peer chạy y như máy cũ. Dừng office ở máy cũ trước, nếu không hai bên sẽ giành tin nhắn của cùng bot.")
	case fullexport.ModeClone:
		fmt.Println("Chế độ NHÂN BẢN: bot và tự động hóa vào ở trạng thái tắt, ghép đôi peer bị bỏ; máy cũ chạy tiếp bình thường.")
	default:
		return fmt.Errorf("--mode phải là move hoặc clone")
	}
	if projectsDir == "" && h.ProjectRoot != "" {
		projectsDir = filepath.Dir(h.ProjectRoot)
	}
	fmt.Printf("Dữ liệu office: %s\nCode các project: %s\n", h.Dir, projectsDir)
	if !yes && (!interactive() || !confirm("Dừng office ở máy này (nếu đang chạy) rồi tiếp tục?", false)) {
		fmt.Println("Đã hủy.")
		return nil
	}
	pass, err := exportPassphrase(false)
	if err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	rep, err := fullexport.Import(ctx, f, fullexport.ImportOptions{
		HomeDir: h.Dir, ProjectRoot: h.ProjectRoot, KeyPath: h.SecretKey(), ProjectsDir: projectsDir,
		Passphrase: pass, Mode: mode, Force: force,
		Log: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
	})
	if err != nil {
		return err
	}
	fmt.Printf("✓ Đã nhập office xuất lúc %s (%d file dữ liệu, %d giá trị đổi đường dẫn)\n",
		rep.Manifest.ExportedAt.Local().Format("02/01/2006 15:04"), rep.Files, rep.Remapped)
	for _, p := range rep.Projects {
		mark := "✓"
		if !p.OK {
			mark = "!"
		}
		fmt.Printf("  %s %s → %s", mark, p.Name, p.To)
		if p.Note != "" {
			fmt.Printf(" (%s)", p.Note)
		}
		fmt.Println()
	}
	for _, w := range rep.Warnings {
		fmt.Println("• " + w)
	}
	fmt.Println("Chạy `office run` để khởi động office. Migration tự chạy nếu bản export cũ hơn office này.")
	return nil
}

// snapshotOf: a consistent copy of a's database (nil when its storage cannot).
func snapshotOf(a *app) func(ctx context.Context, dest string) error {
	b, ok := a.store.(interface {
		Backup(ctx context.Context, dest string) error
	})
	if !ok {
		return nil
	}
	return b.Backup
}
