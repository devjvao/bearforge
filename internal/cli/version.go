package cli

import (
	"fmt"
	"strconv"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"bearforge/internal/app"
	"bearforge/internal/buildinfo"
)

func addVersionCommand(root *cobra.Command, o *options) {
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print version and source/build metadata", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		metadata := buildinfo.Info()
		if o.output != "human" {
			r := app.NewResult("version")
			r.Data = metadata
			o.rendered = true
			return o.render(r)
		}
		if isTerminal(o.out) {
			return o.renderVersion(metadata)
		}
		_, err := fmt.Fprintf(o.out, "bearforge %s (commit %s, dirty=%t, %s, %s)\n", metadata.Version, metadata.Commit, metadata.Dirty, metadata.GoVersion, metadata.Platform)
		return err
	}})
}

func (o *options) renderVersion(metadata buildinfo.Metadata) error {
	writer := o.presentation(o.out, true)
	view := table.New().Rows(
		[]string{"Version", repositoryListText(metadata.Version)},
		[]string{"Commit", repositoryListText(metadata.Commit)},
		[]string{"Dirty", strconv.FormatBool(metadata.Dirty)},
		[]string{"Go", repositoryListText(metadata.GoVersion)},
		[]string{"Platform", repositoryListText(metadata.Platform)},
	).Border(lipgloss.Border{}).BorderTop(false).BorderBottom(false).BorderHeader(false).
		StyleFunc(func(row, column int) lipgloss.Style {
			style := lipgloss.NewStyle().PaddingRight(2)
			if column == 0 && writer.Profile > colorprofile.ASCII {
				return style.Bold(true).Foreground(lipgloss.Color("6"))
			}
			return style
		})
	_, err := fmt.Fprintf(writer, "bearforge\n\n%s\n", view)
	return err
}
