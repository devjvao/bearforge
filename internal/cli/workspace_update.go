package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devjvao/bearforge/internal/app"
	"github.com/devjvao/bearforge/internal/domain"
)

func addWorkspaceUpdateCommand(workspace *cobra.Command, o *options) {
	var repositories []string
	command := &cobra.Command{
		Use:   "update WORKSPACE",
		Short: "Review and fast-forward local branches across a workspace",
		Long: "Review the current and default branches of workspace repositories. " +
			"Use --repo to select repositories or omit it to review all members. " +
			"One confirmation applies all eligible fast-forwards within that selection. " +
			"If remote history is missing, sync and preview again. " +
			"Rebasing is a separate per-repository action.",
		Example: "  bearforge workspace update feature --dry-run\n" +
			"  bearforge workspace update feature --repo api --repo web",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.perform(cmd.Context(), true, false, func(ctx context.Context, s *app.Service) (app.Result, error) {
				result := app.NewResult("workspace.update")
				plan, err := s.PreviewWorkspaceUpdate(ctx, args[0], repositories)
				if err != nil {
					return result, err
				}
				result.Data = plan
				result.Outcome = "planned"
				if o.dry {
					return result, nil
				}
				var selected []string
				blocked := 0
				for _, row := range plan.Rows {
					if row.Eligible {
						selected = append(selected, row.Repository)
					} else if row.Status == "blocked" || row.Status == "sync_required" {
						blocked++
					}
				}
				if len(selected) > 0 {
					if err := o.confirm(ctx, result); err != nil {
						return result, err
					}
					result, err = s.ApplyWorkspaceUpdate(ctx, plan, selected)
					result.Data = plan
				} else {
					result.Outcome = "unchanged"
				}
				if blocked > 0 {
					result.Outcome = "partial"
					err = errors.Join(err, domain.E("conflict", "repositories needing attention: %d; review their reported reasons before retrying", blocked))
				}
				return result, err
			})
		},
	}
	command.Flags().StringArrayVar(&repositories, "repo", nil, "Limit the update to this workspace repository ID (repeatable)")
	workspace.AddCommand(command)
}

// Use the same review for dry runs and confirmation. After apply, replace each
// selected row with its actual outcome and retain the reasons for skipped rows.
func writeWorkspaceUpdate(writer io.Writer, plan app.WorkspaceUpdatePlan, results []app.Item) error {
	items := make(map[string]app.Item, len(results))
	for _, item := range results {
		items[item.Repository] = item
	}
	var lines []string
	for _, row := range plan.Rows {
		status, detail := row.Status, row.Detail
		if row.Eligible {
			status = "selected"
		}
		advice := row.BaseAdvice
		if item, ok := items[row.Repository]; ok {
			status, detail = item.Outcome, ""
			if item.Problem != nil {
				detail = item.Problem.Message
				advice = ""
			}
		}
		header := fmt.Sprintf("%s  [%s]", repositoryListText(row.Repository), repositoryListText(status))
		if row.Branch != "" {
			header += "  " + repositoryListText(row.Branch)
		}
		lines = append(lines, header)
		for _, text := range []string{detail, advice} {
			if text != "" {
				lines = append(lines, "  "+repositoryListText(text))
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No workspace repositories to update.")
	}
	document := strings.Join(lines, "\n")
	if isTerminal(writer) {
		document = terminalDocument(document, outputWidth(writer))
	}
	_, err := fmt.Fprintln(writer, document)
	return err
}
