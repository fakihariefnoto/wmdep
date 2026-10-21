package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// openService shows one service in a focused view: status card, explicit
// actions, and its logs. This is what enters from a dashboard service card.
func (m *Model) openService(a app.App, h app.Host) tea.Cmd {
	m.screen = screenService
	m.service = a
	m.serviceHost = h
	m.returnTo = screenDashboard
	m.logs = newLogPane()
	m.rebuild()
	return tea.Batch(m.refresh(), m.startLogsFor(a, h))
}

func (m *Model) serviceView() string {
	th := m.th
	a := m.service
	h := m.serviceHost
	st := m.status[a.Key()]

	state := st.State
	if state == "" {
		state = app.StateUnknown
	}
	if st.Err != nil {
		state = app.StateFailed
	}
	uptime := "-"
	if state == app.StateRunning {
		uptime = humanUptime(st.Uptime(m.now()))
	}

	crumb := th.faint.Render("dashboard › "+a.Environment+" › "+a.Host+" › ") + th.title.Render(a.ID)
	lines := []string{m.navBar(), crumb}

	statusLine := th.stateStyle(state).Render(stateGlyph(state)+" "+string(state)) +
		"   " + th.bold.Render(uptime) + "   " +
		th.dim.Render("CPU "+pct(st.CPU)) + "   " + th.dim.Render("MEM "+humanBytes(st.MemBytes))
	if st.Err != nil {
		statusLine += "   " + th.err.Render(st.ErrText())
	}
	target := th.dim.Render(string(a.Kind) + " " + a.Target() + "  ·  " + h.Addr() + " · " + h.User)
	extra := th.faint.Render("pipeline " + nonEmpty(a.Deploy, "none"))
	if a.Health != nil && a.Health.Kind != app.HealthNone {
		extra += th.faint.Render("   health " + healthSummary(a.Health))
	}
	lines = append(lines, m.card("status", []string{statusLine, target, extra}, m.width, 6, true, lipgloss.Color("#1E40AF")))

	actions := th.dim.Render("r restart · x start/stop · u reload · d deploy · e edit · F files · f follow · esc back")
	lines = append(lines, actions)

	logH := max(6, m.height-len(lines)-1)
	m.logs.width = m.width - 4
	m.logs.height = max(1, logH-3)
	lines = append(lines, m.box(m.logs.title(th), m.logs.view(th), m.width, logH, true))

	return strings.Join(clip(flatten(lines), m.width, m.height), "\n")
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (m *Model) handleServiceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.navNumber(msg.String()) {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.stopLogs()
		switch m.returnTo {
		case screenDeployments:
			m.openList(listDeployments)
		case screenServers:
			m.openList(listServers)
		default:
			m.openDashboard(scopeAll)
		}
		return m, nil
	case "q":
		m.stopLogs()
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "r":
		return m, m.actionFor(m.service, m.serviceHost, "restart")
	case "x":
		return m, m.actionFor(m.service, m.serviceHost, "toggle")
	case "u":
		return m, m.actionFor(m.service, m.serviceHost, "reload")
	case "d":
		return m, m.startDeployFor(m.service, m.serviceHost)
	case "e":
		m.form = editDeploymentForm(m.bundle, m.service)
		m.screen = screenEditor
	case "F":
		return m, m.openFiles(m.service.Host)
	case "f":
		return m, m.toggleFollow()
	}
	return m, nil
}
