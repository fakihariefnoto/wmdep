package ui

import (
	"context"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
)

// dashScope picks which hosts the dashboard shows.
type dashScope int

const (
	scopeAll dashScope = iota
	scopeDeployments
	scopeServers
)

func (s dashScope) label() string {
	switch s {
	case scopeDeployments:
		return "deployments"
	case scopeServers:
		return "servers"
	default:
		return "overview"
	}
}

type hostRef struct {
	env string
	id  string
}

// dashboard is host-centric: tab changes host, arrows pick a service.
type dashboard struct {
	scope      dashScope
	hostCursor int
	appCursor  int
	offset     int
	hosts      []hostRef
}

func (d *dashboard) build(b *config.Bundle) {
	d.hosts = d.hosts[:0]
	for _, env := range b.Envs {
		hosts := append([]string(nil), env.Hosts...)
		sort.Strings(hosts)
		for _, id := range hosts {
			if _, ok := b.Hosts[id]; !ok {
				continue
			}
			d.hosts = append(d.hosts, hostRef{env: env.Name, id: id})
		}
	}
	if len(d.hosts) == 0 {
		d.hostCursor, d.appCursor = 0, 0
		return
	}
	d.hostCursor = clampIndex(d.hostCursor, len(d.hosts))
}

func (d *dashboard) selectedHost() (hostRef, bool) {
	if d.hostCursor < 0 || d.hostCursor >= len(d.hosts) {
		return hostRef{}, false
	}
	return d.hosts[d.hostCursor], true
}

// moveHost wraps, so tab cycles through the fleet.
func (d *dashboard) moveHost(delta int) {
	if len(d.hosts) == 0 {
		return
	}
	n := len(d.hosts)
	d.hostCursor = ((d.hostCursor+delta)%n + n) % n
	d.appCursor = 0
}

func (d *dashboard) moveApp(delta, n int) {
	if n == 0 {
		return
	}
	d.appCursor = clampIndex(d.appCursor+delta, n)
}

