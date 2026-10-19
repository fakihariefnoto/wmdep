// Package ui is the wmdep terminal interface: a three-pane cockpit over the
// configured environments, hosts, and apps.
package ui

import (
	"bufio"
	"context"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/provider"
	"github.com/fakihariefnoto/wmdep/internal/watch"
)

const (
	minWidth  = 64
	minHeight = 16
	flashTTL  = 5 * time.Second
)

type focus int

const (
	focusTree focus = iota
	focusTable
	focusLogs
)

func (f focus) String() string {
	switch f {
	case focusTree:
		return "hosts"
	case focusTable:
		return "apps"
	default:
		return "logs"
	}
}

// messages produced by commands.
type (
	tickMsg       time.Time
	pollBatchMsg  struct{ ch <-chan watch.Result }
	pollResultMsg struct {
		res watch.Result
		ch  <-chan watch.Result
	}
	pollDoneMsg  struct{}
	logStreamMsg struct {
		key app.Key
		rc  io.ReadCloser
		err error
	}
	logLineMsg struct {
		key  app.Key
		line string
		err  error
	}
	actionDoneMsg struct {
		key  app.Key
		name string
		err  error
	}
)

// Model is the root Bubble Tea model.
type Model struct {
	bundle *config.Bundle
	reg    *provider.Registry
	poller *watch.Poller

	keys KeyMap
	th   theme
	help help.Model
	spin spinner.Model

	width, height int
	focus         focus
	layout        layout

	tree   *hostTree
	table  *appTable
	logs   *logPane
	detail []string

	status   map[app.Key]app.Status
	polling  bool
	lastPoll time.Time

	filtering bool
	filter    string

	showHelp bool
	flash    string
	flashAt  time.Time

	// Demo marks the model as running on the in-memory provider, which the
	// header shows so nobody mistakes fake numbers for production.
	Demo bool

	logReader *bufio.Reader
	logCancel context.CancelFunc
}

// New builds the root model for a loaded bundle.
func New(b *config.Bundle, reg *provider.Registry) *Model {
	th := newTheme(b.Config.UI.Theme != "light")
	h := help.New()
	h.Styles = help.DefaultDarkStyles()

	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(th.accent))

	m := &Model{
		bundle: b,
		reg:    reg,
		keys:   DefaultKeyMap(),
		th:     th,
		help:   h,
		spin:   s,
		tree:   newHostTree(),
		table:  newAppTable(th),
		logs:   newLogPane(),
		status: map[app.Key]app.Status{},
	}
	m.poller = watch.New(reg, b.Defaults().SSHTimeout.Std(), 8)
	m.tree.rebuild(b, m.status)
	m.rebuild()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.tick(), m.refresh(), m.startLogs())
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "wmdep — watch my deployment"
	return v
}

func (m *Model) tick() tea.Cmd {
	d := m.bundle.Defaults().PollInterval.Std()
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// refresh polls the apps that are currently on screen.
func (m *Model) refresh() tea.Cmd {
	targets := m.targets()
	p := m.poller
	interval := m.bundle.Defaults().PollInterval.Std()
	m.polling = true
	return func() tea.Msg {
		return pollBatchMsg{ch: p.Poll(context.Background(), targets, interval)}
	}
}

func (m *Model) targets() []watch.Target {
	env, host, _ := m.tree.selected()
	return watch.Build(m.bundle.Apps, m.bundle.Hosts, env, host, m.filter)
}

func waitForPoll(ch <-chan watch.Result) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return pollDoneMsg{}
		}
		return pollResultMsg{res: res, ch: ch}
	}
}

// selectedApp returns the app under the table cursor and its host.
func (m *Model) selectedApp() (app.App, app.Host, bool) {
	a, ok := m.table.selected()
	if !ok {
		return app.App{}, app.Host{}, false
	}
	h, ok := m.bundle.Hosts[a.Host]
	if !ok {
		return app.App{}, app.Host{}, false
	}
	return a, h, true
}

// setFlash shows a transient message in the status bar.
func (m *Model) setFlash(msg string) {
	m.flash = msg
	m.flashAt = time.Now()
}

