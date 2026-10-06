package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/storage/storagetest"
)

func TestContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Store {
		s, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		// Migrate must be idempotent.
		if err := s.Migrate(context.Background()); err != nil {
			t.Fatalf("second Migrate: %v", err)
		}
		return s
	})
}