func (m *Model) hostApps() []app.App {
	h, ok := m.dash.selectedHost()
	if !ok {
		return nil
	}
	var apps []app.App
	for _, a := range m.bundle.Apps {
		if a.Environment == h.env && a.Host == h.id {
			apps = append(apps, a)
		}
	}
	sort.SliceStable(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps
}

func (m *Model) dashSelectedApp() (app.App, app.Host, bool) {
	apps := m.hostApps()
	if m.dash.appCursor < 0 || m.dash.appCursor >= len(apps) {
		return app.App{}, app.Host{}, false
	}
	a := apps[m.dash.appCursor]
	h, ok := m.bundle.Hosts[a.Host]
	if !ok {
		return app.App{}, app.Host{}, false
	}
	return a, h, true
}

func (m *Model) dashboardView() string {
	head := []string{m.navBar(), m.fleetCard()}
	var body []string
	focusStart, focusEnd := -1, -1

	for hi, h := range m.dash.hosts {
		sectionStart := len(body)
		focusedHost := hi == m.dash.hostCursor
		body = append(body, m.hostBar(h, focusedHost))

		apps := m.appsForHost(h)
		if len(apps) > 0 {
			cols := min(m.appCols(), len(apps))
			if cols < 1 {
				cols = 1
			}
			cw := (m.width - (cols - 1)) / cols
			for i := 0; i < len(apps); i += cols {
				var parts []string
				for j := 0; j < cols && i+j < len(apps); j++ {
					sel := focusedHost && i+j == m.dash.appCursor
					parts = append(parts, m.appCard(apps[i+j], cw, sel))
				}
				body = append(body, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
			}
		}
		if focusedHost {
			focusStart = sectionStart
			focusEnd = len(body)
		}
	}

	if len(m.dash.hosts) == 0 {
		body = append(body, m.th.dim.Render("no hosts configured — add one from Servers (3)"))
	}

	bodyH := max(1, m.height-len(head)-1)
	if focusStart >= 0 {
		if focusStart < m.dash.offset {
			m.dash.offset = focusStart
		}
		if focusEnd > m.dash.offset+bodyH {
			m.dash.offset = focusEnd - bodyH
		}
	}
	if maxOff := max(0, len(body)-bodyH); m.dash.offset > maxOff {
		m.dash.offset = maxOff
	}
	if m.dash.offset < 0 {
		m.dash.offset = 0
	}
	end := min(len(body), m.dash.offset+bodyH)
	body = body[m.dash.offset:end]

	out := append(head, body...)
	out = append(out, m.dashFooter())
	return strings.Join(clip(flatten(out), m.width, m.height), "\n")
}

func (m *Model) appCols() int {
	switch {
	case m.width >= 160:
		return 4
	case m.width >= 110:
		return 3
	default:
		return 2
	}
}

func (m *Model) appsForHost(h hostRef) []app.App {
	var apps []app.App
	for _, a := range m.bundle.Apps {
		if a.Environment == h.env && a.Host == h.id {
			apps = append(apps, a)
		}
	}
	sort.SliceStable(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps
}

// hostBar is the host section header.
func (m *Model) hostBar(h hostRef, focused bool) string {
	th := m.th
	host, ok := m.bundle.Hosts[h.id]
	if !ok {
		return m.card(h.id, []string{th.err.Render("unknown host")}, m.width, 4, focused, lipgloss.Color("#3A4A78"))
	}
	apps := m.appsForHost(h)
	var cpuSum float64
	var mem int64
	var states []app.State
	running, issues := 0, 0
	for _, a := range apps {
		st := m.status[a.Key()]
		state := st.State
		if state == "" {
			state = app.StateUnknown
		}
		if st.Err != nil {
			state = app.StateFailed
		}
		if state == app.StateRunning && st.Err == nil {
			cpuSum += st.CPU
			mem += st.MemBytes
			running++
		}
		if state != app.StateRunning && state != app.StateStopped {
			issues++
		}
		states = append(states, state)
	}
	worst := worstState(states)
	avg := 0.0
	if running > 0 {
		avg = cpuSum / float64(running)
	}

	line := th.bold.Render(host.ID) + "   " +
		th.dim.Render(host.Addr()+" · "+host.User) + "   " +
		th.faint.Render("["+h.env+"]") + "   " +
		th.stateStyle(worst).Render(stateGlyph(worst)+" "+string(worst)) + "   " +
		th.dim.Render("CPU "+pct(avg)) + "   " +
		th.dim.Render("MEM "+humanBytes(mem)) + "   " +
		th.faint.Render(countLabel(len(apps), "deployment"))
	if issues > 0 {
		line += "   " + th.warn.Render(itoa(issues)+" need attention")
	}
	return m.card("", []string{line}, m.width, 3, focused, lipgloss.Color("#3A4A78"))
}

// appCard is one service card inside a host section.
func (m *Model) appCard(a app.App, w int, selected bool) string {
	th := m.th
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
		uptime = humanUptime(st.Uptime(time.Now()))
	}
	pipeline := a.Deploy
	if pipeline == "" {
		pipeline = "no pipeline"
	}
	lines := []string{
		th.stateStyle(state).Render(stateGlyph(state)+" "+string(state)) + "   " + th.dim.Render(uptime),
		th.dim.Render("CPU "+pct(st.CPU)) + "   " + th.dim.Render("MEM "+humanBytes(st.MemBytes)),
		th.faint.Render(pipeline),
	}
	return m.card(a.ID, lines, w, 6, selected, lipgloss.Color("#2F6FB5"))
}

// flatten expands multi-line card strings into individual lines before clip.
func flatten(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// hostTabs is the host switcher. The active host is filled, so tab/h/l has an
// obvious effect.
func (m *Model) hostTabs() string {
	active := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#161821")).
		Background(lipgloss.Color("#8B7BFF")).
		Padding(0, 1)
	idle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#C9CEE0")).
		Background(lipgloss.Color("#2B2F42")).
		Padding(0, 1)

	var parts []string
	for i, h := range m.dash.hosts {
		label := h.id
		if i == m.dash.hostCursor {
			parts = append(parts, active.Render(label))
		} else {
			parts = append(parts, idle.Render(label))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// hostDetail is the status bar for the selected host.
func (m *Model) hostDetail(h hostRef) string {
	th := m.th
	host, ok := m.bundle.Hosts[h.id]
	if !ok {
		return th.err.Render("unknown host " + h.id)
	}
	apps := m.hostApps()
	var cpuSum float64
	var mem int64
	running, issues := 0, 0
	var states []app.State
	for _, a := range apps {
		st := m.status[a.Key()]
		state := st.State
		if state == "" {
			state = app.StateUnknown
		}
		if st.Err != nil {
			state = app.StateFailed
		}
		if state == app.StateRunning && st.Err == nil {
			cpuSum += st.CPU
			mem += st.MemBytes
			running++
		}
		if state == app.StateFailed || state == app.StateDegraded || state == app.StateUnknown {
			issues++
		}
		states = append(states, state)
	}
	worst := worstState(states)
	avg := 0.0
	if running > 0 {
		avg = cpuSum / float64(running)
	}

	line := th.bold.Render(host.ID) + "  " +
		th.dim.Render(host.Addr()+" · "+host.User) + "  " +
		th.faint.Render("["+h.env+"]") + "   " +
		th.stateStyle(worst).Render(stateGlyph(worst)+" "+string(worst)) + "   " +
		th.dim.Render("CPU "+pct(avg)) + "   " +
		th.dim.Render("MEM "+humanBytes(mem)) + "   " +
		th.faint.Render(countLabel(len(apps), "deployment"))
	if issues > 0 {
		line += "   " + th.warn.Render(itoa(issues)+" need attention")
	}
	return m.card("", []string{line}, m.width, 3, false, lipgloss.Color("#3A4A78"))
}

// serviceRow is one selectable deployment inside the host.
func (m *Model) serviceRow(a app.App, selected bool) string {
	th := m.th
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
		uptime = humanUptime(st.Uptime(time.Now()))
	}
	pipeline := a.Deploy
	if pipeline == "" {
		pipeline = "-"
	}

	plain := "  " + pad(a.ID, 18) +
		pad(string(state), 12) +
		pad(uptime, 9) +
		pad(pct(st.CPU), 7) +
		pad(humanBytes(st.MemBytes), 8) +
		pipeline
	if selected {
		sel := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#3D4B7A")).
			Width(m.width)
		return sel.Render("▸ " + strings.TrimPrefix(plain, "  "))
	}

	glyph := th.stateStyle(state).Render(stateGlyph(state))
	return "  " + glyph + " " + th.bold.Render(pad(a.ID, 17)) +
		th.stateStyle(state).Render(pad(string(state), 12)) +
		th.dim.Render(pad(uptime, 9)) +
		th.dim.Render(pad(pct(st.CPU), 7)) +
		th.dim.Render(pad(humanBytes(st.MemBytes), 8)) +
		th.faint.Render(pipeline)
}

func (m *Model) navBar() string {
	tabs := []struct {
		name   string
		screen screenKind
	}{
		{"1 Dashboard", screenDashboard},
		{"2 Deployments", screenDeployments},
		{"3 Servers", screenServers},
		{"4 Scan", screenScan},
		{"5 Settings", screenSettings},
	}
	active := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#161821")).
		Background(lipgloss.Color("#FF7CC0")).
		Padding(0, 1)
	idle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#C9CEE0")).
		Background(lipgloss.Color("#2B2F42")).
		Padding(0, 1)

	var parts []string
	for _, t := range tabs {
		if m.screen == t.screen {
			parts = append(parts, active.Render(t.name))
		} else {
			parts = append(parts, idle.Render(t.name))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	hint := m.th.faint.Render("1-5 open · tab host · ↑↓ service")
	gap := m.width - lipglossWidth(bar) - lipglossWidth(hint) - 1
	if gap < 1 {
		gap = 1
	}
	return bar + strings.Repeat(" ", gap) + hint
}

// card draws a panel with a tinted title bar.
func (m *Model) card(title string, lines []string, w, h int, focused bool, bg color.Color) string {
	if w < 6 || h < 3 {
		return ""
	}
	style, titleStyle := m.th.pane, m.th.paneTitle
	if focused {
		style, titleStyle = m.th.paneFocused, m.th.paneTitleHi
	}
	innerW, innerH := w-4, h-2

	var content []string
	if title != "" {
		content = append(content, titleStyle.Background(bg).Width(innerW).Render(title))
	}
	content = append(content, clip(lines, innerW, innerH-len(content))...)
	return style.Width(w).Height(h).Render(strings.Join(content, "\n"))
}

func (m *Model) fleetCard() string {
	th := m.th
	var up, down, degraded, failed int
	var cpuSum float64
	var mem int64
	running := 0
	for _, a := range m.bundle.Apps {
		st := m.status[a.Key()]
		switch {
		case st.Err != nil:
			down++
		case st.State == app.StateFailed:
			failed++
		case st.State == app.StateDegraded:
			degraded++
		case st.State == app.StateRunning:
			up++
			cpuSum += st.CPU
			mem += st.MemBytes
			running++
		}
	}
	avg := 0.0
	if running > 0 {
		avg = cpuSum / float64(running)
	}
	line1 := th.bold.Render(itoa(len(m.bundle.Apps))+" deployments") + "    " +
		th.ok.Render("● "+itoa(up)+" up") + "   " +
		th.err.Render("✕ "+itoa(down+failed)+" down") + "   " +
		th.warn.Render("▲ "+itoa(degraded)+" degraded")
	line2 := th.dim.Render("CPU "+pct(avg)+" avg") + "   " +
		th.dim.Render("MEM "+humanBytes(mem)) + "   " +
		th.dim.Render(itoa(len(m.bundle.Hosts))+" hosts") + "   " +
		th.faint.Render(itoa(down)+" unreachable")
	return m.card("fleet", []string{line1, line2}, m.width, 5, false, lipgloss.Color("#4B3FB8"))
}

func (m *Model) dashHeader() string { return "" }

// panel is a border with no title, used by the list and scan screens.
func (m *Model) panel(lines []string, w, h int) string {
	if w < 4 || h < 3 {
		return ""
	}
	return m.th.pane.Width(w).Height(h).Render(strings.Join(clip(lines, w-4, h-2), "\n"))
}

func (m *Model) dashFooter() string {
	return m.th.dim.Render("tab host · ↑↓ service · enter open · r restart · x start/stop · u reload · d deploy · l logs · ? help")
}

func (m *Model) navNumber(k string) bool {
	switch k {
	case "1":
		m.openDashboard(scopeAll)
	case "2":
		m.openList(listDeployments)
	case "3":
		m.openList(listServers)
	case "4":
		m.openList(listServers)
		m.setFlash("select a host and press s to scan")
	case "5":
		m.screen = screenSettings
		m.returnTo = screenDashboard
	default:
		return false
	}
	return true
}

func (m *Model) handleDashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.navNumber(msg.String()) {
		return m, nil
	}

	n := len(m.hostApps())
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Back):
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		m.screen = screenSplash
		return m, nil
	case key.Matches(msg, m.keys.Tab):
		m.dash.moveHost(1)
		return m, nil
	case key.Matches(msg, m.keys.ShiftTab):
		m.dash.moveHost(-1)
		return m, nil
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Left):
		m.dash.moveApp(-1, n)
		return m, nil
	case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.Right):
		m.dash.moveApp(1, n)
		return m, nil
	case key.Matches(msg, m.keys.Top):
		m.dash.appCursor = 0
		return m, nil
	case key.Matches(msg, m.keys.Bottom):
		m.dash.appCursor = max(0, n-1)
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		m.setFlash("refreshing…")
		return m, m.refresh()
	case key.Matches(msg, m.keys.Toggle):
		h, ok := m.dash.selectedHost()
		if !ok {
			return m, nil
		}
		if a, host, ok := m.dashSelectedApp(); ok {
			return m, m.openService(a, host)
		}
		return m, m.openManage(h.env, h.id, "")
	}

	if a, h, ok := m.dashSelectedApp(); ok {
		switch msg.String() {
		case "r":
			return m, m.actionFor(a, h, "restart")
		case "x":
			return m, m.actionFor(a, h, "toggle")
		case "u":
			return m, m.actionFor(a, h, "reload")
		case "l":
			return m, m.openService(a, h)
		case "d":
			return m, m.startDeployFor(a, h)
		}
	}
	return m, nil
}

// actionFor runs a start/stop/restart/reload for an explicit app.
func (m *Model) actionFor(a app.App, h app.Host, verb string) tea.Cmd {
	prov, err := m.reg.For(a)
	if err != nil {
		m.setFlash(err.Error())
		return nil
	}
	keyv := a.Key()
	st := m.status[keyv]
	timeout := m.bundle.Defaults().SSHTimeout.Std()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var err error
		name := verb
		switch verb {
		case "restart":
			err = prov.Restart(ctx, a, h)
		case "reload":
			err = prov.Reload(ctx, a, h)
		case "toggle":
			if st.State == app.StateRunning {
				name = "stop"
				err = prov.Stop(ctx, a, h)
			} else {
				name = "start"
				err = prov.Start(ctx, a, h)
			}
		}
		return actionDoneMsg{key: keyv, name: name, err: err}
	}
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func stateRank(s app.State) int {
	switch s {
	case app.StateFailed:
		return 4
	case app.StateDegraded:
		return 3
	case app.StateUnknown:
		return 2
	case app.StateRunning:
		return 1
	default:
		return 0
	}
}

func ansiTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}
