package ui

import (
	"charm.land/bubbles/v2/key"
)

// KeyMap is the single source of truth for bindings: the help overlay, the
// status bar, and Update all read from it.
type KeyMap struct {
	Quit     key.Binding
	Help     key.Binding
	Back     key.Binding
	Tab      key.Binding
	ShiftTab key.Binding
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Top      key.Binding
	Bottom   key.Binding
	Toggle   key.Binding
	Focus    key.Binding
	Refresh  key.Binding
	Filter   key.Binding
	Deploy   key.Binding
	Restart  key.Binding
	Stop     key.Binding
	Logs     key.Binding
	Files    key.Binding
	Follow   key.Binding
	Clear    key.Binding
}

// DefaultKeyMap returns the v0.1 bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next pane")),
		ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇧tab", "previous pane")),
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Left:     key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse")),
		Right:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand")),
		Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:   key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		Toggle:   key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("↵", "select")),
		Focus:    key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("^w", "focus apps")),
		Refresh:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh now")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Deploy:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "deploy")),
		Restart:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
		Stop:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "start/stop")),
		Logs:     key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "logs")),
		Files:    key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "files")),
		Follow:   key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow logs")),
		Clear:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear logs")),
	}
}

// ShortHelp implements help.KeyMap.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tab, k.Up, k.Down, k.Deploy, k.Restart, k.Logs, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab, k.ShiftTab, k.Up, k.Down, k.Left, k.Right, k.Top, k.Bottom},
		{k.Toggle, k.Focus, k.Refresh, k.Filter},
		{k.Deploy, k.Restart, k.Stop, k.Files},
		{k.Logs, k.Follow, k.Clear, k.Help, k.Back, k.Quit},
	}
}
