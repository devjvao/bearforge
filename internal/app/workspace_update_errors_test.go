package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

type fullHistoryGit struct{ Git }

func (fullHistoryGit) RequireFullHistory(context.Context, string) error { return nil }

func TestWorkspaceUpdateAdviceForNonportableRefs(t *testing.T) {
	service := Service{Git: fullHistoryGit{}}
	for _, name := range []string{"release\"candidate", "release\u200dcandidate", "release\u2019candidate"} {
		got := service.workspaceHistoryAdvice(t.Context(), "", missingUpdateHistory{target: updateTarget{Remote: "origin", Name: name}, relation: "pending"})
		if strings.Contains(got, "\ngit fetch") || !strings.Contains(got, strconv.QuoteToASCII("refs/heads/"+name)) || !strings.Contains(got, "your Git client") {
			t.Errorf("missing exact nonportable guidance for %q: %s", name, got)
		}
	}
}

func TestWorkspaceUpdateErrorPreservesCausesAndPriority(t *testing.T) {
	failed := errors.New("remote offline")
	refused := domain.E("conflict", "checkout changed")
	for _, errs := range [][]error{{failed, refused}, {refused, failed}, {failed, context.Canceled}, {context.Canceled, failed}} {
		got := WorkspaceUpdateError(errs...)
		want := "failed"
		if errors.Is(got, context.Canceled) {
			want = "cancelled"
		}
		if code := ProblemFor(got).Code; code != want || !errors.Is(got, errs[0]) || !errors.Is(got, errs[1]) {
			t.Errorf("aggregate %v: got %s, want %s with both original causes", errs, code, want)
		}
	}
	if WorkspaceUpdateError(refused) != refused {
		t.Error("single refusal lost its original identity")
	}
}
