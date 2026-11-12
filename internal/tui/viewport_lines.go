package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
)

func viewportLines(content []string, offset, width, height int) []string {
	if height <= 0 {
		return nil
	}
	start := min(max(0, offset), max(0, len(content)-1))
	component := viewport.New(viewport.WithWidth(max(1, width)), viewport.WithHeight(height))
	// The controller's offset may expose the last row followed by empty space.
	// Keep that existing scroll contract rather than introducing a second owner.
	component.SetContent(strings.Join(content[start:], "\n"))
	lines := make([]string, height)
	copy(lines, strings.Split(component.View(), "\n"))
	return lines
}
