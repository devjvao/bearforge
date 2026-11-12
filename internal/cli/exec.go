package cli

import (
	"context"

	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/domain"
	"bearforge/internal/process"
)

func addExecCommand(root *cobra.Command, o *options) {
	var shell string
	var repositories []string
	var failFast, interactive bool
	command := &cobra.Command{
		Use:   "exec WORKSPACE [COMMAND] -- [ARGV...]",
		Short: "Run a command in verified workspace repositories",
		Long: "Run direct argv after --, or one quoted shell expression without --.\n" +
			"Defaults to one worker; --jobs opts into parallel execution.\n" +
			"Use --shell sh, pwsh, powershell or cmd for an explicit shell expression.\n" +
			"Windows .cmd/.bat wrappers require --shell cmd.\n" +
			"Child stdin is closed unless --interactive selects sequential plain terminal execution.\n" +
			"Captured stdout/stderr are capped at 64 KiB each per repository; JSON encodes bytes as base64.\n" +
			"Exec is user-authorized code, not a sandbox; operations resume never replays it.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.ui == "inline" {
				return domain.E("invalid", "bulk exec uses plain output; use --ui=plain or auto")
			}
			if interactive && !o.dry && (o.output != "human" || o.noInput || !isTerminal(o.in) || !isTerminal(o.out) || !isTerminal(o.errOut)) {
				return domain.E("invalid", "interactive exec requires terminal streams, human output and interactive input")
			}
			dash := cmd.ArgsLenAtDash()
			var argv []string
			if shell != "" || dash < 0 {
				if len(args) != 2 {
					return domain.E("invalid", "shell mode requires exactly one quoted command expression")
				}
				var err error
				argv, err = process.ShellArgv(shell, args[1])
				if err != nil {
					return err
				}
			} else {
				if dash != 1 {
					return domain.E("invalid", "put the executable and every argument after --")
				}
				argv = args[1:]
			}
			return o.perform(cmd.Context(), true, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
				return s.Exec(ctx, app.ExecRequest{Workspace: args[0], Repositories: repositories, Argv: argv, Jobs: o.jobs, FailFast: failFast, Interactive: interactive, DryRun: o.dry})
			})
		},
	}
	command.Flags().StringVar(&shell, "shell", "", "Shell: sh, pwsh, powershell, cmd")
	command.Flags().StringSliceVar(&repositories, "repo", nil, "Limit execution to named workspace repositories")
	command.Flags().BoolVar(&failFast, "fail-fast", false, "Stop launching new jobs after a failure; drain running jobs")
	command.Flags().BoolVar(&interactive, "interactive", false, "Share terminal stdin sequentially (one worker)")
	root.AddCommand(command)
}
