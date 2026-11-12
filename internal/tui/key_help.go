package tui

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
)

func actionBindings(actions ...string) []key.Binding {
	bindings := make([]key.Binding, 0, len(actions))
	for _, action := range actions {
		label, description, _ := strings.Cut(action, " ")
		bindings = append(bindings, key.NewBinding(key.WithKeys(label), key.WithHelp(label, description)))
	}
	return bindings
}

func actionHelp(text string, width int, styles dashboardStyles) string {
	if strings.HasPrefix(text, "Type ") {
		return styles.muted.Render(text)
	}
	return keyboardHelp(actionBindings(strings.Split(text, " | ")...), width, " | ", styles)
}

type progressKeyMap struct {
	cancel key.Binding
	quit   key.Binding
}

func newProgressKeys() progressKeyMap {
	return progressKeyMap{
		cancel: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("Ctrl-C", "to cancel")),
		quit:   key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "to cancel")),
	}
}

func (keys progressKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{keys.cancel, keys.quit}
}

func (keys progressKeyMap) help(width int, styles dashboardStyles) string {
	if width < 8 {
		return styles.focus.Render(keys.quit.Help().Key)
	}
	if width < 16 {
		binding := keys.quit
		binding.SetHelp("q", "cancel")
		return keyboardHelp([]key.Binding{binding}, width, " · ", styles)
	}
	if width < 22 {
		return keyboardHelp([]key.Binding{keys.cancel}, width, " · ", styles)
	}
	return keyboardHelp(keys.ShortHelp(), width, " · ", styles)
}

func keyboardHelp(bindings []key.Binding, width int, separator string, styles dashboardStyles) string {
	component := help.New()
	component.SetWidth(width)
	component.ShortSeparator = separator
	component.Ellipsis = "..."
	component.Styles = help.Styles{
		ShortKey:       styles.focus,
		ShortDesc:      styles.muted,
		ShortSeparator: styles.muted,
		Ellipsis:       styles.muted,
	}
	return component.ShortHelpView(bindings)
}