func (m *Model) flashText() string {
	if m.flash == "" || time.Since(m.flashAt) > flashTTL {
		return ""
	}
	return m.flash
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applyLayout()
		return m, nil

	case tickMsg:
		m.applyLayout()
		return m, tea.Batch(m.tick(), m.refresh())

	case pollBatchMsg:
		m.polling = true
		return m, waitForPoll(msg.ch)

	case pollResultMsg:
		m.status[msg.res.Target.App.Key()] = msg.res.Status
		if msg.res.Err != nil {
			st := msg.res.Status
			st.Err = msg.res.Err
			m.status[msg.res.Target.App.Key()] = st
		}
		return m, waitForPoll(msg.ch)

	case pollDoneMsg:
		m.polling = false
		m.lastPoll = time.Now()
		m.rebuild()
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setFlash(msg.name + " failed: " + msg.err.Error())
		} else {
			m.setFlash(msg.name + " ok on " + msg.key.ID + "@" + msg.key.Host)
		}
		return m, m.refresh()

	case logStreamMsg:
		if !m.logs.hasKey || msg.key != m.logs.key {
			if msg.rc != nil {
				_ = msg.rc.Close()
			}
			return m, nil
		}
		if msg.err != nil {
			m.logs.err = msg.err.Error()
			return m, nil
		}
		m.logReader = bufio.NewReader(msg.rc)
		return m, readLogCmd(m.logs.key, m.logReader)

	case logLineMsg:
		if !m.logs.hasKey || msg.key != m.logs.key {
			return m, nil
		}
		if line := strings.TrimRight(msg.line, "\r\n"); line != "" {
			m.logs.append(line)
		}
		if msg.err != nil {
			if msg.err != io.EOF && !strings.Contains(msg.err.Error(), "closed pipe") {
				m.logs.err = msg.err.Error()
			}
			m.logReader = nil
			return m, nil
		}
		return m, readLogCmd(m.logs.key, m.logReader)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.updateFilter(msg)
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		m.stopLogs()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Back):
		if m.showHelp {
			m.showHelp = false
		} else if m.filter != "" {
			m.filter = ""
			return m, m.refresh()
		}
		return m, nil
	case key.Matches(msg, m.keys.Tab):
		m.focus = (m.focus + 1) % 3
		return m, nil
	case key.Matches(msg, m.keys.ShiftTab):
		m.focus = (m.focus + 2) % 3
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		m.setFlash("refreshing…")
		return m, m.refresh()
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		return m, nil
	}

	prev, _ := m.table.selected()

	switch m.focus {
	case focusTree:
		switch {
		case key.Matches(msg, m.keys.Up):
			m.tree.move(-1)
		case key.Matches(msg, m.keys.Down):
			m.tree.move(1)
		case key.Matches(msg, m.keys.Top):
			m.tree.toTop()
		case key.Matches(msg, m.keys.Bottom):
			m.tree.toBottom()
		case key.Matches(msg, m.keys.Left):
			m.tree.collapse()
		case key.Matches(msg, m.keys.Right), key.Matches(msg, m.keys.Toggle):
			m.tree.toggle()
		case key.Matches(msg, m.keys.Focus):
			m.focus = focusTable
			return m, nil
		}
		m.rebuild()
		return m, m.syncSelection(prev)

	case focusTable:
		switch {
		case key.Matches(msg, m.keys.Up):
			m.table.move(-1)
		case key.Matches(msg, m.keys.Down):
			m.table.move(1)
		case key.Matches(msg, m.keys.Top):
			m.table.toTop()
		case key.Matches(msg, m.keys.Bottom):
			m.table.toBottom()
		case key.Matches(msg, m.keys.Toggle):
			m.focus = focusLogs
			return m, nil
		case key.Matches(msg, m.keys.Deploy):
			m.setFlash("deploy runs in M5 — `wmdep config validate` already checks these pipelines")
			return m, nil
		case key.Matches(msg, m.keys.Restart):
			return m, m.action("restart")
		case key.Matches(msg, m.keys.Stop):
			return m, m.action("toggle")
		case key.Matches(msg, m.keys.Files):
			m.setFlash("SFTP browser lands in M6")
			return m, nil
		case key.Matches(msg, m.keys.Logs):
			m.focus = focusLogs
			return m, nil
		}
		return m, m.syncSelection(prev)

	case focusLogs:
		switch {
		case key.Matches(msg, m.keys.Follow):
			return m, m.toggleFollow()
		case key.Matches(msg, m.keys.Clear):
			m.logs.lines = nil
		case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Down),
			key.Matches(msg, m.keys.Top), key.Matches(msg, m.keys.Bottom):
			return m, m.toggleFollow()
		}
		return m, nil
	}
	return m, nil
}

// syncSelection restarts the log tail and refreshes the detail pane when the
// cursor moved to a different app. Returning a command avoids blocking Update.
func (m *Model) syncSelection(prev app.App) tea.Cmd {
	cur, ok := m.table.selected()
	if ok && prev.Key() == cur.Key() && m.logs.hasKey {
		return nil
	}
	m.rebuildDetail()
	m.refresh()
	return m.startLogs()
}

