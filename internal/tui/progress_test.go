package tui

import (
	"bearforge/internal/app"
	"testing"
)

func TestCompletionMessageQuits(t *testing.T) {
	m := model{states: map[string]string{}}
	updated, cmd := m.Update(doneMsg{result: app.NewResult("sync")})
	if cmd == nil {
		t.Fatal("completion did not stop renderer")
	}
	if !updated.(model).finished {
		t.Fatal("completion not retained")
	}
}
