package cli

import (
	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/operation"
	"bearforge/internal/tui"
)

func addDashboardCommand(root *cobra.Command, o *options) {
	root.AddCommand(&cobra.Command{
		Use:   "tui [WORKSPACE]",
		Short: "Browse a read-only workspace dashboard",
		Long: "Browse a read-only workspace dashboard in a full-window terminal.\n" +
			"Refresh observes local Git state without fetching.\n\n" +
			"Tab changes panes; arrows or j/k navigate; / filters; Escape clears;\n" +
			"r refreshes; Page Up/Down scroll details; q quits; Ctrl-C cancels.",
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
			if err := tui.RunDashboard(cmd.Context(), o.in, o.out, service, workspace); err != nil {
				return &domain.Error{Code: "failed", Message: err.Error(), Cause: err}
			}
			return nil
		},
	})
}