func (m *Model) cycleFocus(delta int) {
	n := 3
	m.focus = focus(((int(m.focus)+delta)%n + n) % n)
}

func (m *Model) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filter = ""
	case "enter":
		m.filtering = false
	case "backspace":
		if n := len(m.filter); n > 0 {
			m.filter = m.filter[:n-1]
		}
	default:
		if msg.Text != "" {
			m.filter += msg.Text
		}
	}
	m.rebuild()
	return m, m.refresh()
}

// action runs a start/stop/restart through the provider for the selected app.
func (m *Model) action(name string) tea.Cmd {
	a, h, ok := m.selectedApp()
	if !ok {
		return nil
	}
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
		switch name {
		case "restart":
			err = prov.Restart(ctx, a, h)
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

// startLogs cancels any running stream and begins one for the selected app.
func (m *Model) startLogs() tea.Cmd {
	m.stopLogs()

	a, h, ok := m.selectedApp()
	m.logs = newLogPane()
	if !ok {
		return nil
	}
	m.logs.reset(a.Key())

	prov, err := m.reg.For(a)
	if err != nil {
		m.logs.err = err.Error()
		return nil
	}

	keyv := a.Key()
	tail := m.bundle.Defaults().LogTail
	ctx, cancel := context.WithCancel(context.Background())
	m.logCancel = cancel

	return func() tea.Msg {
		rc, err := prov.Logs(ctx, a, h, provider.LogOpts{Tail: tail, Follow: true})
		return logStreamMsg{key: keyv, rc: rc, err: err}
	}
}

func (m *Model) stopLogs() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.logReader = nil
}

func (m *Model) toggleFollow() tea.Cmd {
	if m.logs.follow {
		m.logs.follow = false
		m.stopLogs()
		m.setFlash("log follow paused")
		return nil
	}
	m.setFlash("following logs")
	return m.startLogs()
}

func readLogCmd(k app.Key, r *bufio.Reader) tea.Cmd {
	if r == nil {
		return nil
	}
	return func() tea.Msg {
		line, err := r.ReadString('\n')
		if line == "" && err != nil {
			return logLineMsg{key: k, err: err}
		}
		return logLineMsg{key: k, line: line, err: err}
	}
}

// rebuild recomputes every derived view from the current status map. Layout is
// applied first so the table's column plan is current before rows are rebuilt.
func (m *Model) rebuild() {
	m.applyLayout()

	env, host, _ := m.tree.selected()
	targets := watch.Build(m.bundle.Apps, m.bundle.Hosts, env, host, m.filter)
	apps := make([]app.App, 0, len(targets))
	for _, t := range targets {
		apps = append(apps, t.App)
	}
	m.tree.rebuild(m.bundle, m.status)
	m.table.setApps(apps, m.status, time.Now(), m.th)
	m.rebuildDetail()
}

func (m *Model) rebuildDetail() {
	a, h, ok := m.selectedApp()
	if !ok {
		m.detail = []string{m.th.faint.Render("no app selected")}
		return
	}
	m.detail = detailLines(a, h, m.status[a.Key()], time.Now(), m.th)
}

// applyLayout sizes the panes for the current terminal. Sized components get
// their interior dimensions: a pane of w columns has w-2 usable, and one of
// those lines is the title.
func (m *Model) applyLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	logH := clampInt(m.height/4, 6, 12)
	bodyH := m.height - 1 - logH - 1
	if bodyH < 5 {
		bodyH = 5
		logH = max(3, m.height-1-bodyH-1)
	}

	treeW := clampInt(m.width/5, 18, 30)
	detailW := 0
	if m.width >= 96 {
		detailW = clampInt(m.width/4, 26, 38)
	}
	tableW := m.width - treeW - detailW
	if tableW < 24 {
		tableW = 24
	}

	bodyInner := bodyH - 2
	logInner := logH - 2

	m.tree.width = treeW - 4
	m.tree.height = max(1, bodyInner-1)
	m.tree.ensureVisible()
	m.table.setSize(max(1, tableW-4), max(1, bodyInner-1))
	m.logs.width = m.width - 4
	m.logs.height = max(1, logInner-1)

	h := m.help
	h.SetWidth(m.width - 2)
	m.help = h

	m.layout = layout{
		bodyH:   bodyH,
		logH:    logH,
		treeW:   treeW,
		tableW:  tableW,
		detailW: detailW,
	}
}

type layout struct {
	bodyH   int
	logH    int
	treeW   int
	tableW  int
	detailW int
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}
	if m.width < minWidth || m.height < minHeight {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.th.warn.Render("terminal too small")+"\n"+
				m.th.dim.Render(itoa(minWidth)+"x"+itoa(minHeight)+" minimum, have "+
					itoa(m.width)+"x"+itoa(m.height)))
	}
	if m.showHelp {
		return m.helpView()
	}
	return m.mainView()
}

