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

// dashScope is the dashboard preset chosen from the splash menu. It changes
// which sections are shown, not which screen is rendered.
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

type cardKind int

const (
	cardHost cardKind = iota
	cardApp
)

// dashCard is one focusable item. The dashboard is grouped by host: a host bar
// followed by one card per deployment on that host.
type dashCard struct {
	kind cardKind
	env  string
	host string
	app  string
}

type dashboard struct {
	scope  dashScope
	cursor int
	offset int
	cards  []dashCard
}

func (d *dashboard) build(b *config.Bundle) {
	d.cards = d.cards[:0]

	for _, env := range b.Envs {
		hosts := append([]string(nil), env.Hosts...)
		sort.Strings(hosts)
		for _, hostID := range hosts {
			if _, ok := b.Hosts[hostID]; !ok {
				continue
			}
			d.cards = append(d.cards, dashCard{kind: cardHost, env: env.Name, host: hostID})
			if d.scope == scopeServers {
				continue
			}
			var apps []app.App
			for _, a := range b.Apps {
				if a.Environment == env.Name && a.Host == hostID {
					apps = append(apps, a)
				}
			}
			sort.SliceStable(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
			for _, a := range apps {
				d.cards = append(d.cards, dashCard{kind: cardApp, env: env.Name, host: hostID, app: a.ID})
			}
		}
	}

	if len(d.cards) == 0 {
		d.cursor = 0
		return
	}
	d.cursor = clampIndex(d.cursor, len(d.cards))
}

func (d *dashboard) selected() (dashCard, bool) {
	if d.cursor < 0 || d.cursor >= len(d.cards) {
		return dashCard{}, false
	}
	return d.cards[d.cursor], true
}

func (d *dashboard) move(delta int) {
	if len(d.cards) == 0 {
		return
	}
	d.cursor = clampIndex(d.cursor+delta, len(d.cards))
}

// dashboardView draws hosts as sections: a host status bar, then that host's
// deployments as cards. Nav and fleet stay pinned; the host body scrolls.
func (m *Model) dashboardView() string {
	sel, _ := m.dash.selected()

	head := []string{m.navBar(), m.fleetCard()}
	var body []string
	focusStart, focusEnd := -1, -1

	for i := 0; i < len(m.dash.cards); {
		c := m.dash.cards[i]
		if c.kind != cardHost {
			i++
			continue
		}
		start := len(body)
		if sel == c {
			focusStart = start
		}
		body = append(body, m.hostBar(c.env, c.host, sel == c))
		if sel == c {
			focusEnd = len(body)
		}

		var apps []dashCard
		j := i + 1
		for ; j < len(m.dash.cards) && m.dash.cards[j].kind == cardApp; j++ {
			apps = append(apps, m.dash.cards[j])
		}
		if len(apps) > 0 {
			start = len(body)
			rows := m.appGrid(apps, sel)
			body = append(body, rows...)
			for k, appCard := range apps {
				if sel == appCard {
					// One row per `cols` cards; approximate the focused row.
					cols := m.appCols()
					focusStart = start + k/cols
					focusEnd = focusStart + 1
				}
			}
		}
		i = j
	}

	body = append(body, m.attentionRows()...)
	activity := m.activityRows()
	body = append(body, m.card("recent runs", activity, m.width, len(activity)+3, false, lipgloss.Color("#1E3A2F")))

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

// hostBar is the section header for one host.
func (m *Model) hostBar(env, hostID string, focused bool) string {
	th := m.th
	h, ok := m.bundle.Hosts[hostID]
	if !ok {
		return m.card(hostID, []string{th.err.Render("unknown host")}, m.width, 4, focused, lipgloss.Color("#2A2D3A"))
	}

	var apps []app.App
	var states []app.State
	var cpuSum float64
	var memSum int64
	running := 0
	var issues int
	for _, a := range m.bundle.Apps {
		if a.Environment != env || a.Host != hostID {
			continue
		}
		apps = append(apps, a)
		st := m.status[a.Key()]
		state := st.State
		if state == "" {
			state = app.StateUnknown
		}
		if st.Err != nil {
			state = app.StateFailed
			issues++
		}
		if st.State == app.StateFailed || st.State == app.StateDegraded {
			issues++
		}
		if state == app.StateRunning && st.Err == nil {
			cpuSum += st.CPU
			memSum += st.MemBytes
			running++
		}
		states = append(states, state)
	}
	worst := worstState(states)
	avg := 0.0
	if running > 0 {
		avg = cpuSum / float64(running)
	}

	line := th.dim.Render(h.Addr()+" · "+h.User) + "   " +
		m.th.stateStyle(worst).Render(stateGlyph(worst)+" "+string(worst)) + "   " +
		th.dim.Render("CPU "+pct(avg)) + "   " + th.dim.Render("MEM "+humanBytes(memSum)) + "   " +
		th.faint.Render(countLabel(len(apps), "deployment")+" · "+env)
	if issues > 0 {
		line += "   " + th.warn.Render(itoa(issues)+" issues")
	}
	return m.card(hostID, []string{line}, m.width, 4, focused, lipgloss.Color("#2A2D3A"))
}

// appGrid lays out one host's deployments as cards.
func (m *Model) appGrid(cards []dashCard, sel dashCard) []string {
	cols := min(m.appCols(), len(cards))
	if cols < 1 {
		cols = 1
	}
	cw := (m.width - (cols - 1)) / cols
	var out []string
	for i := 0; i < len(cards); i += cols {
		var parts []string
		for j := 0; j < cols && i+j < len(cards); j++ {
			parts = append(parts, m.appCard(cards[i+j], cw, cards[i+j] == sel))
		}
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}
	return out
}

func (m *Model) appCard(c dashCard, w int, focused bool) string {
	th := m.th
	var a app.App
	for _, candidate := range m.bundle.Apps {
		if candidate.Environment == c.env && candidate.Host == c.host && candidate.ID == c.app {
			a = candidate
			break
		}
	}
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
	line2 := th.stateStyle(state).Render(stateGlyph(state)+" "+string(state)) + "   " + th.dim.Render(uptime)
	line3 := th.dim.Render("CPU "+pct(st.CPU)) + "   " + th.dim.Render("MEM "+humanBytes(st.MemBytes))
	pipeline := a.Deploy
	if pipeline == "" {
		pipeline = "no pipeline"
	}
	line4 := th.faint.Render(pipeline)

	return m.card(c.app, []string{line2, line3, line4}, w, 6, focused, lipgloss.Color("#1F3A5F"))
}

func flatten(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// navBar is the persistent wayfinder: it shows every top-level screen and
// highlights the one being shown, so the dashboard is never a dead end.
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
	active := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#12131A")).
		Background(lipgloss.Color("#FF5FAF")).Padding(0, 1)
	idle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8FA3")).
		Background(lipgloss.Color("#22242E")).Padding(0, 1)

	var parts []string
	for _, t := range tabs {
		if m.screen == t.screen {
			parts = append(parts, active.Render(t.name))
		} else {
			parts = append(parts, idle.Render(t.name))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	hint := m.th.faint.Render("1-5 open · tab/←→ cards · enter select")
	gap := m.width - lipglossWidth(bar) - lipglossWidth(hint) - 1
	if gap < 1 {
		gap = 1
	}
	return bar + strings.Repeat(" ", gap) + hint
}

// card draws a panel with a tinted title bar. The background lives on the
// header only: inner text styles reset backgrounds, so tinting the whole body
// would produce patchy blocks on real terminals.
func (m *Model) card(title string, lines []string, w, h int, focused bool, bg color.Color) string {
	if w < 6 || h < 3 {
		return ""
	}
	style, titleStyle := m.th.pane, m.th.paneTitle
	if focused {
		style, titleStyle = m.th.paneFocused, m.th.paneTitleHi
	}
	innerW, innerH := w-4, h-2

	header := titleStyle.
		Background(bg).
		Width(innerW).
		Render(title)

	content := make([]string, 0, innerH)
	content = append(content, header)
	content = append(content, clip(lines, innerW, innerH-1)...)
	return style.Width(w).Height(h).Render(strings.Join(content, "\n"))
}

func (m *Model) dashHeader() string {
	th := m.th
	left := th.title.Render("wmdep") + th.faint.Render("  dashboard")
	if m.dash.scope != scopeAll {
		left += "  " + th.accent.Render("["+m.dash.scope.label()+"]")
	}

	right := th.dim.Render(strings.Join(m.bundle.EnvNames(), ",") + " · " +
		itoa(len(m.bundle.Hosts)) + " hosts")
	if m.Demo {
		right += " · " + th.warn.Render("demo")
	}
	gap := m.width - lipglossWidth(left) - lipglossWidth(right) - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) dashFooter() string {
	return m.th.dim.Render("enter open · r restart · x start/stop · u reload · d deploy · l logs · ? help")
}

// navNumber maps 1-5 to the top-level screens, so the tab bar is real
// navigation rather than decoration.
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

// dashSelectedApp resolves the app behind the focused card, if any.
func (m *Model) dashSelectedApp() (app.App, app.Host, bool) {
	c, ok := m.dash.selected()
	if !ok || c.kind != cardApp {
		return app.App{}, app.Host{}, false
	}
	h, ok := m.bundle.Hosts[c.host]
	if !ok {
		return app.App{}, app.Host{}, false
	}
	for _, a := range m.bundle.Apps {
		if a.Environment == c.env && a.Host == c.host && a.ID == c.app {
			return a, h, true
		}
	}
	return app.App{}, app.Host{}, false
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

// handleDashboardKey drives the card grid. Cards are a flat focus list, so the
// arrows and tab do the same thing; that keeps the grid navigable without
// spatial hit-testing.
func (m *Model) handleDashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.navNumber(msg.String()) {
		return m, nil
	}
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
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Left),
		key.Matches(msg, m.keys.ShiftTab):
		m.dash.move(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.Right),
		key.Matches(msg, m.keys.Tab):
		m.dash.move(1)
		return m, nil
	case key.Matches(msg, m.keys.Top):
		m.dash.cursor = 0
		return m, nil
	case key.Matches(msg, m.keys.Bottom):
		if n := len(m.dash.cards); n > 0 {
			m.dash.cursor = n - 1
		}
		return m, nil
	case key.Matches(msg, m.keys.Refresh), key.Matches(msg, m.keys.Restart):
		m.setFlash("refreshing…")
		return m, m.refresh()
	case key.Matches(msg, m.keys.Toggle):
		c, ok := m.dash.selected()
		if !ok {
			return m, nil
		}
		if c.kind == cardHost {
			return m, m.openManage(c.env, c.host, "")
		}
		return m, m.openManage(c.env, c.host, c.app)
	}

	// Service actions work directly from a focused app card.
	if a, h, ok := m.dashSelectedApp(); ok {
		switch msg.String() {
		case "r":
			return m, m.actionFor(a, h, "restart")
		case "x":
			return m, m.actionFor(a, h, "toggle")
		case "u":
			return m, m.actionFor(a, h, "reload")
		case "l":
			return m, m.openManage(a.Environment, a.Host, a.ID)
		case "d":
			return m, m.startDeployFor(a, h)
		}
	}

	switch msg.String() {
	case "a":
		m.list.kind = listDeployments
		return m.startCreate()
	case "e", "x":
		m.openList(listDeployments)
		if msg.String() == "e" {
			m.setFlash("select a deployment to edit")
		} else {
			m.setFlash("select a deployment to delete")
		}
		return m, m.refresh()
	case "s":
		m.setFlash("host scan lands in M2b — it needs the SSH discovery pass")
	}
	return m, nil
}

func (m *Model) fleetCard() string {
	th := m.th
	var up, down, degraded, failed int
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
		}
	}

	line := th.bold.Render(itoa(len(m.bundle.Apps))+" deployments") + "    " +
		th.ok.Render("● "+itoa(up)+" up") + "   " +
		th.err.Render("✕ "+itoa(down+failed)+" down") + "   " +
		th.warn.Render("▲ "+itoa(degraded)+" degraded")

	return m.box("fleet", []string{line}, m.width, 4, false)
}

