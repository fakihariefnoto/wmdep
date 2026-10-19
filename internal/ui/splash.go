package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// wordmark is the splash logo. It is 56 columns wide and 6 rows tall; a test
// asserts the width so it never wraps on an 80-column terminal.
var wordmark = []string{
	"██╗    ██╗ ███╗   ███╗ ██████╗  ███████╗ ██████╗ ",
	"██║    ██║ ████╗ ████║ ██╔══██╗ ██╔════╝ ██╔══██╗",
	"██║ █╗ ██║ ██╔████╔██║ ██║  ██║ █████╗   ██████╔╝",
	"██║███╗██║ ██║╚██╔╝██║ ██║  ██║ ██╔══╝   ██╔═══╝ ",
	"╚███╔███╔╝ ██║ ╚═╝ ██║ ██████╔╝ ███████╗ ██║     ",
	" ╚══╝╚══╝  ╚═╝     ╚═╝ ╚═════╝  ╚══════╝ ╚═╝     ",
}

type splashItem struct {
	name string
	desc string
}

// splash is the menu screen state.
type splash struct {
	cursor int
}

func splashItems() []splashItem {
	return []splashItem{
		{"Dashboard", "fleet at a glance"},
		{"Deployments", "add, edit, delete services"},
		{"Servers", "host inventory"},
		{"Scan", "discover services on a host"},
		{"Settings", "defaults, paths, keybindings"},
		{"Quit", ""},
	}
}

func (s *splash) move(delta int) {
	n := len(splashItems())
	s.cursor = ((s.cursor+delta)%n + n) % n
}

func (m *Model) splashView() string {
	th := m.th
	items := splashItems()

	art := make([]string, 0, len(wordmark))
	gradient := []string{"#7D56F4", "#9557F0", "#B25AE8", "#D05CDF", "#EA5ED4", "#FF5FAF"}
	if !m.th.dark {
		gradient = []string{"#4B2FA8", "#6536B8", "#7C3EC4", "#9346CE", "#A94ED6", "#C2247A"}
	}
	for i, l := range wordmark {
		c := gradient[min(i, len(gradient)-1)]
		style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c))
		art = append(art, style.Render(strings.TrimRight(l, " ")))
	}

	menu := make([]string, 0, len(items))
	for i, it := range items {
		name := pad(it.name, 13)
		switch {
		case i == m.splash.cursor:
			menu = append(menu, th.selected.Render("▸ "+name)+th.dim.Render(it.desc))
		default:
			menu = append(menu, "  "+th.bold.Render(name)+th.dim.Render(it.desc))
		}
	}

	version := Version
	if version == "" || version == "dev" {
		version = "dev"
	} else {
		version = "v" + version
	}

	lines := append([]string{}, art...)
	lines = append(lines, "", th.dim.Render("watch my deployment  ·  services across many servers, over SSH"), "")
	lines = append(lines, menu...)

	// The footer is a single line with the version pinned to the same right
	// edge as the block, so the two halves never drift apart.
	keys := th.faint.Render("↑/k ↓/j move · enter open · ? help · q quit")
	padW := max(0, blockWidth(lines)-lipglossWidth(keys)-lipglossWidth(th.faint.Render(version))-2)
	lines = append(lines, "", keys+strings.Repeat(" ", padW+2)+th.faint.Render(version))

	return renderCenteredBlock(m.width, m.height, lines)
}

// blockWidth is the widest line, so callers can right-align a footer.
func blockWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if n := lipglossWidth(l); n > w {
			w = n
		}
	}
	return w
}

// renderCenteredBlock left-aligns the lines against each other, pads them to a
// common width, and centers that block. Centering line by line would ragged the
// menu; keeping every line flush left aligns the wordmark, tagline, and menu.
func renderCenteredBlock(w, h int, lines []string) string {
	blockW := 0
	for _, l := range lines {
		if n := lipglossWidth(l); n > blockW {
			blockW = n
		}
	}
	left := max(0, (w-blockW)/2)

	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if n := lipglossWidth(l); n < blockW {
			l += strings.Repeat(" ", blockW-n)
		}
		out = append(out, strings.Repeat(" ", left)+l)
	}
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Center, strings.Join(out, "\n"))
}

func (m *Model) handleSplashKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.splash.move(-1)
	case "down", "j":
		m.splash.move(1)
	case "enter":
		return m.activateSplashItem()
	case "?":
		m.showHelp = true
	}
	return m, nil
}

func (m *Model) activateSplashItem() (tea.Model, tea.Cmd) {
	switch m.splash.cursor {
	case 0:
		m.openDashboard(scopeAll)
	case 1:
		m.openList(listDeployments)
		return m, m.refresh()
	case 2:
		m.openList(listServers)
		return m, m.refresh()
	case 3:
		m.openList(listServers)
		m.setFlash("select a host and press s to scan it")
		return m, m.refresh()
	case 4:
		m.openDashboard(scopeAll)
		m.setFlash("settings land in M6; edit ~/.config/wmdep/config.yaml for now")
	case 5:
		return m, tea.Quit
	}
	return m, nil
}

// StartAt selects the initial screen for --screen / --no-splash / ui.splash.
func (m *Model) StartAt(screen string) {
	switch screen {
	case "manage":
		m.screen = screenManage
		m.rebuild()
	case "dashboard":
		m.openDashboard(scopeAll)
	}
}

// openDashboard switches to the card view with a scope and refreshes the poll
// targets for that view.
func (m *Model) openDashboard(scope dashScope) {
	m.screen = screenDashboard
	m.dash.scope = scope
	m.dash.cursor = 0
	m.showHelp = false
	m.rebuild()
}

// openManage enters the three-pane workspace scoped to an environment and/or
// host. Empty strings mean "not scoped".
func (m *Model) openManage(env, host, appID string) tea.Cmd {
	// Remember where Manage was opened from so esc returns there, not always
	// to the dashboard.
	if m.screen == screenDeployments || m.screen == screenServers || m.screen == screenDashboard {
		m.returnTo = m.screen
	}
	m.screen = screenManage
	m.scopeEnv, m.scopeHost = env, host
	m.tree.setScope(env, host)
	if appID != "" {
		m.focusApp(appID)
	}
	m.rebuild()
	return m.refresh()
}

// focusApp moves the table cursor to an app id after a scoped rebuild.
func (m *Model) focusApp(id string) {
	for i, a := range m.table.apps {
		if a.ID == id {
			m.table.tbl.SetCursor(i)
			return
		}
	}
}

// now is a tiny indirection that keeps time out of the render paths' tests.
func (m *Model) now() time.Time { return time.Now() }
