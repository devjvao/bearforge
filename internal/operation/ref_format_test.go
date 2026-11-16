package operation

import (
	"testing"

	"bearforge/internal/app"
)

func TestReferenceFormatJournalCompatibility(t *testing.T) {
	files, store := operationFixture(t)
	for _, format := range []string{"", "files", "reftable"} {
		op := &app.Operation{Version: 1, ID: "format", Root: files.Root, Kind: "repo.add", State: "partial", Steps: []app.Step{{Repository: "api", State: "started", RefFormat: format}}}
		if err := store.Save(op); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load(op.ID)
		if err != nil || loaded.Steps[0].RefFormat != format {
			t.Fatalf("format %q did not roundtrip: %v", format, err)
		}
	}
	for _, step := range []app.Step{{Repository: "api", RefFormat: "auto"}, {Repository: "api", RefFormat: "unknown"}, {Repository: "api", RequestedRefFormat: "unknown"}} {
		op := &app.Operation{Version: 1, ID: "bad-format", Root: files.Root, Kind: "repo.add", State: "partial", Steps: []app.Step{step}}
		if err := store.Save(op); err == nil {
			t.Fatalf("invalid format accepted: %+v", step)
		}
	}
}
