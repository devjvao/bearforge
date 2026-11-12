package cli

import (
	"context"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/gitcli"
)

func addDoctorCommand(root *cobra.Command, o *options) {
	root.AddCommand(&cobra.Command{Use: "doctor [WORKSPACE]", Short: "Check local configuration, repositories and recovery without repairs", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		workspace := ""
		if len(args) == 1 {
			workspace = args[0]
		}
		return o.perform(cmd.Context(), false, true, func(ctx context.Context, s *app.Service) (app.Result, error) {
			git, err := gitcli.New()
			if err == nil {
				s.Git = git
			}
			return s.Doctor(ctx, workspace)
		})
	}})
}
