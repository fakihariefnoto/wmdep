package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
)

// Deployer runs a pipeline for an app. The CLI wires the real engine; tests can
// substitute a function.
type Deployer interface {
	Deploy(ctx context.Context, a app.App, h app.Host, p config.Pipeline) (ok bool, output string, err error)
}

type deployDoneMsg struct {
	app  app.App
	ok   bool
	out  string
	err  error
	when time.Time
}

// startDeploy arms on the first press and runs on the second, so a stray key
// cannot restart production.
func (m *Model) startDeploy() tea.Cmd {
	a, h, ok := m.selectedApp()
	if !ok {
		return nil
	}
	return m.startDeployFor(a, h)
}

func (m *Model) startDeployFor(a app.App, h app.Host) tea.Cmd {
	if a.Deploy == "" {
		m.setFlash("no pipeline assigned to " + a.ID + " — edit it and pick one")
		return nil
	}
	if m.Deployer == nil {
		m.setFlash("deploys need a live SSH connection (run without --demo)")
		return nil
	}
	p, ok := m.bundle.Pipelines[a.Deploy]
	if !ok {
		m.setFlash("pipeline " + a.Deploy + " not found")
		return nil
	}

	keyv := a.Key().String()
	if m.pendingDeploy != keyv {
		m.pendingDeploy = keyv
		m.setFlash("press d again to deploy " + a.ID + " via " + a.Deploy)
		return nil
	}
	m.pendingDeploy = ""

	dep := m.Deployer
	m.deployStarted = time.Now()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		ok, out, err := dep.Deploy(ctx, a, h, p)
		return deployDoneMsg{app: a, ok: ok, out: out, err: err, when: time.Now()}
	}
}

// deploySummary condenses engine output for the status bar.
func deploySummary(msg deployDoneMsg) string {
	if msg.ok {
		return fmt.Sprintf("deployed %s@%s ok", msg.app.ID, msg.app.Host)
	}
	detail := ""
	if msg.err != nil {
		detail = ": " + msg.err.Error()
	}
	lines := strings.Split(strings.TrimSpace(msg.out), "\n")
	if len(lines) > 0 && lines[0] != "" {
		detail = ": " + lines[len(lines)-1]
	}
	return "deploy failed for " + msg.app.ID + detail
}
