// Package fullexport moves a whole office to another machine (ADR-274): its
// database and key, its files (attachments, library, assistant, the AI
// accounts' config without their logins) and every project's code: a project
// with a git remote goes as its remote, branch and commit plus what is not
// pushed (a git bundle) and not committed (a patch and the untracked files);
// a local repo goes as a bundle of its whole history; a folder without git
// goes as its files. One file: tar, compressed with zstd, encrypted with a
// passphrase (age, scrypt), since it opens every key and token of the office.
package fullexport

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// MinPassphrase is the shortest passphrase an export takes.
const MinPassphrase = 12

// ErrPassphrase: the passphrase does not open the file (or it is not an export).
var ErrPassphrase = errors.New("fullexport: sai passphrase hoặc file không phải bản export đầy đủ")

// archiveWriter is tar → zstd → age → dst.
type archiveWriter struct {
	tw *tar.Writer
	zw *zstd.Encoder
	aw io.WriteCloser
}

func newArchiveWriter(dst io.Writer, passphrase string) (*archiveWriter, error) {
	if len([]rune(passphrase)) < MinPassphrase {
		return nil, fmt.Errorf("fullexport: passphrase cần ít nhất %d ký tự", MinPassphrase)
	}
	rcp, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	aw, err := age.Encrypt(dst, rcp)
	if err != nil {
		return nil, err
	}
	zw, err := zstd.NewWriter(aw, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	return &archiveWriter{tw: tar.NewWriter(zw), zw: zw, aw: aw}, nil
}

func (a *archiveWriter) Close() error {
	return errors.Join(a.tw.Close(), a.zw.Close(), a.aw.Close())
}

func (a *archiveWriter) addBytes(name string, data []byte) error {
	if err := a.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := a.tw.Write(data)
	return err
}

// addFile adds the regular file at p as name.
func (a *archiveWriter) addFile(name, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	h := &tar.Header{Name: name, Mode: int64(st.Mode().Perm()), Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}
	if err := a.tw.WriteHeader(h); err != nil {
		return err
	}
	_, err = io.Copy(a.tw, f)
	return err
}

func (a *archiveWriter) addSymlink(name, target string) error {
	return a.tw.WriteHeader(&tar.Header{Name: name, Linkname: target, Mode: 0o777, ModTime: time.Now(), Typeflag: tar.TypeSymlink})
}

// addTree adds root's files under prefix. skip(rel, d) leaves an entry (and,
// for a folder, all it holds) out. Symlinks go as links when links is set,
// else they are left out.
func (a *archiveWriter) addTree(root, prefix string, links bool, skip func(rel string, d fs.DirEntry) bool) (int, error) {
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skip != nil && skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if !links {
				return nil
			}
			target, err := os.Readlink(p)
			if err != nil {
				return nil
			}
			n++
			return a.addSymlink(path.Join(prefix, rel), target)
		case d.Type().IsRegular():
			n++
			return a.addFile(path.Join(prefix, rel), p)
		}
		return nil
	})
	return n, err
}

// archiveReader is src → age → zstd → tar.
func newArchiveReader(src io.Reader, passphrase string) (*tar.Reader, func(), error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, nil, err
	}
	ar, err := age.Decrypt(src, id)
	if err != nil {
		return nil, nil, ErrPassphrase
	}
	zr, err := zstd.NewReader(ar)
	if err != nil {
		return nil, nil, err
	}
	return tar.NewReader(zr), zr.Close, nil
}

// safeJoin is dir/name when name stays inside dir ("" otherwise): an entry
// never writes outside where it goes.
func safeJoin(dir, name string) string {
	name = path.Clean("/" + name)[1:]
	if name == "" || strings.HasPrefix(name, "../") {
		return ""
	}
	return filepath.Join(dir, filepath.FromSlash(name))
}

// extract writes entry h (its body in r) to dst, a path safeJoin gave.
// A symlink must point inside root.
func extract(h *tar.Header, r io.Reader, dst, root string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	switch h.Typeflag {
	case tar.TypeReg:
		mode := os.FileMode(h.Mode).Perm()
		if mode == 0 {
			mode = 0o600
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	case tar.TypeSymlink:
		target := h.Linkname
		resolved := target
		if !filepath.IsAbs(target) {
			resolved = filepath.Join(filepath.Dir(dst), target)
		}
		if rel, err := filepath.Rel(root, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil // a link out of the project: left out
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	}
	return nil
}
