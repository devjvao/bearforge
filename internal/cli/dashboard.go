package cli

import (
	"context"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/github"
	"bearforge/internal/operation"
	"bearforge/internal/tui"
)

// dashboardService keeps each sync observer local to that operation. Dashboard
// reads and the original application service never share a mutable callback.
type dashboardService struct {
	*app.Service
	jobs int
}

func newDashboardService(service *app.Service, o *options) dashboardService {
	configured := *service
	configured.PRs = o.pullRequestProvider
	if configured.PRs == nil {
		configured.PRs = github.NewClient()
	}
	return dashboardService{Service: &configured, jobs: o.jobs}
}

func (s dashboardService) Sync(ctx context.Context, names []string, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.Sync(ctx, names, s.jobs, false)
}

func (s dashboardService) AddRepositories(ctx context.Context, req app.AddRequest, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.AddRepositories(ctx, req)
}

func (s dashboardService) CreateWorkspace(ctx context.Context, req app.CreateWorkspaceRequest, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.CreateWorkspaceWithPlan(ctx, req)
}

func (s dashboardService) RemoveRepositories(ctx context.Context, req app.RemoveRequest, observe func(app.Event)) (app.Result, error) {
	service := *s.Service
	service.Observe = observe
	return service.RemoveRepositories(ctx, req)
}

func addDashboardCommand(root *cobra.Command, o *options) {
	root.AddCommand(&cobra.Command{
		Use:   "tui [WORKSPACE]",
		Short: "Browse workspace health and manage workspace repositories",
		Long: "Browse workspace health in a full-window terminal.\n" +
			"Refresh observes local Git state without fetching.\n" +
			"p looks up open GitHub PRs for every loaded workspace member; P bypasses the memory cache.\n" +
			"Startup, navigation and r never contact GitHub. PR snapshots appear in repository details.\n" +
			"s fetches/prunes all repositories in the current workspace, including filtered rows.\n" +
			"Sync uses --jobs and shows progress and per-repository results without changing checkouts.\n\n" +
			"a opens the registered-repository picker for the current workspace.\n" +
			"Space selects, Enter previews resolved defaults, and y confirms the reviewed add plan.\n\n" +
			"n creates an empty workspace; d removes the selected repository; D removes the workspace.\n" +
			"Each action previews its destinations and requires y to confirm; unsafe worktrees are refused.\n\n" +
			"Tab changes panes; arrows or j/k navigate; / filters; Escape clears;\n" +
			"r refreshes locally; p/P look up/refresh PRs; s syncs; a adds; n creates; d/D removes; v toggles results; Page Up/Down scroll details; q quits; Ctrl-C cancels.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !o.interactive() || o.ui != "auto" || o.dry {
				return domain.E("invalid", "tui requires terminal stdin/stdout/stderr, human output, --ui=auto, and interactive input without --quiet or --dry-run; use status WORKSPACE for plain/JSON output")
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			store, err := o.store()
			if err != nil {
				return err
			}
			workspace := ""
			if len(args) == 1 {
				workspace = args[0]
				// Validate the requested definition before taking over the terminal.
				if _, _, err := store.LoadWorkspace(workspace); err != nil {
					return err
				}
			}
			git, err := gitcli.New()
			if err != nil {
				return err
			}
			service := app.New(store, git, operation.New(store))
			if err := tui.RunDashboard(cmd.Context(), o.in, o.out, newDashboardService(service, o), workspace, tui.DashboardOptions{Color: o.color}); err != nil {
				return &domain.Error{Code: "failed", Message: err.Error(), Cause: err}
			}
			return nil
		},
	})
}