func (m *Model) mainView() string {
	l := m.layout

	treeBox := m.box("hosts", m.tree.lines(m.th, m.focus == focusTree), l.treeW, l.bodyH, m.focus == focusTree)
	tableLines := strings.Split(m.table.view(), "\n")
	tableBox := m.box(m.appsTitle(), tableLines, l.tableW, l.bodyH, m.focus == focusTable)

	panes := []string{treeBox, tableBox}
	if l.detailW > 0 {
		panes = append(panes, m.box("detail", m.detail, l.detailW, l.bodyH, false))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)

	logBox := m.box(m.logs.title(m.th), m.logs.view(m.th), m.width, l.logH, m.focus == focusLogs)

	return strings.Join([]string{
		m.headerLine(),
		body,
		logBox,
		m.statusLine(),
	}, "\n")
}

func (m *Model) appsTitle() string {
	env, host, _ := m.tree.selected()
	switch {
	case host != "":
		return "apps · " + host
	case env != "":
		return "apps · " + env
	default:
		return "apps"
	}
}

func (m *Model) headerLine() string {
	envs := strings.Join(m.bundle.EnvNames(), ",")
	if envs == "" {
		envs = "no environments"
	}
	left := m.th.title.Render("wmdep") + m.th.faint.Render("  watch my deployment") +
		m.th.dim.Render("  ·  "+envs+"  ·  "+itoa(len(m.bundle.Apps))+" apps")
	if m.Demo {
		left += "  " + m.th.warn.Render("demo")
	}

	right := ""
	if m.polling {
		right = m.spin.View() + m.th.dim.Render("polling")
	} else if !m.lastPoll.IsZero() {
		right = m.th.faint.Render("updated " + humanAgo(time.Now(), m.lastPoll))
	}
	if m.filter != "" {
		right = m.th.accent.Render("filter: "+m.filter) + "  " + right
	}
	if m.filtering {
		right = m.th.accent.Render("filter: "+m.filter+"█") + "  " + right
	}

	gap := m.width - lipglossWidth(left) - lipglossWidth(right) - 1
	if gap < 1 {
		gap = 1
	}
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, m.width, "…")
}

func (m *Model) statusLine() string {
	left := m.flashText()
	if left == "" {
		left = m.th.dim.Render("pane: " + m.focus.String())
	} else {
		left = m.th.warn.Render(left)
	}
	right := m.help.View(m.keys)
	gap := m.width - lipglossWidth(left) - lipglossWidth(right) - 1
	if gap < 1 {
		gap = 1
	}
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, m.width, "…")
}

// helpView renders the full key reference in a centered box. The help model
// lays out columns to its configured width, so it must be told the interior
// width of the box, not the terminal width.
func (m *Model) helpView() string {
	w := min(m.width-2, 76)
	if w < 20 {
		w = 20
	}

	h := m.help
	h.SetWidth(w - 4)
	m.help = h

	title := m.th.title.Render("wmdep") + m.th.faint.Render("  keys")
	hint := m.th.dim.Render("esc or ? to close")

	body := strings.Split(h.FullHelpView(m.keys.FullHelp()), "\n")
	inner := make([]string, 0, len(body)+4)
	inner = append(inner, title, "")
	inner = append(inner, body...)
	inner = append(inner, "", hint)

	content := clip(inner, w-4, len(inner))
	box := m.th.paneFocused.Width(w).Height(len(content) + 2).Render(strings.Join(content, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// box draws a bordered pane with a title line, hard-clipping content to fit so
// a long line can never push a pane out of shape.
//
// lipgloss v2 sizes a bordered style from the outside: Width and Height are the
// total block size, and content is padded to fill but never truncated. So the
// content is clipped here, to the interior, before rendering.
func (m *Model) box(title string, lines []string, w, h int, focused bool) string {
	if w < 4 || h < 3 {
		return ""
	}
	style, titleStyle := m.th.pane, m.th.paneTitle
	if focused {
		style, titleStyle = m.th.paneFocused, m.th.paneTitleHi
	}
	innerW := w - 4 // 2 border cells + 1 padding cell each side
	innerH := h - 2

	content := make([]string, 0, innerH)
	content = append(content, ansi.Truncate(titleStyle.Render(title), innerW, "…"))
	content = append(content, clip(lines, innerW, innerH-1)...)
	return style.Width(w).Height(h).Render(strings.Join(content, "\n"))
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }
