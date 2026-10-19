package ui

import (
	"context"
	"os"
	"path"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// Filer lists and downloads remote files. The CLI wires it to the SSH pool.
type Filer interface {
	ReadDir(ctx context.Context, h app.Host, dir string) ([]os.FileInfo, error)
	Download(ctx context.Context, h app.Host, remote, local string) error
}

type filesState struct {
	host    app.Host
	path    string
	entries []os.FileInfo
	cursor  int
	loading bool
	err     string
}

type filesLoadedMsg struct {
	entries []os.FileInfo
	err     error
}

func (m *Model) openFiles(hostID string) tea.Cmd {
	h, ok := m.bundle.Hosts[hostID]
	if !ok {
		m.setFlash("unknown host " + hostID)
		return nil
	}
	if m.Filer == nil {
		m.setFlash("files need a live SSH connection (run without --demo)")
		return nil
	}
	m.screen = screenFiles
	m.files = filesState{host: h, path: ".", loading: true}
	return m.loadFiles(".")
}

func (m *Model) loadFiles(dir string) tea.Cmd {
	filer := m.Filer
	h := m.files.host
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), m.bundle.Defaults().SSHTimeout.Std()*2)
		defer cancel()
		entries, err := filer.ReadDir(ctx, h, dir)
		return filesLoadedMsg{entries: entries, err: err}
	}
}

func (m *Model) handleFilesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.files.loading {
		if msg.String() == "esc" {
			m.screen = m.returnTo
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.screen = m.returnTo
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.files.cursor = clampIndex(m.files.cursor-1, len(m.files.entries))
	case "down", "j":
		m.files.cursor = clampIndex(m.files.cursor+1, len(m.files.entries))
	case "backspace", "h":
		parent := path.Dir(m.files.path)
		m.files.loading = true
		return m, m.loadFiles(parent)
	case "enter", "l":
		if m.files.cursor < len(m.files.entries) && m.files.entries[m.files.cursor].IsDir() {
			next := path.Join(m.files.path, m.files.entries[m.files.cursor].Name())
			m.files.loading = true
			return m, m.loadFiles(next)
		}
	case "d":
		if m.files.cursor >= len(m.files.entries) {
			return m, nil
		}
		entry := m.files.entries[m.files.cursor]
		if entry.IsDir() {
			m.setFlash("select a file to download")
			return m, nil
		}
		remote := path.Join(m.files.path, entry.Name())
		local := entry.Name()
		filer := m.Filer
		h := m.files.host
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), m.bundle.Defaults().SSHTimeout.Std()*10)
			defer cancel()
			return filesDownloadMsg{name: local, err: filer.Download(ctx, h, remote, local)}
		}
	}
	return m, nil
}

type filesDownloadMsg struct {
	name string
	err  error
}

func (m *Model) filesView() string {
	th := m.th
	f := &m.files
	header := th.title.Render("wmdep") + th.faint.Render("  › ") + th.accent.Render("files") +
		th.dim.Render("  ·  "+f.host.ID+"  ·  "+f.path)
	if f.loading {
		return strings.Join([]string{header, "", th.dim.Render("listing…"), "", th.faint.Render("esc back")}, "\n")
	}
	lines := []string{th.paneTitle.Render("   " + pad("NAME", 36) + pad("SIZE", 10) + "MODE")}
	if f.err != "" {
		lines = append(lines, "", th.err.Render(f.err))
	}
	for i, e := range f.entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		size := "-"
		if !e.IsDir() {
			size = humanBytes(e.Size())
		}
		plain := "   " + pad(name, 36) + pad(size, 10) + e.Mode().String()
		if i == f.cursor {
			lines = append(lines, th.selected.Render(plain))
			continue
		}
		lines = append(lines, "   "+th.bold.Render(pad(name, 36))+th.dim.Render(pad(size, 10))+th.faint.Render(e.Mode().String()))
	}
	body := m.panel(clip(lines, m.width-4, max(1, m.height-3)), m.width, m.height-2)
	footer := th.dim.Render("enter open · backspace up · d download to cwd · esc back")
	return strings.Join([]string{header, body, footer}, "\n")
}
