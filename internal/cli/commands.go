package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func addCommands(root *cobra.Command, o *options) {
	addDashboardCommand(root, o)
	addExecCommand(root, o)
	addDoctorCommand(root, o)
	repo := &cobra.Command{Use: "repo", Short: "Manage bare repositories"}
	workspace := &cobra.Command{Use: "workspace", Short: "Manage workspaces"}
	ops := &cobra.Command{Use: "operations", Short: "Inspect and recover interrupted operations"}
	root.AddCommand(repo, workspace, ops)
	addRepositoryLifecycleCommands(repo, o)
	command := func(parent *cobra.Command, use, short string, args cobra.PositionalArgs, git, progress bool, fn func(context.Context, *app.Service, []string) (app.Result, error)) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			return o.perform(cmd.Context(), git, progress, func(ctx context.Context, s *app.Service) (app.Result, error) { return fn(ctx, s, args) })
		}}
		parent.AddCommand(c)
		return c
	}
	command(repo, "add NAME URL", "Register and clone a bare repository", cobra.ExactArgs(2), true, true, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		return s.AddRepository(ctx, a[0], a[1], o.dry)
	})
	command(repo, "list", "List registered repositories", cobra.NoArgs, false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("repo.list")
		c, _, err := s.Store.LoadCatalog()
		if err != nil {
			return r, err
		}
		for id := range c.Repositories {
			r.Items = append(r.Items, app.Item{Repository: id, Outcome: "registered"})
		}
		return r, nil
	})
	command(repo, "inspect NAME", "Inspect a repository and its worktrees", cobra.ExactArgs(1), true, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("repo.inspect")
		c, _, err := s.Store.LoadCatalog()
		if err != nil {
			return r, err
		}
		if _, ok := c.Repositories[a[0]]; !ok {
			return r, domain.E("not_found", "repository not found")
		}
		p, err := s.Store.SafePath("repos", a[0]+".git")
		if err != nil {
			return r, err
		}
		trees, err := s.Git.Worktrees(ctx, p)
		r.Data = trees
		return r, err
	})
	syncFn := func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		return s.Sync(ctx, a, o.jobs, o.dry)
	}
	command(root, "sync [REPO...]", "Fetch remote updates without changing checkouts", cobra.ArbitraryArgs, true, true, syncFn)
	command(repo, "sync [REPO...]", "Fetch registered repositories", cobra.ArbitraryArgs, true, true, syncFn)
	for _, name := range []string{"create", "init"} {
		var branch string
		c := command(workspace, name+" NAME", "Create an empty workspace", cobra.ExactArgs(1), true, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
			return s.CreateWorkspace(ctx, a[0], branch, o.dry)
		})
		c.Flags().StringVar(&branch, "branch", "", "Default branch name")
	}
	var branch, base, track string
	var overrides map[string]string
	add := command(workspace, "add WORKSPACE REPO...", "Materialize repositories as worktrees", cobra.MinimumNArgs(2), true, true, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		return s.AddRepositories(ctx, app.AddRequest{Workspace: a[0], Repositories: a[1:], Branch: branch, Base: base, Tracking: track, BranchMap: overrides, DryRun: o.dry})
	})
	add.Flags().StringVar(&branch, "branch", "", "Branch for selected repositories")
	add.Flags().StringVar(&base, "base", "", "Starting ref for new branches")
	add.Flags().StringVar(&track, "track", "", "Tracking: none, same-name, preserve")
	add.Flags().StringToStringVar(&overrides, "branch-map", nil, "Repository branch overrides (repo=branch)")
	command(workspace, "list", "List workspace definitions", cobra.NoArgs, false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("workspace.list")
		names, err := s.Store.ListWorkspaces()
		for _, name := range names {
			r.Items = append(r.Items, app.Item{Workspace: name, Outcome: "registered"})
		}
		return r, err
	})
	for _, name := range []string{"show", "ls"} {
		command(workspace, name+" NAME", "Show live workspace contents", cobra.ExactArgs(1), true, true, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) { return s.Status(ctx, a[0]) })
	}
	var check bool
	status := command(root, "status WORKSPACE", "Report live worktree health", cobra.ExactArgs(1), true, true, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r, err := s.Status(ctx, a[0])
		if err == nil && check {
			for _, item := range r.Items {
				if item.Outcome != "clean" || item.Status.Detached || item.Status.UpstreamGone {
					err = domain.E("unhealthy", "workspace does not meet health checks")
					break
				}
			}
		}
		return r, err
	})
	status.Flags().BoolVar(&check, "check", false, "Fail when worktrees have changes or drift")
	workspace.AddCommand(&cobra.Command{Use: "path NAME", Short: "Print only the absolute workspace path", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		store, err := o.store()
		if err != nil {
			return err
		}
		if _, _, err := store.LoadWorkspace(a[0]); err != nil {
			return err
		}
		p, err := store.SafePath(a[0])
		if err != nil {
			return err
		}
		if o.output != "human" {
			return o.perform(cmd.Context(), false, false, func(context.Context, *app.Service) (app.Result, error) {
				r := app.NewResult("workspace.path")
				r.Data = p
				return r, nil
			})
		}
		_, err = fmt.Fprintln(o.out, p)
		return err
	}})
	for _, whole := range []bool{false, true} {
		use := "remove-repo WORKSPACE REPO..."
		args := cobra.MinimumNArgs(2)
		if whole {
			use = "remove WORKSPACE"
			args = cobra.ExactArgs(1)
		}
		command(workspace, use, "Safely remove clean worktrees; preserve branches", args, true, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
			req := app.RemoveRequest{Workspace: a[0], Repositories: a[1:], Whole: whole, DryRun: true}
			plan, err := s.RemoveRepositories(ctx, req)
			if err != nil || o.dry {
				return plan, err
			}
			if err := o.confirm(ctx, plan); err != nil {
				return plan, err
			}
			req.DryRun = false
			req.ExpectedPlan = plan.PlanDigest
			return o.operationProgress(ctx, s, func(ctx context.Context, s *app.Service) (app.Result, error) {
				return s.RemoveRepositories(ctx, req)
			})
		})
	}
	command(ops, "list", "List recorded operations", cobra.NoArgs, false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("operations.list")
		data, err := s.Journal.List()
		r.Data = data
		return r, err
	})
	command(ops, "show ID", "Inspect an operation", cobra.ExactArgs(1), false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("operations.show")
		data, err := s.Journal.Load(a[0])
		r.Data = data
		return r, err
	})
	command(ops, "resume ID", "Resume an interrupted mutation", cobra.ExactArgs(1), true, true, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		if o.dry {
			r := app.NewResult("operations.resume")
			op, err := s.Journal.Load(a[0])
			r.Data = op
			r.Outcome = "planned"
			return r, err
		}
		return s.Resume(ctx, a[0])
	})
	command(ops, "abandon ID", "Stop recovery tracking without deleting any files", cobra.ExactArgs(1), false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("operations.abandon")
		if o.dry {
			r.Outcome = "planned"
			return r, nil
		}
		if !o.yes {
			return r, domain.E("confirmation", "use --yes to abandon; existing files and Git effects are retained")
		}
		return s.Abandon(ctx, a[0])
	})
	cfg := &cobra.Command{Use: "config", Short: "Validate configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}}
	root.AddCommand(cfg)
	command(cfg, "validate", "Validate catalog and workspace definitions", cobra.NoArgs, false, false, func(ctx context.Context, s *app.Service, a []string) (app.Result, error) {
		r := app.NewResult("config.validate")
		catalog, _, err := s.Store.LoadCatalog()
		if err != nil {
			return r, err
		}
		names, err := s.Store.ListWorkspaces()
		if err != nil {
			return r, err
		}
		for _, name := range names {
			w, _, err := s.Store.LoadWorkspace(name)
			if err != nil {
				return r, err
			}
			for id := range w.Repositories {
				if _, ok := catalog.Repositories[id]; !ok {
					return r, domain.E("invalid", "workspace %s refers to unknown repository %s", name, id)
				}
			}
		}
		r.Outcome = "valid"
		return r, nil
	})
	root.AddCommand(&cobra.Command{Use: "completion SHELL", Short: "Generate shell completion", ValidArgs: []string{"bash", "zsh", "fish", "powershell"}, Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs), RunE: func(cmd *cobra.Command, a []string) error {
		switch a[0] {
		case "bash":
			return root.GenBashCompletionV2(o.out, true)
		case "zsh":
			return root.GenZshCompletion(o.out)
		case "fish":
			return root.GenFishCompletion(o.out, true)
		default:
			return root.GenPowerShellCompletionWithDesc(o.out)
		}
	}})
}
