package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/watch"
)

type listKind int

const (
	listDeployments listKind = iota
	listServers
)

// listState is the shared cursor/filter state for the Deployments and Servers
// screens.
type listState struct {
	kind          listKind
	cursor        int
	offset        int
	query         string
	filtering     bool
	pendingDelete string
	confirm       string
}

func (m *Model) openList(kind listKind) {
	m.screen = screenDeployments
	if kind == listServers {
		m.screen = screenServers
	}
	m.list = listState{kind: kind}
	m.returnTo = screenDashboard
	m.showHelp = false
	m.rebuild()
}

// listApps returns the deployments matching the current filter.
func (m *Model) listApps() []app.App {
	q := strings.ToLower(strings.TrimSpace(m.list.query))
	out := make([]app.App, 0, len(m.bundle.Apps))
	for _, a := range m.bundle.Apps {
		if q != "" {
			hay := strings.ToLower(a.ID + " " + a.Host + " " + a.Environment + " " + string(a.Kind) + " " + a.Target())
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// listHosts returns the hosts matching the current filter.
func (m *Model) listHosts() []app.Host {
	q := strings.ToLower(strings.TrimSpace(m.list.query))
	out := make([]app.Host, 0, len(m.bundle.Hosts))
	for _, id := range m.bundle.HostOrder {
		h := m.bundle.Hosts[id]
		if q != "" {
			hay := strings.ToLower(h.ID + " " + h.Address + " " + h.User + " " + string(h.Auth))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}

func (m *Model) listLen() int {
	if m.list.kind == listServers {
		return len(m.listHosts())
	}
	return len(m.listApps())
}

func (m *Model) listView() string {
	if m.list.kind == listServers {
		return m.serversView()
	}
	return m.deploymentsView()
}

func (m *Model) listHeader(title string, shown, total int) string {
	left := m.th.title.Render("wmdep") + m.th.faint.Render("  › ") + m.th.accent.Render(title)
	right := m.th.dim.Render(itoa(shown) + " of " + itoa(total))
	if m.list.query != "" {
		right = m.th.accent.Render("filter: "+m.list.query) + "   " + right
	}
	gap := m.width - lipglossWidth(left) - lipglossWidth(right) - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) deploymentsView() string {
	th := m.th
	apps := m.listApps()
	cols := []struct {
		title string
		w     int
	}{
		{"APP", max(10, m.width/4)},
		{"ENV", 10},
		{"HOST", 12},
		{"PROVIDER", 9},
		{"STATE", 12},
		{"PIPELINE", 0},
	}

	header := "  "
	for _, c := range cols {
		header += pad(c.title, c.w)
	}
	lines := []string{th.paneTitle.Render(header)}

	if len(apps) == 0 {
		lines = append(lines, "", th.faint.Render("no deployments match"))
	}
	for i, a := range apps {
		st := m.status[a.Key()]
		state := st.State
		if state == "" {
			state = app.StateUnknown
		}
		if st.Err != nil {
			state = app.StateFailed
		}
		pipeline := a.Deploy
		if pipeline == "" {
			pipeline = "-"
		}
		sel := i == m.list.cursor

		cell := func(s string, w int, style lipgloss.Style) string {
			if sel {
				return pad(s, w)
			}
			return pad(style.Render(s), w)
		}

		row := "  "
		if sel {
			row = "▸ "
		}
		row += cell(a.ID, cols[0].w, th.bold)
		row += cell(a.Environment, cols[1].w, th.dim)
		row += cell(a.Host, cols[2].w, th.dim)
		row += cell(string(a.Kind), cols[3].w, th.dim)
		row += cell(stateGlyph(state)+" "+string(state), cols[4].w, th.stateStyle(state))
		row += pipeline
		if sel {
			row = th.selected.Render(row)
		}
		lines = append(lines, row)
	}

	body := m.panel(clip(lines, m.width-4, max(1, m.height-3)), m.width, m.height-2)
	return strings.Join([]string{
		m.listHeader("deployments", len(apps), len(m.bundle.Apps)),
		body,
		th.dim.Render("enter open · a add · e edit · c duplicate · x delete · / search · esc back"),
	}, "\n")
}

func (m *Model) serversView() string {
	th := m.th
	hosts := m.listHosts()

	lines := []string{th.paneTitle.Render("  " + pad("HOST", 16) + pad("ADDRESS", 24) + pad("USER", 10) + pad("AUTH", 9) + pad("APPS", 6) + "STATE")}
	if len(hosts) == 0 {
		lines = append(lines,
			"", th.faint.Render("no hosts match"),
			"", th.faint.Render("press a to add one"))
	}
	for i, h := range hosts {
		apps := m.appsOnHost(h.ID)
		states := make([]app.State, 0, len(apps))
		for _, a := range apps {
			st := m.status[a.Key()]
			s := st.State
			if s == "" {
				s = app.StateUnknown
			}
			if st.Err != nil {
				s = app.StateFailed
			}
			states = append(states, s)
		}
		state := worstState(states)
		sel := i == m.list.cursor

		cell := func(s string, w int, style lipgloss.Style) string {
			if sel {
				return pad(s, w)
			}
			return pad(style.Render(s), w)
		}
		row := "  "
		if sel {
			row = "▸ "
		}
		row += cell(h.ID, 16, th.bold)
		row += cell(h.Addr(), 24, th.dim)
		row += cell(h.User, 10, th.dim)
		row += cell(string(h.Auth), 9, th.dim)
		row += cell(itoa(len(apps)), 6, th.dim)
		row += stateGlyph(state) + " " + string(state)
		if sel {
			row = th.selected.Render(row)
		}
		lines = append(lines, row)
	}

	body := m.panel(clip(lines, m.width-4, max(1, m.height-3)), m.width, m.height-2)
	return strings.Join([]string{
		m.listHeader("servers", len(hosts), len(m.bundle.Hosts)),
		body,
		th.dim.Render("enter open · a add · e edit · x delete · t test · s scan · / search · esc back"),
	}, "\n")
}

func (m *Model) appsOnHost(id string) []app.App {
	var out []app.App
	for _, a := range m.bundle.Apps {
		if a.Host == id {
			out = append(out, a)
		}
	}
	return out
}

// handleListKey drives both management screens.
func (m *Model) handleListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.list.filtering {
		return m.updateListFilter(msg)
	}
	if m.navNumber(msg.String()) {
		return m, nil
	}
	if m.showHelp {
		switch msg.String() {
		case "esc", "?":
			m.showHelp = false
		}
		return m, nil
	}

	// Typing while a delete is armed feeds the production confirmation.
	if m.list.pendingDelete != "" && msg.Text != "" && msg.String() != "x" {
		m.list.confirm += msg.Text
		return m, nil
	}

	n := m.listLen()
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
	case key.Matches(msg, m.keys.Back):
		m.openDashboard(m.dash.scope)
		m.rebuild()
	case key.Matches(msg, m.keys.Up):
		m.list.cursor = clampIndex(m.list.cursor-1, n)
		m.list.pendingDelete = ""
	case key.Matches(msg, m.keys.Down):
		m.list.cursor = clampIndex(m.list.cursor+1, n)
		m.list.pendingDelete = ""
	case key.Matches(msg, m.keys.Top):
		m.list.cursor = 0
	case key.Matches(msg, m.keys.Bottom):
		m.list.cursor = max(0, n-1)
	case key.Matches(msg, m.keys.Refresh):
		m.setFlash("refreshing…")
		return m, m.refresh()
	case key.Matches(msg, m.keys.Toggle):
		return m.openSelectedFromList()
	case msg.String() == "/":
		m.list.filtering = true
	case msg.String() == "a":
		return m.startCreate()
	case msg.String() == "e":
		return m.startEdit()
	case msg.String() == "c":
		return m.startDuplicate()
	case msg.String() == "x":
		return m.deleteSelected()
	case msg.String() == "f" && m.list.kind == listServers:
		hosts := m.listHosts()
		if len(hosts) > 0 && m.list.cursor < len(hosts) {
			return m, m.openFiles(hosts[m.list.cursor].ID)
		}
	case msg.String() == "t" && m.list.kind == listServers:
		m.setFlash("connection test lands with the SSH core (M2)")
	case msg.String() == "s":
		if m.list.kind == listServers {
			hosts := m.listHosts()
			if len(hosts) > 0 && m.list.cursor < len(hosts) {
				return m, m.startScan(hosts[m.list.cursor].ID)
			}
			return m, nil
		}
		m.setFlash("scan a host from the Servers screen")
	}
	return m, nil
}

func (m *Model) updateListFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.list.filtering = false
		m.list.query = ""
		m.list.cursor = 0
	case "enter":
		m.list.filtering = false
	default:
		if msg.String() == "backspace" {
			if n := len(m.list.query); n > 0 {
				m.list.query = m.list.query[:n-1]
			}
		} else if msg.Text != "" {
			m.list.query += msg.Text
		}
		m.list.cursor = 0
	}
	return m, nil
}

func (m *Model) openSelectedFromList() (tea.Model, tea.Cmd) {
	if m.list.kind == listServers {
		hosts := m.listHosts()
		if len(hosts) == 0 || m.list.cursor >= len(hosts) {
			return m, nil
		}
		h := hosts[m.list.cursor]
		env := ""
		for _, e := range m.bundle.Envs {
			for _, id := range e.Hosts {
				if id == h.ID {
					env = e.Name
					break
				}
			}
			if env != "" {
				break
			}
		}
		return m, m.openManage(env, h.ID, "")
	}

	apps := m.listApps()
	if len(apps) == 0 || m.list.cursor >= len(apps) {
		return m, nil
	}
	a := apps[m.list.cursor]
	return m, m.openManage(a.Environment, a.Host, a.ID)
}

func (m *Model) startCreate() (tea.Model, tea.Cmd) {
	switch m.list.kind {
	case listServers:
		m.form = newHostForm()
		m.screen = screenHostEditor
	default:
		env, host := "", ""
		if len(m.bundle.Envs) > 0 {
			env = m.bundle.Envs[0].Name
			if len(m.bundle.Envs[0].Hosts) > 0 {
				host = m.bundle.Envs[0].Hosts[0]
			}
		}
		if host == "" && len(m.bundle.HostOrder) > 0 {
			host = m.bundle.HostOrder[0]
		}
		m.form = newDeploymentForm(m.bundle, env, host)
		m.screen = screenEditor
	}
	return m, nil
}

func (m *Model) startEdit() (tea.Model, tea.Cmd) {
	if m.list.kind == listServers {
		hosts := m.listHosts()
		if len(hosts) == 0 || m.list.cursor >= len(hosts) {
			return m, nil
		}
		m.form = editHostForm(hosts[m.list.cursor])
		m.screen = screenHostEditor
		return m, nil
	}
	apps := m.listApps()
	if len(apps) == 0 || m.list.cursor >= len(apps) {
		return m, nil
	}
	m.form = editDeploymentForm(m.bundle, apps[m.list.cursor])
	m.screen = screenEditor
	return m, nil
}

func (m *Model) startDuplicate() (tea.Model, tea.Cmd) {
	if m.list.kind != listDeployments {
		return m, nil
	}
	apps := m.listApps()
	if len(apps) == 0 || m.list.cursor >= len(apps) {
		return m, nil
	}
	a := apps[m.list.cursor]
	if m.envFileTemplated(a.Environment) {
		m.setFlash("environments/" + a.Environment + ".yaml uses ${…} templates — edit it by hand")
		return m, nil
	}
	m.form = duplicateDeploymentForm(m.bundle, a)
	m.screen = screenEditor
	return m, nil
}

// deleteSelected uses a two-press confirmation: the first x arms, the second
// deletes the same row.
func (m *Model) deleteSelected() (tea.Model, tea.Cmd) {
	if m.list.kind == listServers {
		hosts := m.listHosts()
		if len(hosts) == 0 || m.list.cursor >= len(hosts) {
			return m, nil
		}
		id := hosts[m.list.cursor].ID
		if m.list.pendingDelete != id {
			m.list.pendingDelete = id
			m.setFlash("press x again to delete host " + id)
			return m, nil
		}
		w := config.NewWriter(m.bundle)
		if err := w.DeleteHost(id); err != nil {
			m.setFlash(err.Error())
			m.list.pendingDelete = ""
			return m, nil
		}
		if err := m.reloadConfig(); err != nil {
			m.setFlash(err.Error())
			return m, nil
		}
		m.setFlash("deleted host " + id)
		return m, m.refresh()
	}

	apps := m.listApps()
	if len(apps) == 0 || m.list.cursor >= len(apps) {
		return m, nil
	}
	a := apps[m.list.cursor]
	if m.list.pendingDelete != a.Key().String() {
		m.list.pendingDelete = a.Key().String()
		m.list.confirm = ""
		if a.Environment == "prod" {
			m.setFlash("type " + a.ID + " then press x to delete from prod")
		} else {
			m.setFlash("press x again to delete " + a.ID + " from " + a.Environment)
		}
		return m, nil
	}
	if a.Environment == "prod" && m.list.confirm != a.ID {
		m.setFlash("type " + a.ID + " then press x to confirm")
		return m, nil
	}
	w := config.NewWriter(m.bundle)
	if err := w.DeleteDeployment(a.Environment, a.ID); err != nil {
		m.setFlash(err.Error())
		m.list.pendingDelete = ""
		return m, nil
	}
	if err := m.reloadConfig(); err != nil {
		m.setFlash(err.Error())
		return m, nil
	}
	m.setFlash("deleted " + a.ID)
	return m, m.refresh()
}

// handleFormKey edits the active form and saves on ctrl+s.
func (m *Model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := &m.form
	back := screenDeployments
	if f.kind == "host" {
		back = screenServers
	}

	switch msg.String() {
	case "esc":
		if f.dirty && !f.confirmEsc {
			f.confirmEsc = true
			f.err = "unsaved changes — press esc again to discard"
			return m, nil
		}
		m.screen = back
		m.form.err = ""
		return m, nil
	case "ctrl+s":
		return m.saveForm(back)
	case "tab", "down":
		f.move(1)
		return m, nil
	case "shift+tab", "up":
		f.move(-1)
		return m, nil
	case "enter", "right", "left":
		f.dirty = true
		if msg.String() == "left" {
			f.cycle(-1)
		} else {
			f.cycle(1)
		}
		m.applyTemplate()
		return m, nil
	case "backspace":
		f.dirty = true
		f.backspace()
		return m, nil
	}

	if msg.Text != "" {
		f.dirty = true
		f.insert(msg.Text)
	}
	return m, nil
}

func (m *Model) saveForm(back screenKind) (tea.Model, tea.Cmd) {
	f := &m.form
	w := config.NewWriter(m.bundle)

	var err error
	if f.kind == "host" {
		id, spec, e := f.hostSpec()
		if e != nil {
			f.err = e.Error()
			return m, nil
		}
		err = w.UpsertHost(id, f.originalID, spec)
	} else {
		env, spec, e := f.deploymentSpec()
		if e != nil {
			f.err = e.Error()
			return m, nil
		}
		err = w.UpsertDeployment(env, f.originalID, spec)
	}
	if err != nil {
		f.err = err.Error()
		return m, nil
	}
	if err := m.reloadConfig(); err != nil {
		f.err = err.Error()
		return m, nil
	}
	f.dirty = false
	m.screen = back
	m.setFlash(f.title + " saved")
	return m, m.refresh()
}

// reloadConfig re-reads the directory after a write and rebuilds derived views.
func (m *Model) reloadConfig() error {
	b, diags := config.Load(m.bundle.Dir, config.Options{})
	var errs []string
	for _, d := range diags {
		if d.Severity == "error" {
			errs = append(errs, d.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config reload failed: %s", strings.Join(errs, "; "))
	}
	m.bundle = b
	m.poller = watch.New(m.reg, b.Defaults().SSHTimeout.Std(), 8)
	m.dash.build(b)
	m.tree.rebuild(b, m.status)
	m.rebuild()
	return nil
}

// applyTemplate prefills the form when the template field changes.
func (m *Model) applyTemplate() {
	f := &m.form
	if f.kind != "deployment" || f.cursor >= len(f.fields) || f.fields[f.cursor].label != "template" {
		return
	}
	id := strings.TrimSpace(f.value("id"))
	if id == "" {
		id = "app"
	}
	switch f.value("template") {
	case "go-systemd", "node-systemd":
		port := "8080"
		if f.value("template") == "node-systemd" {
			port = "3000"
		}
		f.set("provider", "systemd")
		f.set("target", id+".service")
		f.set("workdir", "/srv/"+id)
		f.set("health", "http")
		f.set("health target", "http://127.0.0.1:"+port+"/health")
		f.set("expect", "200")
	case "docker":
		f.set("provider", "docker")
		f.set("target", id)
	}
}

// envFileTemplated reports whether an environment file uses ${...} expansion.
// Editing it from a form would bake resolved values into the repo.
func (m *Model) envFileTemplated(env string) bool {
	path := filepath.Join(config.LayoutFor(m.bundle.Dir).Environments, env+".yaml")
	return m.templatedFile(path)
}

func (m *Model) templatedFile(path string) bool {
	return m.bundle.Templated(path)
}
