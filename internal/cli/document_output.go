package cli

import (
	"io"
	"os"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

func outputWidth(output io.Writer) int {
	if file, ok := output.(*os.File); ok {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 80
}

// terminalDocument renders every wrapped line without starting an input loop.
// Its content is already sanitized; trusted presentation styles remain intact.
func terminalDocument(text string, width int) string {
	if text == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	width = min(width, max(1, lipgloss.Width(text)))
	lines := strings.Split(ansi.Wrap(text, width, ""), "\n")
	component := viewport.New(viewport.WithWidth(width), viewport.WithHeight(len(lines)))
	component.FillHeight = false
	component.SetContent(strings.Join(lines, "\n"))
	visible := strings.Split(component.View(), "\n")
	for index := range lines {
		if index < len(visible) {
			// Remove component padding while retaining intentional prompt spaces.
			lines[index] = ansi.Truncate(visible[index], ansi.StringWidth(lines[index]), "")
		}
	}
	return strings.Join(lines, "\n")
}
