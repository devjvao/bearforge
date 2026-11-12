package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"bearforge/internal/app"
	"bearforge/internal/domain"
)

func (o *options) renderPullRequests(r app.Result, terminal bool) error {
	p := o.presentation(o.out, terminal)
	width := outputWidth(o.out)
	if len(r.Items) == 0 {
		if _, err := fmt.Fprintln(p, terminalDocument("No workspace repositories.", width)); err != nil {
			return err
		}
	}
	headers := []string{"REPOSITORY", "STATUS", "BRANCH", "EXPECTED BRANCH", "BASE", "PR", "TITLE", "STATE", "DRAFT", "SOURCE", "TARGET", "UPDATED AT", "URL", "OBSERVED AT", "CACHED", "ERROR"}
	var rows [][]string
	for _, item := range r.Items {
		base := []string{item.Repository, item.Outcome, item.Branch, item.ExpectedBranch, item.Base, "", "", "", "", "", "", "", "", "", "", ""}
		if item.Problem != nil {
			base[15] = item.Problem.Code + ": " + item.Problem.Message
		}
		observation := item.PullRequests
		if observation != nil {
			q := observation.Query
			base[9] = q.SourceRepository + ":" + q.Branch
			base[10] = q.Repository + ":" + q.Base
			base[13] = "not observed"
			if !observation.ObservedAt.IsZero() {
				base[13] = observation.ObservedAt.Format(time.RFC3339Nano)
			}
			base[14] = strconv.FormatBool(observation.Cached)
		}
		if observation == nil || len(observation.PullRequests) == 0 {
			rows = append(rows, base)
			continue
		}
		for _, pr := range observation.PullRequests {
			row := append([]string(nil), base...)
			fillPullRequestRow(row, pr)
			rows = append(rows, row)
		}
	}
	if len(rows) > 0 {
		if terminal {
			if _, err := fmt.Fprintln(p, renderResultRows(p, headers, rows, width)); err != nil {
				return err
			}
		} else {
			for _, row := range rows {
				for index, value := range row {
					row[index] = repositoryListText(value)
				}
				if _, err := fmt.Fprintln(p, strings.Join(row, "\t")); err != nil {
					return err
				}
			}
		}
	}
	if r.Problem != nil {
		return o.diagnostic("Error:", r.Problem.Message)
	}
	return nil
}

func fillPullRequestRow(row []string, pr domain.PullRequest) {
	row[5], row[6], row[7], row[8] = strconv.Itoa(pr.Number), pr.Title, pr.State, strconv.FormatBool(pr.Draft)
	row[9], row[10] = pr.SourceRepository+":"+pr.SourceBranch, pr.TargetRepository+":"+pr.TargetBranch
	row[11], row[12] = pr.UpdatedAt.Format(time.RFC3339Nano), pr.URL
}
