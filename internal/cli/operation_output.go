package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"bearforge/internal/app"
)

func recoveryOutputHeading(p *colorprofile.Writer, title string) string {
	title = repositoryListText(title)
	if p.Profile > colorprofile.ASCII {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render(title)
	}
	return title
}

func (o *options) renderOperationResult(result app.Result) error {
	p := o.presentation(o.out, isTerminal(o.out))
	view := operationResultDocument(p, result, outputWidth(o.out))
	if _, err := fmt.Fprintln(p, view); err != nil {
		return err
	}
	if result.Problem != nil {
		return o.diagnostic("Error:", result.Problem.Message)
	}
	return nil
}

func operationResultDocument(p *colorprofile.Writer, result app.Result, width int) string {
	if width <= 0 {
		width = 80
	}
	var sections []string
	switch data := result.Data.(type) {
	case []app.Operation:
		sections = append(sections, recoveryOutputHeading(p, "Operations"))
		if len(data) == 0 {
			sections = append(sections, "No recorded operations.")
			break
		}
		operations := slices.Clone(data)
		slices.SortFunc(operations, func(left, right app.Operation) int { return strings.Compare(left.ID, right.ID) })
		rows := make([][]string, 0, len(operations))
		unfinished := 0
		for _, operation := range operations {
			if operation.State != "complete" && operation.State != "abandoned" {
				unfinished++
			}
			workspace := operation.Workspace
			if workspace == "" {
				workspace = "-"
			}
			rows = append(rows, []string{operation.ID, operation.Kind, workspace, operation.State, strconv.Itoa(len(operation.Steps))})
		}
		sections = append(sections, fmt.Sprintf("%d recorded | %d unfinished", len(operations), unfinished), renderResultRows(p, []string{"ID", "COMMAND", "WORKSPACE", "STATE", "STEPS"}, rows, width))
		root := repositoryListText(operations[0].Root)
		inspect := "bearforge operations show ID"
		if root != "" {
			inspect = (&options{activeRoot: root}).recoveryCommand("show", "ID")
		}
		sections = append(sections, "Inspect: "+inspect)
	case *app.Operation:
		if data == nil {
			sections = append(sections, "No operation details available.")
		} else {
			sections = append(sections, operationDetailDocument(p, *data, result.Outcome == "planned", width))
		}
	case app.Operation:
		sections = append(sections, operationDetailDocument(p, data, result.Outcome == "planned", width))
	}
	return terminalDocument(strings.Join(sections, "\n\n"), width)
}

