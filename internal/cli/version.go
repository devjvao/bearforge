package cli

import (
	"fmt"
	"strconv"

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
	view := terminalRecords(writer, [][]string{
		{"Version", metadata.Version},
		{"Commit", metadata.Commit},
		{"Dirty", strconv.FormatBool(metadata.Dirty)},
		{"Go", metadata.GoVersion},
		{"Platform", metadata.Platform},
	}, outputWidth(o.out))
	_, err := fmt.Fprintf(writer, "bearforge\n\n%s\n", view)
	return err
}
