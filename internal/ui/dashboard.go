package ui

import (
	"sort"
	"strings"

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
	cardEnv cardKind = iota
	cardHost
)

// dashCard is a focusable card. Fleet and attention are informational panels,
// not cards, so they are not in the focus order.
type dashCard struct {
	kind cardKind
	env  string
	host string
}

// dashboard is the card grid state: its scope, focus cursor, and the flattened
// list of focusable cards built from the bundle.
type dashboard struct {
	scope  dashScope
	cursor int
	cards  []dashCard
}

// build recomputes the focusable cards for the current scope.
func (d *dashboard) build(b *config.Bundle) {
	d.cards = d.cards[:0]

	if d.scope != scopeServers {
		for _, env := range b.Envs {
			d.cards = append(d.cards, dashCard{kind: cardEnv, env: env.Name})
		}
	}
	if d.scope != scopeDeployments {
		for _, env := range b.Envs {
			hosts := append([]string(nil), env.Hosts...)
			sort.Strings(hosts)
			for _, id := range hosts {
				if _, ok := b.Hosts[id]; ok {
					d.cards = append(d.cards, dashCard{kind: cardHost, env: env.Name, host: id})
				}
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

func (m *Model) dashboardView() string {
	th := m.th
	d := &m.dash

	lines := []string{
		m.dashHeader(),
		"",
		th.faint.Render(strings.ToUpper(d.scope.label())),
		m.fleetCard(),
	}

	if d.scope != scopeServers {
		lines = append(lines, m.cardRows(cardEnv, true)...)
	}
	if d.scope != scopeDeployments {
		lines = append(lines, "", th.faint.Render("HOSTS"))
		lines = append(lines, m.cardRows(cardHost, false)...)
	}

	lines = append(lines, "", th.faint.Render("ATTENTION"))
	lines = append(lines, m.attentionRows()...)

	// Cards are multi-line blocks, so flatten before clipping: clip() operates
	// on lines, and a block treated as a single line would be truncated at its
	// first row.
	flat := flatten(lines)
	body := clip(flat, m.width, max(1, m.height-1))
	return strings.Join(append(body, m.dashFooter()), "\n")
}

// flatten expands multi-line strings into individual lines.
func flatten(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
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
	return m.th.dim.Render("↑↓←→ cards · enter open · a add · e edit · x delete · s scan · r refresh · ? help")
}

// handleDashboardKey drives the card grid. Cards are a flat focus list, so the
// arrows and tab do the same thing; that keeps the grid navigable without
// spatial hit-testing.
func (m *Model) handleDashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		return m, m.openManage(c.env, "", "")
	}

	switch msg.String() {
	case "a":
		m.setFlash("the deployment editor lands in M1c")
	case "e":
		m.setFlash("edit lands in M1c")
	case "x":
		m.setFlash("delete lands in M1c")
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
func (m *Model) cardRows(kind cardKind, envSection bool) []string {
	var cards []dashCard
	for _, c := range m.dash.cards {
		if c.kind == kind {
			cards = append(cards, c)
		}
	}
	if len(cards) == 0 {
		return nil
	}

	maxCols := 2
	if m.width >= 130 {
		maxCols = 3
	}
	if m.width < 90 {
		maxCols = 1
	}
	if !envSection && m.width >= 90 {
		maxCols = 3
	}
	cols := min(len(cards), maxCols)
	cw := (m.width - (cols - 1)) / cols

	var out []string
	for i := 0; i < len(cards); i += cols {
		var parts []string
		for j := 0; j < cols && i+j < len(cards); j++ {
			c := cards[i+j]
			focused := false
			if sel, ok := m.dash.selected(); ok {
				focused = sel == c
			}
			parts = append(parts, m.cardView(c, cw, focused))
		}
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}
	return out
}

func (m *Model) cardView(c dashCard, w int, focused bool) string {
	if c.kind == cardEnv {
		return m.envCard(c.env, w, focused)
	}
	return m.hostCard(c.env, c.host, w, focused)
}

func (m *Model) envCard(env string, w int, focused bool) string {
	th := m.th

	var apps []app.App
	var up, issues int
	for _, a := range m.bundle.Apps {
		if a.Environment != env {
			continue
		}
		apps = append(apps, a)
		st := m.status[a.Key()]
		if st.Err != nil || st.State == app.StateFailed || st.State == app.StateDegraded {
			issues++
		}
		if st.State == app.StateRunning && st.Err == nil {
			up++
		}
	}

	hosts := m.bundle.HostsOf(env)
	hostNames := make([]string, 0, len(hosts))
	for _, h := range hosts {
		hostNames = append(hostNames, h.ID)
	}

	status := th.ok.Render("● healthy")
	if issues > 0 {
		status = th.warn.Render("▲ " + itoa(issues) + " issues")
	}

	lines := []string{
		th.bold.Render(countLabel(len(apps), "deployment")) + "   " + status,
		th.dim.Render(strings.Join(hostNames, " · ")),
		m.appChips(apps, w-4),
	}
	return m.box(env, lines, w, 6, focused)
}

// appChips renders up to three app states on one line, with a +N remainder.
func (m *Model) appChips(apps []app.App, width int) string {
	const maxChips = 3
	var parts []string
	for i, a := range apps {
		if i >= maxChips {
			break
		}
		st := m.status[a.Key()]
		state := st.State
		if state == "" {
			state = app.StateUnknown
		}
		if st.Err != nil {
			state = app.StateFailed
		}
		label := stateGlyph(state) + " " + a.ID
		parts = append(parts, m.th.stateStyle(state).Render(label))
	}
	if len(apps) > maxChips {
		parts = append(parts, m.th.faint.Render("+"+itoa(len(apps)-maxChips)))
	}
	line := strings.Join(parts, "  ")
	return ansiTruncate(line, width)
}

func (m *Model) hostCard(env, hostID string, w int, focused bool) string {
	th := m.th
	h, ok := m.bundle.Hosts[hostID]
	if !ok {
		return m.box(hostID, []string{th.err.Render("unknown host")}, w, 6, focused)
	}

	var apps []app.App
	var issues int
	var worst app.Status
	worstStateValue := app.StateUnknown
	for _, a := range m.bundle.Apps {
		if a.Environment != env || a.Host != hostID {
			continue
		}
		apps = append(apps, a)
		st := m.status[a.Key()]
		if st.Err != nil || st.State == app.StateFailed || st.State == app.StateDegraded {
			issues++
			if stateRank(st.State) > stateRank(worstStateValue) || st.Err != nil {
				worst = st
				worstStateValue = st.State
			}
		}
	}

	line2 := th.bold.Render(itoa(len(apps)) + " dep")
	switch {
	case hasAnyErr(m.status, env, hostID):
		line2 += "   " + th.err.Render("unreachable")
	case issues > 0:
		line2 += "   " + th.warn.Render(itoa(issues)+" issue")
	default:
		line2 += "   " + th.ok.Render("healthy")
	}

	detail := th.faint.Render("no deployments")
	if issues > 0 {
		reason := worst.ErrText()
		if reason == "" {
			reason = worst.Detail
		}
		if reason == "" {
			reason = string(worst.State)
		}
		detail = m.th.stateStyle(worstStateValue).Render(stateGlyph(worstStateValue) + " " + truncate(reason, w-8))
	} else if len(apps) > 0 {
		detail = th.ok.Render("● " + apps[0].ID + " running")
	}

	lines := []string{
		th.dim.Render(h.Addr() + " · " + h.User),
		line2,
		detail,
	}
	return m.box(hostID, lines, w, 6, focused)
}

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
		return []string{th.ok.Render("● nothing needs attention")}
	}

	const maxRows = 3
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
	return []string{m.panel(rows, m.width, len(rows)+2)}
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
