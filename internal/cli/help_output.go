package cli

import (
	"bytes"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
)

func configureHelp(root *cobra.Command, o *options) {
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		if !isTerminal(out) || o.json || o.output == "json" || o.output == "jsonl" {
			defaultHelp(cmd, args)
			return
		}
		var buffer bytes.Buffer
		func() {
			cmd.SetOut(&buffer)
			defer cmd.SetOut(out)
			defaultHelp(cmd, args)
		}()
		p := o.presentation(out, true)
		_, _ = fmt.Fprint(p, helpText(buffer.String(), p))
	})
}

func helpText(text string, p *colorprofile.Writer) string {
	lines := strings.Split(text, "\n")
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	for index, line := range lines {
		line = repositoryListText(strings.ReplaceAll(line, "\t", "    "))
		label := strings.TrimLeft(line, " ")
		if p.Profile > colorprofile.ASCII {
			switch label {
			case "Usage:", "Available Commands:", "Flags:", "Global Flags:", "Additional help topics:":
				line = line[:len(line)-len(label)] + heading.Render(label)
			}
		}
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}
