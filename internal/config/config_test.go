package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/config"
)

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "examples", "office.config.example.json"))
	if err != nil {
		t.Fatalf("example config: %v", err)
	}
	if cfg.Project.Name != "demo-shop" || cfg.Storage.Driver != "sqlite" || cfg.Server.APIAddr != "127.0.0.1:8787" {
		t.Fatalf("decoded = %+v", cfg)
	}
	if !filepath.IsAbs(cfg.Storage.Path) && !strings.Contains(cfg.Storage.Path, "examples") {
		t.Fatalf("storage path not resolved relative to config: %q", cfg.Storage.Path)
	}
}

func TestInvalidConfigListsProblems(t *testing.T) {
	p := filepath.Join(t.TempDir(), "office.config.json")
	os.WriteFile(p, []byte(`{"version": 1, "project": {"name": "x"}, "storage": {"driver": "mysql"}}`), 0o600)
	_, err := config.Load(p)
	var ve *config.ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) < 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingFile(t *testing.T) {
	if _, err := config.Load(filepath.Join(t.TempDir(), "nope.json")); !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
