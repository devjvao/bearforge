package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func (o *options) presentation(output io.Writer, terminal bool) *colorprofile.Writer {
	profile := colorprofile.ASCII
	if terminal && os.Getenv("NO_COLOR") == "" && o.color != "never" && os.Getenv("TERM") != "dumb" {
		if o.color == "always" {
			profile = colorprofile.TrueColor
		} else {
			profile = colorprofile.Detect(output, os.Environ())
		}
	}
	return &colorprofile.Writer{Forward: output, Profile: profile}
}

func outcomeStyle(value string) lipgloss.Style {
	color := "7"
	switch value {
	case "success", "succeeded", "clean", "synced", "registered", "created", "removed", "restored", "valid", "passed", "ok", "completed", "unchanged":
		color = "2"
	case "partial", "changes", "mapping_drift", "planned", "queued", "running", "started", "output", "retired", "warning", "needs_attention", "container_retained", "skipped", "not_run", "not run":
		color = "3"
	case "failed", "unhealthy", "unreadable", "cancelled", "conflict", "missing", "error":
		color = "1"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

func (o *options) diagnostic(label, message string) error {
	if !isTerminal(o.errOut) {
		_, err := fmt.Fprintln(o.errOut, label, message)
		return err
	}
	p := o.presentation(o.errOut, true)
	label = repositoryListText(label)
	if p.Profile > colorprofile.ASCII {
		label = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")).Render(label)
	}
	_, err := fmt.Fprintln(p, terminalDocument(label+" "+repositoryListText(message), outputWidth(o.errOut)))
	return err
}

func (o *options) progressLine(repository, state string) error {
	if !isTerminal(o.errOut) {
		_, err := fmt.Fprintf(o.errOut, "%s: %s\n", repository, state)
		return err
	}
	p := o.presentation(o.errOut, true)
	label := repositoryListText(progressLabel(state, o.progressTitle))
	if p.Profile > colorprofile.ASCII {
		label = outcomeStyle(state).Render(label)
	}
	_, err := fmt.Fprintln(p, terminalDocument(repositoryListText(repository)+": "+label, outputWidth(o.errOut)))
	return err
}

func progressLabel(state, title string) string {
	label := state
	switch state {
	case "running", "started", "output":
		label = "Running"
		switch title {
		case "repo add":
			label = "Cloning repository"
		case "sync", "repo sync":
			label = "Fetching updates"
		case "workspace add":
			label = "Creating worktree"
		case "workspace remove", "workspace remove-repo":
			label = "Removing worktree"
		case "repo remove":
			label = "Retiring repository"
		case "repo restore":
			label = "Restoring repository"
		case "status", "workspace show", "workspace ls", "doctor":
			label = "Checking"
		case "operations resume":
			label = "Recovering operation"
		case "exec":
			label = "Running command"
		}
	case "success", "succeeded":
		label = "Succeeded"
	case "planned", "queued":
		label = "Queued"
	case "not run", "not_run":
		label = "Not run"
	case "synced", "clean", "registered", "created", "removed", "restored", "valid", "passed", "ok", "completed", "unchanged", "failed", "unhealthy", "unreadable", "cancelled", "conflict", "missing", "error", "partial", "changes", "retired", "warning", "skipped", "mapping_drift", "needs_attention", "container_retained":
		label = strings.ToUpper(state[:1]) + strings.ReplaceAll(state[1:], "_", " ")
	}
	return label
}

func (o *options) confirmationText(format string, args ...any) error {
	text := fmt.Sprintf(format, args...)
	if !isTerminal(o.errOut) {
		_, err := io.WriteString(o.errOut, text)
		return err
	}
	p := o.presentation(o.errOut, true)
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = repositoryListText(line)
	}
	text = strings.Join(lines, "\n")
	if p.Profile > colorprofile.ASCII {
		text = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(text)
	}
	_, err := io.WriteString(p, terminalDocument(text, outputWidth(o.errOut)))
	return err
}
