package cli

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"bearforge/internal/app"
)

func (o *options) renderRepositoryList(r app.Result, terminal bool) error {
	items := slices.Clone(r.Items)
	slices.SortFunc(items, func(left, right app.Item) int {
		return strings.Compare(left.Repository, right.Repository)
	})
	if !terminal {
		for _, item := range items {
			if _, err := fmt.Fprintf(o.out, "%s\t%s\n", item.Repository, item.Outcome); err != nil {
				return err
			}
		}
		return nil
	}
	writer := o.presentation(o.out, terminal)
	if len(items) == 0 {
		_, err := fmt.Fprintln(writer, "No repositories registered.\nAdd one with: bearforge repo add NAME URL")
		return err
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{repositoryListText(item.Repository), repositoryListText(item.Outcome)})
	}
	view := terminalTable(writer, []string{"REPOSITORY", "STATUS"}, rows)
	label := "repositories"
	if len(items) == 1 {
		label = "repository"
	}
	_, err := fmt.Fprintf(writer, "%s\n\n%d %s\n", view, len(items), label)
	return err
}

func repositoryListText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return -1
		}
		return character
	}, ansi.Strip(value))
}
