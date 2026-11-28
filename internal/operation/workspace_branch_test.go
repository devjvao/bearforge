package operation

import (
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/app"
)

func TestWorkspaceBranchJournalCompatibility(t *testing.T) {
	files, store := operationFixture(t)
	oid := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	step := app.Step{Repository: "api", Branch: "topic", Start: oid, Create: true}
	op := &app.Operation{Version: 1, ID: "review", Root: files.Root, Kind: "workspace.add", State: "partial", Steps: []app.Step{step}}
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	old, err := store.Load(op.ID)
	if err != nil || old.Steps[0].RemoteBranch != nil {
		t.Fatalf("legacy step: %+v, %v", old, err)
	}
	pin := &app.RemoteBranchPlan{Remote: "origin", URL: "https://example.test/team/api.git", OID: oid}
	op.Steps[0].RemoteBranch = pin
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	pin.OID = other
	if err := store.Save(op); err == nil {
		t.Fatal("creation journal accepted a starting commit other than the reviewed remote tip")
	}
	pin.OID = oid
	op.Steps[0].Create = false
	pin.LocalOID = "topic"
	if err := store.Save(op); err == nil {
		t.Fatal("fast-forward journal accepted mutable branch as old tip")
	}
	op.Steps[0].RemoteBranch = nil
	op.Steps[0].Tracking = "same-name"
	op.Steps[0].TrackingRemote = "origin"
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}
	op.Steps[0].TrackingRemote = "origin/topic"
	if err := store.Save(op); err == nil {
		t.Fatal("offline journal accepted an invalid tracking remote")
	}
}
