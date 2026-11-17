package operation

import (
	"maps"
	"testing"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func importSnapshot() domain.Repository {
	return domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://github.com/team/api.git"}, Provider: map[string]string{"kind": "github", "host": "github.com", "api_url": "https://api.github.com", "repository": "team/api", "remote_id": "123", "transport": "https", "connection": "work", "grant_id": "one"}}
}

func TestImportSnapshotJournalValidation(t *testing.T) {
	files, store := operationFixture(t)
	repo := importSnapshot()
	op := &app.Operation{Version: 1, ID: "import", Root: files.Root, Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", State: "started", ImportedRepository: &repo, RefFormat: "files"}}}
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(op.ID)
	if err != nil || loaded.Steps[0].ImportedRepository.Provider["grant_id"] != "one" {
		t.Fatalf("snapshot roundtrip: %+v %v", loaded, err)
	}
	for _, mutate := range []func(*app.Operation){
		func(op *app.Operation) { op.Steps = nil },
		func(op *app.Operation) { op.Steps = append(op.Steps, op.Steps[0]) },
		func(op *app.Operation) { op.Steps[0].ImportedRepository = nil },
		func(op *app.Operation) { op.Steps[0].ImportedRepository.ID = "other" },
		func(op *app.Operation) { delete(op.Steps[0].ImportedRepository.Provider, "grant_id") },
		func(op *app.Operation) {
			op.Steps[0].ImportedRepository.Remotes["origin"] = "https://secret@github.com/team/api.git"
		},
		func(op *app.Operation) { op.Steps[0].State = "invented" },
	} {
		repo := importSnapshot()
		bad := *op
		bad.Steps = []app.Step{{Repository: "api", State: "started", ImportedRepository: &repo}}
		mutate(&bad)
		if err := store.Save(&bad); err == nil {
			t.Fatalf("accepted malformed import snapshot: %+v", bad)
		}
	}
}

func TestImportReassociationCannotChangeImmutableScope(t *testing.T) {
	files, store := operationFixture(t)
	old := importSnapshot()
	replacement := old
	replacement.Provider = maps.Clone(old.Provider)
	replacement.Provider["grant_id"] = "two"
	op := &app.Operation{Version: 1, ID: "repair", Root: files.Root, Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", State: "succeeded", ImportedRepository: &replacement, PreviousImportedRepository: &old}}}
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	replacement.Provider["repository"] = "other/repository"
	if err := store.Save(op); err == nil {
		t.Fatal("reassociation changed immutable provider identity")
	}
}

func TestImportHelperHistoryRoundtripAndScopeValidation(t *testing.T) {
	files, store := operationFixture(t)
	current := importSnapshot()
	old := current
	old.Provider = maps.Clone(current.Provider)
	old.Provider["grant_id"] = "earlier"
	op := &app.Operation{Version: 1, ID: "history", Root: files.Root, Kind: "repo.import", State: "partial", Steps: []app.Step{{Repository: "api", State: "started", ImportedRepository: &current, ImportBindingHistory: []domain.Repository{old}}}}
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(op.ID)
	if err != nil || len(loaded.Steps[0].ImportBindingHistory) != 1 || loaded.Steps[0].ImportBindingHistory[0].Provider["grant_id"] != "earlier" {
		t.Fatalf("helper provenance lost: %+v %v", loaded, err)
	}
	old.Provider["repository"] = "outside/scope"
	if err := store.Save(op); err == nil {
		t.Fatal("accepted helper provenance outside immutable repository scope")
	}
	op.Kind = "repo.add"
	op.Steps[0].ImportedRepository = nil
	if err := store.Save(op); err == nil {
		t.Fatal("accepted import helper provenance in a different operation kind")
	}
}
