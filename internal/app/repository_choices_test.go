package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRepositoryChoicesReadCatalogMembershipWithoutWrites(t *testing.T) {
	s, store, remote := fixture(t)
	_, _, otherRemote := fixture(t)
	ctx := t.Context()
	if _, err := s.AddRepository(ctx, "web", otherRemote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepository(ctx, "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkspace(ctx, "feature", "work/feature", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRepositories(ctx, addRequest("feature", "api")); err != nil {
		t.Fatal(err)
	}
	before := dashboardFiles(t, store.Root)
	choices, err := s.RepositoryChoices(ctx, "feature")
	if err != nil || choices.Workspace != "feature" || choices.DefaultBranch != "work/feature" || len(choices.Items) != 2 {
		t.Fatalf("catalog choices: %+v, %v", choices, err)
	}
	if choices.Items[0].Repository != "api" || !choices.Items[0].InWorkspace || choices.Items[1].Repository != "web" || choices.Items[1].InWorkspace {
		t.Fatalf("incorrect membership/ordering: %+v", choices.Items)
	}
	if !reflect.DeepEqual(before, dashboardFiles(t, store.Root)) {
		t.Fatal("picker query changed files, refs, index or journals")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RepositoryChoices(cancelled, "feature"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query: %v", err)
	}
}
