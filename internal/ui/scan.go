package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/provider/sshprov"
)

// Scanner discovers services on a host. The CLI wires the SSH implementation.
type Scanner interface {
	Discover(ctx context.Context, h app.Host) ([]sshprov.Discovered, error)
}

type scanState struct {
	host    app.Host
	env     string
	items   []sshprov.Discovered
	checked []bool
	cursor  int
	loading bool
	err     string
}

type scanLoadedMsg struct {
	items []sshprov.Discovered
	err   error
}

// startScan opens the checklist and begins discovery.
func (m *Model) startScan(hostID string) tea.Cmd {
	h, ok := m.bundle.Hosts[hostID]
	if !ok {
		m.setFlash("unknown host " + hostID)
		return nil
	}
	if m.Scanner == nil {
		m.setFlash("scan needs a live SSH connection (run without --demo)")
		return nil
	}

	m.screen = screenScan
	m.scan = scanState{host: h, loading: true}
	if len(m.bundle.Envs) > 0 {
		m.scan.env = m.bundle.Envs[0].Name
	}

	sc := m.Scanner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), m.bundle.Defaults().SSHTimeout.Std()*3)
		defer cancel()
		items, err := sc.Discover(ctx, h)
		return scanLoadedMsg{items: items, err: err}
	}
}

func (m *Model) handleScanKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.scan.loading {
		if msg.String() == "esc" {
			m.openList(listServers)
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.openList(listServers)
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.scan.cursor = clampIndex(m.scan.cursor-1, len(m.scan.items))
	case "down", "j":
		m.scan.cursor = clampIndex(m.scan.cursor+1, len(m.scan.items))
	case " ":
		if m.scan.cursor < len(m.scan.checked) {
			m.scan.checked[m.scan.cursor] = !m.scan.checked[m.scan.cursor]
		}
	case "a":
		for i := range m.scan.checked {
			m.scan.checked[i] = true
		}
	case "n":
		for i := range m.scan.checked {
			m.scan.checked[i] = false
		}
	case "E":
		m.cycleScanEnv()
	case "enter":
		return m.importScan()
	}
	return m, nil
}

func (m *Model) cycleScanEnv() {
	names := m.bundle.EnvNames()
	if len(names) == 0 {
		return
	}
	cur := 0
	for i, n := range names {
		if n == m.scan.env {
			cur = i
		}
	}
	m.scan.env = names[(cur+1)%len(names)]
}

func (m *Model) importScan() (tea.Model, tea.Cmd) {
	if m.scan.env == "" {
		m.scan.err = "no environment to import into"
		return m, nil
	}
	imported, skipped := 0, 0
	for i, item := range m.scan.items {
		if i >= len(m.scan.checked) || !m.scan.checked[i] {
			continue
		}
		id := scanID(item)
		if id == "" || m.alreadyManaged(item, id) {
			skipped++
			continue
		}
		spec := config.AppSpec{ID: id, Host: m.scan.host.ID, Provider: string(item.Kind)}
		switch item.Kind {
		case app.KindSystemd:
			spec.Unit = item.Name + ".service"
		case app.KindDocker:
			spec.Container = item.Name
		case app.KindPM2:
			spec.Process = item.Name
		default:
			skipped++
			continue
		}
		// One writer per item: each successful write invalidates the bundle's
		// load stamps, so reload before the next one.
		if err := config.NewWriter(m.bundle).UpsertDeployment(m.scan.env, "", spec); err != nil {
			m.scan.err = err.Error()
			continue
		}
		if err := m.reloadConfig(); err != nil {
			m.scan.err = err.Error()
			break
		}
		imported++
	}
	if imported == 0 && m.scan.err == "" {
		m.scan.err = "nothing imported (items already managed or unsupported)"
		return m, nil
	}
	m.openList(listDeployments)
	m.setFlash(fmt.Sprintf("imported %d deployment(s) into %s (%d skipped)", imported, m.scan.env, skipped))
	return m, m.refresh()
}

func (m *Model) alreadyManaged(item sshprov.Discovered, id string) bool {
	for _, a := range m.bundle.Apps {
		if a.Host != m.scan.host.ID || a.Kind != item.Kind {
			continue
		}
		if a.ID == id || a.Target() == item.Name || a.Target() == item.Name+".service" {
			return true
		}
	}
	return false
}

func scanID(item sshprov.Discovered) string {
	id := strings.TrimSpace(strings.TrimSuffix(item.Name, ".service"))
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.ToLower(id)
	return id
}

func (m *Model) scanView() string {
	th := m.th
	s := &m.scan

	header := th.title.Render("wmdep") + th.faint.Render("  › ") + th.accent.Render("scan") +
		th.dim.Render("  ·  "+s.host.ID+"  ·  "+s.host.Addr())
	if s.loading {
		return strings.Join([]string{
			header, "",
			th.dim.Render("discovering services over SSH…"),
			"", th.faint.Render("esc cancel"),
		}, "\n")
	}

	lines := []string{
		th.paneTitle.Render("   " + pad("KIND", 9) + pad("NAME", 24) + "DETAIL"),
	}
	if s.err != "" {
		lines = append(lines, "", th.err.Render(s.err))
	}
	if len(s.items) == 0 {
		lines = append(lines, "", th.faint.Render("no services found on this host"))
	}
	for i, item := range s.items {
		on := i < len(s.checked) && s.checked[i]
		mark := "[ ]"
		if on {
			mark = "[x]"
		}
		plain := mark + "  " + pad(string(item.Kind), 9) + pad(item.Name, 24) + item.Detail
		if i == s.cursor {
			lines = append(lines, th.selected.Render(plain))
			continue
		}
		markStyled := th.warn.Render(mark)
		if on {
			markStyled = th.ok.Render(mark)
		}
		lines = append(lines, markStyled+"  "+th.dim.Render(pad(string(item.Kind), 9))+
			th.bold.Render(pad(item.Name, 24))+th.dim.Render(item.Detail))
	}

	footer := th.dim.Render("space toggle · a all · n none · E env: " + s.env + " · enter import · esc back")
	body := m.panel(clip(lines, m.width-4, max(1, m.height-4)), m.width, m.height-3)
	return strings.Join([]string{header, "", body, footer}, "\n")
}
