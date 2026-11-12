package cli

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
)

func addRepositoryLifecycleCommands(repo *cobra.Command, o *options) {
	repo.AddCommand(&cobra.Command{Use: "remove NAME", Aliases: []string{"retire"}, Short: "Retire an unreferenced bare repository intact into local trash", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return o.perform(cmd.Context(), true, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
			req := app.RetireRequest{Repository: args[0], DryRun: true}
			plan, err := s.RetireRepository(ctx, req)
			if err != nil || o.dry {
				return plan, err
			}
			if err := o.confirm(ctx, plan); err != nil {
				return plan, err
			}
			req.DryRun, req.ExpectedPlan, req.PlanOperation = false, plan.PlanDigest, plan.Data.(app.RepositoryPlan).Operation
			return o.operationProgress(ctx, s, func(ctx context.Context, s *app.Service) (app.Result, error) {
				return s.RetireRepository(ctx, req)
			})
		})
	}})
	var operation string
	restore := &cobra.Command{Use: "restore NAME --operation ID", Short: "Restore a retired repository without cloning or overwriting", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return o.perform(cmd.Context(), true, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
			req := app.RestoreRequest{Repository: args[0], Operation: operation, DryRun: true}
			plan, err := s.RestoreRepository(ctx, req)
			if err != nil || o.dry {
				return plan, err
			}
			if err := o.confirm(ctx, plan); err != nil {
				return plan, err
			}
			req.DryRun, req.ExpectedPlan, req.PlanOperation = false, plan.PlanDigest, plan.Data.(app.RepositoryPlan).Operation
			return o.operationProgress(ctx, s, func(ctx context.Context, s *app.Service) (app.Result, error) {
				return s.RestoreRepository(ctx, req)
			})
		})
	}}
	restore.Flags().StringVar(&operation, "operation", "", "Original retirement operation ID")
	repo.AddCommand(restore)
	repo.AddCommand(&cobra.Command{Use: "retired", Short: "List preserved retired repositories and their restoration IDs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.perform(cmd.Context(), false, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
			r := app.NewResult("repo.retired")
			c, _, err := s.Store.LoadCatalog()
			if err != nil {
				return r, err
			}
			ids := make([]string, 0, len(c.Retired))
			for id := range c.Retired {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			type entry struct {
				Repository string `json:"repository"`
				Operation  string `json:"operation"`
				Path       string `json:"path"`
			}
			entries := make([]entry, 0, len(ids))
			for _, id := range ids {
				retired := c.Retired[id]
				path, err := s.Store.SafePath(".bearforge", "trash", retired.Operation, id+".git")
				if err != nil {
					return r, err
				}
				r.Items = append(r.Items, app.Item{Repository: id, Path: path, Outcome: "retired"})
				entries = append(entries, entry{Repository: id, Operation: retired.Operation, Path: path})
			}
			r.Data = entries
			return r, nil
		})
	}})
}
