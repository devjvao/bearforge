package cli

import (
	"context"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func addRepositoryRebindCommand(repo *cobra.Command, o *options) {
	var name string
	command := &cobra.Command{
		Use:   "rebind REPOSITORY... --connection NAME",
		Short: "Review and replace saved authentication for registered repositories",
		Long:  "Review and replace the saved connection and grant for selected registered repositories. Updates scoped HTTPS Git credential routing while preserving clones, refs, and worktrees. Use --dry-run to inspect the changes first.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, ids []string) error {
			if name == "" {
				return domain.E("invalid", "select a replacement with --connection NAME")
			}
			return o.perform(cmd.Context(), true, false, func(ctx context.Context, service *app.Service) (app.Result, error) {
				manager, err := o.authentication()
				if err != nil {
					return app.NewResult("repo.rebind"), err
				}
				connection, err := manager.Connection(ctx, name)
				if err != nil {
					return app.NewResult("repo.rebind"), err
				}
				request := app.RepositoryRebindRequest{Repositories: ids, Connection: connection.Name, GrantID: connection.GrantID, DryRun: true}
				plan, err := service.RebindRepositories(ctx, request)
				plan = annotateReassociationAccount(plan, connection)
				if err != nil || o.dry {
					return plan, err
				}
				if err := o.confirm(ctx, plan); err != nil {
					return plan, err
				}
				request.DryRun, request.ExpectedPlan = false, plan.PlanDigest
				result, err := o.operationProgress(ctx, service, func(ctx context.Context, service *app.Service) (app.Result, error) {
					return service.RebindRepositories(ctx, request)
				})
				return annotateReassociationAccount(result, connection), err
			})
		},
	}
	command.Flags().StringVar(&name, "connection", "", "Replacement saved connection for every selected repository")
	_ = command.MarkFlagRequired("connection")
	repo.AddCommand(command)
}
