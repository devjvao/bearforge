package operation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/config"
)

func journalWithRecordedRoot(t *testing.T, current, recorded string) *Store {
	t.Helper()
	files := config.New(current)
	op := app.Operation{Version: 1, ID: "task", Kind: "workspace.create", State: "partial", Root: recorded}
	doc, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Write([]string{".bearforge", "state", "operations", "task.json"}, doc); err != nil {
		t.Fatal(err)
	}
	return New(files)
}

func TestJournalRootWithDifferentCase(t *testing.T) {
	base, _ := operationFixture(t)
	current := filepath.Join(base.Root, "Managed")
	recorded := filepath.Join(base.Root, "managed")
	store := journalWithRecordedRoot(t, current, recorded)
	err := os.Mkdir(recorded, 0700)
	if err == nil {
		// This filesystem permits two distinct directories with these spellings.
		if _, err := store.Load("task"); err == nil || !strings.Contains(err.Error(), "schema/root mismatch") {
			t.Fatalf("journal from another directory accepted: %v", err)
		}
		return
	}
	if !os.IsExist(err) {
		t.Fatal(err)
	}
	op, err := store.Load("task")
	if err != nil {
		t.Fatal(err)
	}
	if op.Root != current {
		t.Fatalf("loaded root = %q, want current spelling %q", op.Root, current)
	}
	pending, err := store.PendingSummaries(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != op.ID {
		t.Fatalf("pending operations = %+v, %v", pending, err)
	}
	if err := store.Save(op); err != nil {
		t.Fatalf("save validated operation: %v", err)
	}
}

func TestJournalRootRejectsCaseVariantLinkedAncestor(t *testing.T) {
	base, _ := operationFixture(t)
	actual := filepath.Join(base.Root, "Managed")
	alias := filepath.Join(base.Root, "managed")
	store := journalWithRecordedRoot(t, filepath.Join(actual, "root"), filepath.Join(alias, "root"))
	if _, err := os.Lstat(alias); err == nil {
		t.Skip("filesystem resolves the alternate spelling to the existing directory")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Load("task"); err == nil || !strings.Contains(err.Error(), "schema/root mismatch") {
		t.Fatalf("journal with a linked ancestor accepted: %v", err)
	}
}