func operationDetailDocument(p *colorprofile.Writer, operation app.Operation, planned bool, width int) string {
	sections := []string{recoveryOutputHeading(p, "Operation "+operation.ID)}
	if planned {
		sections = append(sections, "Recovery preview; no changes made by this command.")
	}
	metadata := [][]string{{"Command", operation.Kind}, {"State", operation.State}, {"Root", operation.Root}}
	if operation.Remote != "" {
		metadata = append(metadata, []string{"Remote", operation.Remote})
	}
	if operation.Workspace != "" {
		metadata = append(metadata, []string{"Workspace", operation.Workspace})
	}
	if !operation.Created.IsZero() {
		metadata = append(metadata, []string{"Created", operation.Created.Format(time.RFC3339)})
	}
	if operation.Repository != nil {
		metadata = append(metadata, []string{"Repository", operation.Repository.ID}, []string{"Origin", operation.Repository.Remotes["origin"]})
	}
	if operation.Exec != nil {
		arguments := make([]string, 0, len(operation.Exec.Argv))
		for _, argument := range operation.Exec.Argv {
			arguments = append(arguments, strconv.Quote(argument))
		}
		metadata = append(metadata, []string{"Arguments", strings.Join(arguments, " ")}, []string{"Workers", strconv.Itoa(operation.Exec.Jobs)})
	}
	sections = append(sections, terminalRecords(p, metadata, width))
	if len(operation.Steps) > 0 {
		branch, refFormat, execution := false, false, false
		for _, step := range operation.Steps {
			branch = branch || step.Branch != ""
			refFormat = refFormat || step.RefFormat != "" || step.RequestedRefFormat != ""
			execution = execution || step.Execution != nil
		}
		headers := []string{"REPOSITORY", "STATE"}
		if branch {
			headers = append(headers, "BRANCH")
		}
		if refFormat {
			headers = append(headers, "REF FORMAT")
		}
		if execution {
			headers = append(headers, "EXIT", "DURATION")
		}
		rows := make([][]string, 0, len(operation.Steps))
		for _, step := range operation.Steps {
			row := []string{step.Repository, step.State}
			if branch {
				row = append(row, step.Branch)
			}
			if refFormat {
				format := step.RefFormat
				if format == "" {
					format = "-"
					if step.RequestedRefFormat != "" {
						format = step.RequestedRefFormat + " (requested)"
					}
				}
				row = append(row, format)
			}
			if execution {
				exit, duration := "", ""
				if step.Execution != nil {
					exit, duration = strconv.Itoa(step.Execution.ExitCode), step.Execution.Duration.String()
					if step.Execution.Cancelled {
						duration += " (cancelled)"
					}
					if step.Execution.Signal != "" {
						duration += " signal=" + step.Execution.Signal
					}
				}
				row = append(row, exit, duration)
			}
			rows = append(rows, row)
		}
		sections = append(sections, recoveryOutputHeading(p, "Steps"), renderResultRows(p, headers, rows, width))
		for _, step := range operation.Steps {
			details := [][]string{}
			if step.ImportedRepository != nil {
				if origin := step.ImportedRepository.Remotes["origin"]; origin != "" {
					details = append(details, []string{"Origin", origin})
				}
				if connection := step.ImportedRepository.Provider["connection"]; connection != "" {
					details = append(details, []string{"Connection", connection})
				}
			}
			for _, field := range [][2]string{{"Base", step.Base}, {"Start", step.Start}, {"Tracking", step.Tracking}, {"Upstream", step.Upstream}} {
				if field[1] != "" {
					details = append(details, []string{field[0], field[1]})
				}
			}
			if step.Create {
				details = append(details, []string{"Branch action", "Create branch"})
			}
			if len(details) > 0 {
				sections = append(sections, recoveryOutputHeading(p, "Repository "+step.Repository), terminalRecords(p, details, width))
			}
		}
	}
	var failures []string
	if operation.Error != "" {
		failures = append(failures, "Operation: "+repositoryListText(operation.Error))
	}
	for _, step := range operation.Steps {
		if step.Error != "" {
			failures = append(failures, repositoryListText(step.Repository)+": "+repositoryListText(step.Error))
		}
	}
	if len(failures) > 0 {
		sections = append(sections, recoveryOutputHeading(p, "Failure details"), strings.Join(failures, "\n"))
	}
	if operation.State != "complete" && operation.State != "abandoned" {
		root := &options{activeRoot: repositoryListText(operation.Root)}
		id := repositoryListText(operation.ID)
		recovery := []string{}
		if operation.Kind == "exec" {
			recovery = append(recovery, "Commands are never rerun by recovery. Inspect results before stopping tracking.")
		} else {
			recovery = append(recovery, "Retry: "+root.recoveryCommand("resume", id))
		}
		recovery = append(recovery, "Stop tracking: "+root.recoveryCommand("abandon", id)+" --yes", "Stopping tracking retains files and successful work; this operation cannot be resumed afterward.")
		sections = append(sections, recoveryOutputHeading(p, "Recovery"), strings.Join(recovery, "\n"))
	} else if operation.State == "abandoned" {
		sections = append(sections, "Recovery tracking stopped; files and Git effects are retained.")
	}
	return terminalDocument(strings.Join(sections, "\n\n"), width)
}
