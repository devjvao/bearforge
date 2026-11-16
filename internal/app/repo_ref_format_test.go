package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"bearforge/internal/app"
)

type planningGit struct {
	app.Git
	journal         app.Journal
	planned, cloned bool
}

func (g *planningGit) PlanClone(ctx context.Context, remote, path, requested string) (string, error) {
	ops, err := g.journal.List()
	if err != nil || len(ops) != 1 {
		return "", fmt.Errorf("planning must be journal-owned: %v", err)
	}
	g.planned = true
	return "files", nil
}

func (g *planningGit) CloneWithFormat(ctx context.Context, remote, path, format string) error {
	ops, err := g.journal.List()
	if err != nil || len(ops) != 1 {
		return fmt.Errorf("missing clone journal: %v", err)
	}
	b, _ := json.Marshal(ops[0].Steps[0])
	var step map[string]any
	_ = json.Unmarshal(b, &step)
	if format != "files" || step["ref_format"] != format {
		return fmt.Errorf("format was not durably selected before clone: %s", b)
	}
	g.cloned = true
	return g.Git.Clone(ctx, remote, path)
}

func (g *planningGit) InitializeWithFormat(ctx context.Context, path, format string) error {
	return g.Git.Initialize(ctx, path)
}

func TestRepositoryCloneFormatPersistedBeforeGit(t *testing.T) {
	s, _, remote := fixture(t)
	g := &planningGit{Git: s.Git, journal: s.Journal}
	s.Git = g
	if _, err := s.AddRepository(t.Context(), "api", remote, false); err != nil {
		t.Fatal(err)
	}
	if !g.planned || !g.cloned {
		t.Fatal("optional clone planner was bypassed")
	}
}

func TestRepositoryPreviewDoesNotPlanOrProbe(t *testing.T) {
	s, _, remote := fixture(t)
	g := &planningGit{Git: s.Git, journal: s.Journal}
	s.Git = g
	if _, err := s.AddRepository(t.Context(), "api", remote, true); err != nil {
		t.Fatal(err)
	}
	if g.planned || g.cloned {
		t.Fatal("preview invoked execution-only planner")
	}
	ops, err := s.Journal.List()
	if err != nil || len(ops) != 0 {
		t.Fatalf("preview wrote journal: %v %v", ops, err)
	}
}

func TestRepositoryRefFormatValidationHasNoEffects(t *testing.T) {
	s, _, remote := fixture(t)
	g := &planningGit{Git: s.Git, journal: s.Journal}
	s.Git = g
	for _, dry := range []bool{true, false} {
		if _, err := s.AddRepositoryWithFormat(t.Context(), "api", remote, "unknown", dry); err == nil {
			t.Fatal("invalid ref format accepted")
		}
	}
	ops, err := s.Journal.List()
	if err != nil || len(ops) != 0 || g.planned || g.cloned {
		t.Fatalf("invalid format caused effects: %v %v", ops, err)
	}
}
