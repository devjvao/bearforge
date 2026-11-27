package cli

import (
	"context"
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
				selected, blocked := workspaceUpdateScope(plan)
				if len(selected) > 0 {
					if err := o.confirm(ctx, result); err != nil {
						return result, err
					}
					result, err = s.ApplyWorkspaceUpdate(ctx, plan, selected)
					result.Data = plan
					if err != nil && len(result.Items) == 0 {
						return result, err // Lock or setup refusal before the batch started.
					}
				} else {
					result.Outcome = "unchanged"
				}
				if err = app.WorkspaceUpdateError(blocked, err); err != nil {
					result.Outcome = "partial"
				}
				return result, err
			})
		},
	}
	command.Flags().StringArrayVar(&repositories, "repo", nil, "Limit the update to this workspace repository ID (repeatable)")
	workspace.AddCommand(command)
}

func workspaceUpdateScope(plan app.WorkspaceUpdatePlan) ([]string, error) {
	var selected []string
	var blocked []error
	for _, row := range plan.Rows {
		if row.Eligible {
			selected = append(selected, row.Repository)
			continue
		}
		if row.Status != "blocked" && row.Status != "sync_required" {
			continue
		}
		code, message := "conflict", row.Detail
		if row.Problem != nil {
			code, message = row.Problem.Code, row.Problem.Message
		}
		blocked = append(blocked, domain.E(code, "%s: %s", row.Repository, message))
	}
	return selected, app.WorkspaceUpdateError(blocked...)
}

// Use the same review for dry runs and confirmation. After apply, replace each
// selected row with its actual outcome and retain the reasons for skipped rows.
func writeWorkspaceUpdate(writer io.Writer, plan app.WorkspaceUpdatePlan, results []app.Item, reviewing bool) error {
	items := make(map[string]app.Item, len(results))
	for _, item := range results {
		items[item.Repository] = item
	}
	var lines []string
	for _, row := range plan.Rows {
		status, detail := row.Status, row.Detail
		advice := row.BaseAdvice
		branch := row.Branch
		if item, ok := items[row.Repository]; ok {
			status, detail = item.Outcome, ""
			var observations []string
			for _, move := range item.AppliedMoves {
				observations = append(observations, fmt.Sprintf("%s: %s → %s", move.Branch, move.From, move.To))
			}
			if item.Problem != nil {
				observations = append(observations, item.Problem.Message)
				advice = ""
			} else if item.Outcome == "updated" {
				branch = ""
			}
			detail = strings.Join(observations, "\n")
		} else if row.Eligible {
			if reviewing {
				status = "selected"
			} else {
				status, detail, advice = "not run", "", ""
			}
		}
		header := fmt.Sprintf("%s  [%s]", repositoryListText(row.Repository), repositoryListText(status))
		if branch != "" {
			header += "  " + repositoryListText(branch)
		}
		lines = append(lines, header)
		for _, text := range []string{detail, advice} {
			if text != "" {
				for line := range strings.SplitSeq(text, "\n") {
					lines = append(lines, "  "+repositoryListText(line))
				}
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No workspace repositories to update.")
	}
	// The terminal soft-wraps these lines. Inserting hard line breaks would
	// split a fetch instruction, commit ID, or exact ref when copied.
	_, err := fmt.Fprintln(writer, strings.Join(lines, "\n"))
	return err
}
