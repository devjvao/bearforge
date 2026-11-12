package cli

import (
	"context"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
)

func addPullRequestCommands(root *cobra.Command, o *options) {
	pr := &cobra.Command{Use: "pr", Short: "Look up pull requests without changing repositories"}
	var req app.PRRequest
	list := &cobra.Command{Use: "list WORKSPACE", Short: "List pull requests for actual workspace branches", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req.Workspace, req.DryRun = args[0], o.dry
		return o.perform(cmd.Context(), true, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
			return s.PullRequests(ctx, req)
		})
	}}
	list.Flags().StringArrayVar(&req.Repositories, "repo", nil, "Select a workspace member (repeatable)")
	list.Flags().StringVar(&req.Base, "base", "", "Override the mapped pull-request target branch")
	list.Flags().StringVar(&req.State, "state", "open", "Pull-request state: open, closed, all")
	list.Flags().BoolVar(&req.Refresh, "refresh", false, "Bypass cached pull-request observations")
	pr.AddCommand(list)
	root.AddCommand(pr)
}
