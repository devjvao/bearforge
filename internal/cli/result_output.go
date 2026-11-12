package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"bearforge/internal/app"
)

func (o *options) renderResult(r app.Result) error {
	p := o.presentation(o.out, true)
	width := 80
	if output, ok := o.out.(*os.File); ok {
		if columns, _, err := term.GetSize(int(output.Fd())); err == nil && columns > 0 {
			width = columns
		}
	}
	title := repositoryListText(strings.ReplaceAll(r.Command, ".", " "))
	outcome := repositoryListText(r.Outcome)
	if p.Profile > colorprofile.ASCII {
		title = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(title)
		outcome = outcomeStyle(r.Outcome).Render(outcome)
	}
	if _, err := fmt.Fprintf(p, "%s  [%s]\n", title, outcome); err != nil {
		return err
	}
	items := slices.Clone(r.Items)
	slices.SortFunc(items, func(left, right app.Item) int {
		return strings.Compare(itemName(left), itemName(right))
	})
	if len(items) > 0 {
		headers := []string{"NAME", "STATUS"}
		branch, path, status, execution, details := false, false, false, false, false
		for _, item := range items {
			branch = branch || item.Branch != "" || item.ExpectedBranch != ""
			path = path || item.Path != ""
			status = status || item.Status != nil
			execution = execution || item.Execution != nil
			details = details || item.Base != "" || item.Start != "" || item.Tracking != "" || item.CreateBranch
		}
		if branch {
			headers = append(headers, "BRANCH")
		}
		if status {
			headers = append(headers, "CHANGES", "UPSTREAM")
		}
		if execution {
			headers = append(headers, "EXIT", "DURATION")
		}
		if details {
			headers = append(headers, "DETAILS")
		}
		if path {
			headers = append(headers, "PATH")
		}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			row := []string{itemName(item), item.Outcome}
			if branch {
				value := item.Branch
				if item.ExpectedBranch != "" && item.ExpectedBranch != item.Branch {
					value += " (expected " + item.ExpectedBranch + ")"
				}
				row = append(row, value)
			}
			if status {
				changes, upstream := "", ""
				if state := item.Status; state != nil {
					changes = fmt.Sprintf("staged=%d modified=%d untracked=%d conflicts=%d", state.Staged, state.Modified, state.Untracked, state.Conflicts)
					if state.Detached {
						changes += " detached"
					}
					if state.Unborn {
						changes += " unborn"
					}
					upstream = state.Upstream
					if state.Ahead != nil {
						upstream += fmt.Sprintf(" ahead=%d", *state.Ahead)
					}
					if state.Behind != nil {
						upstream += fmt.Sprintf(" behind=%d", *state.Behind)
					}
					if state.UpstreamGone {
						upstream += " gone"
					}
				}
				row = append(row, changes, strings.TrimSpace(upstream))
			}
			if execution {
				exit, duration := "", ""
				if item.Execution != nil {
					exit = strconv.Itoa(item.Execution.ExitCode)
					duration = item.Execution.Duration.String()
					if item.Execution.StdoutTruncated || item.Execution.StderrTruncated {
						duration += " (captured output truncated)"
					}
				}
				row = append(row, exit, duration)
			}
			if details {
				values := []string{}
				if item.Base != "" {
					values = append(values, "base="+item.Base)
				}
				if item.Start != "" {
					values = append(values, "start="+item.Start)
				}
				if item.Tracking != "" {
					values = append(values, "tracking="+item.Tracking)
				}
				if item.CreateBranch {
					values = append(values, "new branch")
				}
				row = append(row, strings.Join(values, ", "))
			}
			if path {
				row = append(row, item.Path)
			}
			rows = append(rows, row)
		}
		if _, err := fmt.Fprintf(p, "\n%s\n", renderResultRows(p, headers, rows, width)); err != nil {
			return err
		}
	} else if r.Data == nil && r.Problem == nil {
		message := "No entries."
		switch r.Command {
		case "workspace.list":
			message = "No workspaces registered.\nCreate one with: bearforge workspace create NAME"
		case "config.validate":
			message = "Configuration is valid."
		case "operations.abandon":
			if r.Outcome == "planned" {
				message = "Would stop recovery tracking; existing files and Git effects will be retained."
			} else {
				message = "Recovery tracking stopped; existing files and Git effects are retained."
			}
		default:
			if !strings.HasSuffix(r.Command, ".list") {
				message = "Operation " + r.Outcome + "."
			}
		}
		if _, err := fmt.Fprintln(p, message); err != nil {
			return err
		}
	}
	if r.Data != nil {
		if _, err := fmt.Fprintln(p); err != nil {
			return err
		}
		if err := renderData(r.Data, p, width); err != nil {
			return err
		}
	}
	if r.OperationID != "" {
		if _, err := fmt.Fprintln(p, "\nOperation:", repositoryListText(r.OperationID)); err != nil {
			return err
		}
	}
	if r.PlanDigest != "" {
		if _, err := fmt.Fprintln(p, "Plan:", repositoryListText(r.PlanDigest)); err != nil {
			return err
		}
	}
	var resultErr error
	for _, item := range items {
		if item.Problem != nil {
			resultErr = errors.Join(resultErr, o.diagnostic(itemName(item)+":", item.Problem.Message))
		}
	}
	if r.Problem != nil {
		resultErr = errors.Join(resultErr, o.diagnostic("Error:", r.Problem.Message))
	}
	if r.OperationID != "" && r.Outcome == "partial" {
		message := "Resume with: " + o.recoveryCommand("resume", r.OperationID)
		if r.Command == "exec" {
			message = "Inspect execution with: " + o.recoveryCommand("show", r.OperationID) + " (commands are never replayed)"
		}
		resultErr = errors.Join(resultErr, o.confirmationText("%s\n", message))
	}
	return resultErr
}

func renderResultRows(p *colorprofile.Writer, headers []string, rows [][]string, width int) string {
	if width <= 0 {
		width = 80
	}
	cleanHeaders := make([]string, len(headers))
	for index, header := range headers {
		cleanHeaders[index] = repositoryListText(header)
	}
	view := terminalTable(p, cleanHeaders, rows)
	if lipgloss.Width(view) <= width {
		return view
	}
	var records []string
	for _, row := range rows {
		var fields []string
		for column := 0; column < max(len(cleanHeaders), len(row)); column++ {
			label := "VALUE"
			if column < len(cleanHeaders) {
				label = cleanHeaders[column]
			}
			value := ""
			if column < len(row) {
				value = repositoryListText(row[column])
			}
			if p.Profile > colorprofile.ASCII {
				if label == "STATUS" || label == "STATE" {
					value = outcomeStyle(value).Render(value)
				}
				label = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(label)
			}
			fields = append(fields, ansi.Wrap(label+": "+value, width, ""))
		}
		records = append(records, strings.Join(fields, "\n"))
	}
	return strings.Join(records, "\n\n")
}

func itemName(item app.Item) string {
	name := item.Repository
	if item.Workspace != "" {
		if name == "" {
			name = item.Workspace
		} else {
			name = item.Workspace + " / " + name
		}
	}
	if item.Check != "" {
		if name == "" {
			name = item.Check
		} else {
			name += " / " + item.Check
		}
	}
	return name
}
