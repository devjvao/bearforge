package app

import (
	"context"
	"errors"
	"testing"

	"github.com/devjvao/bearforge/internal/domain"
)

type checkpointCancellationGit struct {
	Git
	clones int
}

func (g *checkpointCancellationGit) Clone(ctx context.Context, _, _ string) error {
	g.clones++
	return ctx.Err()
}

func TestImportWorkerStopsAfterStartedCheckpointCancellation(t *testing.T) {
	assertImportCheckpointCancellation(t, true)
}

func TestImportWorkerStopsAfterFormatCheckpointCancellation(t *testing.T) {
	assertImportCheckpointCancellation(t, false)
}

func assertImportCheckpointCancellation(t *testing.T, cancelAtStart bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	git := &checkpointCancellationGit{}
	service := &Service{Git: git}
	task := importTask{git: git, path: "api.git", step: Step{
		Repository: "api", State: "planned",
		ImportedRepository: &domain.Repository{ID: "api", Remotes: map[string]string{"origin": "https://github.com/team/api.git"}},
	}}
	result := service.runImportTask(ctx, task, func(_ Step, starting bool) error {
		if starting == cancelAtStart {
			cancel()
		}
		// Model an acknowledgement already buffered when cancellation arrives.
		return nil
	})
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("checkpoint cancellation returned %v", result.err)
	}
	if git.clones != 0 {
		t.Fatalf("started %d clones after checkpoint cancellation", git.clones)
	}
}
