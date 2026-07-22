package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pressKey sends a single key to the menu and returns the updated model plus
// whatever message its command produced (nil when there was none).
func pressKey(t *testing.T, m menuModel, key string) (menuModel, tea.Msg) {
	t.Helper()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	updated, ok := nm.(menuModel)
	if !ok {
		t.Fatalf("Update returned %T, want menuModel", nm)
	}
	if cmd == nil {
		return updated, nil
	}
	return updated, cmd()
}

func TestMenuNavigationClamps(t *testing.T) {
	m := newMenu()
	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}

	// Down past the end stays on the last item.
	for i := 0; i < 5; i++ {
		m, _ = pressKey(t, m, "j")
	}
	if want := len(m.items) - 1; m.cursor != want {
		t.Errorf("cursor = %d after 5 downs, want %d (clamped)", m.cursor, want)
	}

	// Up past the start stays on the first item.
	for i := 0; i < 5; i++ {
		m, _ = pressKey(t, m, "k")
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d after 5 ups, want 0 (clamped)", m.cursor)
	}
}

func TestMenuSelectEmitsScreenMessages(t *testing.T) {
	cases := []struct {
		name   string
		cursor int
		want   tea.Msg
	}{
		{"dashboard", 0, openDashboardMsg{}},
		{"settings", 1, openSettingsMsg{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMenu()
			m.cursor = tc.cursor
			cmd := m.selectCmd()
			if cmd == nil {
				t.Fatal("selectCmd returned nil")
			}
			if got := cmd(); got != tc.want {
				t.Errorf("selectCmd() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestMenuQuitItemIsLast(t *testing.T) {
	m := newMenu()
	if got := m.items[len(m.items)-1].title; got != "Quit" {
		t.Errorf("last menu item = %q, want Quit (selectCmd's default branch quits)", got)
	}
}

func TestMenuViewWithinBounds(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 40}, {60, 24}, {30, 10}} {
		m := newMenu()
		nm, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		out := nm.(menuModel).View()

		if strings.TrimSpace(out) == "" {
			t.Errorf("%dx%d: empty view", size.w, size.h)
			continue
		}
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > size.w {
				t.Errorf("%dx%d: line %d width %d exceeds %d: %q",
					size.w, size.h, i, got, size.w, ansiRe.ReplaceAllString(line, ""))
			}
		}
	}
}