// cardRows lays out the cards of one kind in a grid and marks the focused one.
// attentionRows lists every deployment that is failing, degraded, or
// unreachable, worst first.
func (m *Model) attentionRows() []string {
	th := m.th

	type issue struct {
		app app.App
		st  app.Status
	}
	var issues []issue
	for _, a := range m.bundle.Apps {
		st := m.status[a.Key()]
		if st.Err != nil || st.State == app.StateFailed || st.State == app.StateDegraded {
			issues = append(issues, issue{app: a, st: st})
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		ri, rj := stateRank(issues[i].st.State), stateRank(issues[j].st.State)
		if issues[i].st.Err != nil {
			ri = 9
		}
		if issues[j].st.Err != nil {
			rj = 9
		}
		if ri != rj {
			return ri > rj
		}
		return issues[i].app.Key().String() < issues[j].app.Key().String()
	})

	if len(issues) == 0 {
		return []string{m.card("needs attention", []string{th.ok.Render("● nothing needs attention")}, m.width, 4, false, lipgloss.Color("#4A1F24"))}
	}

	const maxRows = 2
	var rows []string
	for i, is := range issues {
		if i >= maxRows {
			rows = append(rows, th.faint.Render("+"+itoa(len(issues)-maxRows)+" more"))
			break
		}
		state := is.st.State
		if is.st.Err != nil {
			state = app.StateFailed
		}
		reason := is.st.ErrText()
		if reason == "" {
			reason = is.st.Detail
		}
		if reason == "" || reason == string(state) {
			reason = ""
		}
		where := pad(is.app.Host+" · "+is.app.Environment, 16)
		reasonCell := th.faint.Render(truncate(reason, max(8, m.width-42)))
		rows = append(rows,
			m.th.stateStyle(state).Render(stateGlyph(state)+" "+pad(is.app.ID, 10))+
				th.dim.Render(where)+
				m.th.stateStyle(state).Render(pad(string(state), 10))+
				reasonCell)
	}
	return []string{m.card("needs attention", rows, m.width, len(rows)+3, false, lipgloss.Color("#4A1F24"))}
}

// panel draws a borderless-content box (no title row) for the attention list.
func (m *Model) panel(lines []string, w, h int) string {
	if w < 4 || h < 3 {
		return ""
	}
	return m.th.pane.Width(w).Height(h).Render(strings.Join(clip(lines, w-4, h-2), "\n"))
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

func hasAnyErr(status map[app.Key]app.Status, env, host string) bool {
	for k, st := range status {
		if k.Env == env && k.Host == host && st.Err != nil {
			return true
		}
	}
	return false
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
