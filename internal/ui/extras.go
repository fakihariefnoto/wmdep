package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *Model) deployLogView() string {
	th := m.th
	lines := strings.Split(strings.TrimRight(m.deployLog, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{th.faint.Render("no output")}
	}
	title := "deploy · " + m.deployApp
	body := m.panel(clip(lines, m.width-4, max(1, m.height-3)), m.width, m.height-2)
	return strings.Join([]string{title, body, th.dim.Render("esc back")}, "\n")
}

func (m *Model) settingsView() string {
	th := m.th
	d := m.bundle.Defaults()
	kv := func(k, v string) string { return th.dim.Render(pad(k, 14)) + th.bold.Render(v) }

	lines := []string{
		th.title.Render("settings"),
		"",
		kv("config dir", m.bundle.Dir),
		kv("version", "wmdep "+Version),
		kv("provider", providerMode(m.Demo)),
		kv("ssh timeout", d.SSHTimeout.Std().String()),
		kv("poll interval", d.PollInterval.Std().String()),
		kv("log tail", itoa(d.LogTail)),
		"",
		th.paneTitle.Render("files"),
		th.faint.Render("  config.yaml  hosts.yaml  pipelines.yaml  environments/"),
		"",
		th.faint.Render("edit the YAML by hand or from the Deployments/Servers screens."),
		th.faint.Render("esc back"),
	}
	box := m.box("", lines, min(m.width-4, 78), len(lines)+2, false)
	return centered(m.width, m.height, box)
}

func providerMode(demo bool) string {
	if demo {
		return "in-memory (--demo)"
	}
	return "SSH"
}

func centered(w, h int, s string) string {
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s)
}

// handleDeployLogKey closes the deploy output pane.
func (m *Model) handleDeployLogKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || msg.String() == "q" || msg.String() == "enter" {
		m.screen = m.returnTo
		m.rebuild()
	}
	return m, nil
}

func (m *Model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || msg.String() == "q" {
		m.screen = m.returnTo
	}
	return m, nil
}

// activityRows renders the last runs for the dashboard.
func (m *Model) activityRows() []string {
	th := m.th
	runs := m.Runs
	if runs == nil {
		return []string{th.faint.Render("no runs yet")}
	}
	recent := runs.Recent(4)
	if len(recent) == 0 {
		return []string{th.faint.Render("no runs yet — deploy with d d")}
	}
	var rows []string
	for i := len(recent) - 1; i >= 0; i-- {
		r := recent[i]
		state := th.ok.Render("● ok")
		if !r.OK {
			state = th.err.Render("✕ failed")
		}
		rows = append(rows, state+"  "+th.bold.Render(pad(r.App, 12))+
			th.dim.Render(pad(r.Env+" · "+r.Host, 20))+
			th.faint.Render(r.Time.Format("15:04")+"  "+r.Duration))
	}
	return rows
}
