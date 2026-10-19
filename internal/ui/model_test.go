package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
	"github.com/fakihariefnoto/wmdep/internal/provider"
	"github.com/fakihariefnoto/wmdep/internal/watch"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	b, diags := config.Load("../config/testdata/good", config.Options{})
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("fixture has errors: %s", d.Error())
		}
	}
	reg := provider.NewRegistry()
	reg.RegisterAll(provider.NewFake())
	return New(b, reg)
}

// testModel starts in the workspace, which is what most of the M1 tests cover.
func testModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.screen = screenManage
	m.tree.setScope("", "")
	m.rebuild()
	return m
}

func send(t *testing.T, m *Model, msg tea.Msg) *Model {
	t.Helper()
	next, _ := m.Update(msg)
	nm, ok := next.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", next)
	}
	return nm
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	default:
		r := []rune(s)[0]
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

// viewText is the rendered screen with styling stripped by the caller; tests
// only ever assert on plain text, so escape codes are removed here.
func viewText(m *Model) string {
	return stripANSI(m.render())
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm' || r == 'K'):
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestRenderAtSize(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	got := viewText(m)
	for _, want := range []string{"wmdep", "hosts", "apps", "api", "worker", "redis", "logs", "prod", "staging"} {
		if !strings.Contains(got, want) {
			t.Errorf("view missing %q\n--- view ---\n%s", want, got)
		}
	}
	if h := strings.Count(got, "\n") + 1; h > 40 {
		t.Errorf("view is %d lines, want at most 40", h)
	}
}

func TestTerminalTooSmall(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})
	if got := viewText(m); !strings.Contains(got, "terminal too small") {
		t.Errorf("want the too-small notice, got:\n%s", got)
	}
}

func TestResizeKeepsPanesInsideTerminal(t *testing.T) {
	m := testModel(t)
	for _, size := range []struct{ w, h int }{
		{80, 24}, {120, 40}, {200, 50}, {100, 20}, {64, 16}, {300, 60},
	} {
		m = send(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		lines := strings.Split(viewText(m), "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: %d lines, want <= %d", size.w, size.h, len(lines), size.h)
		}
		for i, l := range lines {
			// lipgloss measures the styled string; a line wider than the
			// terminal means a pane escaped its box.
			if w := len([]rune(l)); w > size.w {
				t.Errorf("%dx%d: line %d is %d cells wide: %q", size.w, size.h, i, w, l)
			}
		}
	}
}

func TestFocusCycles(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.focus != focusTree {
		t.Fatalf("focus = %v, want tree", m.focus)
	}
	m = send(t, m, press("tab"))
	if m.focus != focusTable {
		t.Fatalf("focus = %v, want table", m.focus)
	}
	m = send(t, m, press("tab"))
	if m.focus != focusLogs {
		t.Fatalf("focus = %v, want logs", m.focus)
	}
	m = send(t, m, press("tab"))
	if m.focus != focusTree {
		t.Fatalf("focus = %v, want wrap to tree", m.focus)
	}
	m = send(t, m, press("shift+tab"))
	if m.focus != focusLogs {
		t.Fatalf("focus = %v, want wrap back to logs", m.focus)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(t, m, press("?"))
	got := viewText(m)
	if !strings.Contains(got, "keys") || !strings.Contains(got, "quit") {
		t.Errorf("help overlay missing content:\n%s", got)
	}
	m = send(t, m, press("esc"))
	if strings.Contains(viewText(m), "esc or ? to close") {
		t.Error("help overlay did not close on esc")
	}
}

func TestStatusUpdateReachesTableAndDetail(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 45})

	target := m.targets()[0]
	started := time.Now().Add(-90 * time.Minute)
	m = send(t, m, pollResultMsg{res: watch.Result{
		Target: target,
		Status: app.Status{
			State:    app.StateRunning,
			Detail:   "active (running)",
			Since:    started,
			CPU:      12.5,
			MemBytes: 340 << 20,
			At:       time.Now(),
		},
	}})
	m = send(t, m, pollDoneMsg{})

	got := viewText(m)
	if !strings.Contains(got, "running") {
		t.Errorf("view missing the running state:\n%s", got)
	}
	if !strings.Contains(got, "340M") {
		t.Errorf("view missing the memory figure:\n%s", got)
	}

	detail := stripANSI(strings.Join(m.detail, "\n"))
	if !strings.Contains(detail, target.App.ID) {
		t.Errorf("detail pane should describe %s:\n%s", target.App.ID, detail)
	}
	if !strings.Contains(detail, "running") {
		t.Errorf("detail pane missing state:\n%s", detail)
	}
}

func TestUnreachableHostIsSurfaced(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 45})

	target := m.targets()[0]
	m = send(t, m, pollResultMsg{res: watch.Result{
		Target: target,
		Status: app.Status{State: app.StateUnknown},
		Err:    errString("dial tcp: connection refused"),
	}})
	m = send(t, m, pollDoneMsg{})

	if got := viewText(m); !strings.Contains(got, "unreachable") {
		t.Errorf("view should mark the app unreachable:\n%s", got)
	}
	detail := stripANSI(strings.Join(m.detail, "\n"))
	if !strings.Contains(detail, "connection refused") {
		t.Errorf("detail pane should show the error:\n%s", detail)
	}
}

func TestHostSelectionFiltersApps(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// Find the tree row for web-2 and select it.
	idx := -1
	for i, r := range m.tree.rows {
		if r.kind == treeHost && r.host == "web-2" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("web-2 not found in the tree")
	}
	m.tree.cursor = idx
	m.rebuild()

	for _, a := range m.table.apps {
		if a.Host != "web-2" {
			t.Errorf("host filter leaked %s@%s", a.ID, a.Host)
		}
	}
	if len(m.table.apps) == 0 {
		t.Fatal("host filter dropped every app")
	}
}

func TestFilterNarrowsApps(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m.filter = "redis"
	m.rebuild()
	if len(m.table.apps) != 1 || m.table.apps[0].ID != "redis" {
		t.Fatalf("filter redis matched %v", m.table.apps)
	}

	m.filter = "no-such-app"
	m.rebuild()
	if len(m.table.apps) != 0 {
		t.Fatalf("filter should match nothing, got %v", m.table.apps)
	}
}

func TestFilterModeCapturesTyping(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m = send(t, m, press("/"))
	if !m.filtering {
		t.Fatal("/ should enter filter mode")
	}
	for _, r := range "api" {
		m = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.filter != "api" {
		t.Fatalf("filter = %q, want api", m.filter)
	}
	// q must be typed into the filter, not quit the program.
	m = send(t, m, press("q"))
	if m.filter != "apiq" {
		t.Fatalf("filter = %q, want apiq", m.filter)
	}
	m = send(t, m, press("esc"))
	if m.filtering || m.filter != "" {
		t.Fatalf("esc should leave filter mode with an empty filter, got %q", m.filter)
	}
}

func TestLogPaneGetsLines(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	a, _, ok := m.selectedApp()
	if !ok {
		t.Fatal("no app selected at startup")
	}
	m.logs.reset(a.Key())
	for _, l := range []string{
		"12:00:01 INFO  request handled path=/health",
		"12:00:02 WARN  pool saturated",
		"12:00:03 ERROR upstream gone",
	} {
		m.logs.append(l)
	}

	got := viewText(m)
	for _, want := range []string{"request handled", "pool saturated", "upstream gone"} {
		if !strings.Contains(got, want) {
			t.Errorf("log pane missing %q:\n%s", want, got)
		}
	}
}

func TestToggleFollow(t *testing.T) {
	m := testModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if !m.logs.follow {
		t.Fatal("logs should follow by default")
	}
	m.focus = focusLogs
	m = send(t, m, press("f"))
	if m.logs.follow {
		t.Fatal("f should pause following")
	}
}

func TestClampAndHelpers(t *testing.T) {
	if got := clampInt(5, 0, 3); got != 3 {
		t.Errorf("clampInt(5,0,3) = %d, want 3", got)
	}
	if got := clampInt(-1, 0, 3); got != 0 {
		t.Errorf("clampInt(-1,0,3) = %d, want 0", got)
	}
	if got := humanUptime(90 * time.Minute); got != "1h30m" {
		t.Errorf("humanUptime(90m) = %q", got)
	}
	if got := humanUptime(50 * time.Hour); got != "2d2h" {
		t.Errorf("humanUptime(50h) = %q", got)
	}
	if got := humanBytes(340 << 20); got != "340M" {
		t.Errorf("humanBytes = %q", got)
	}
	if got := worstState([]app.State{app.StateRunning, app.StateDegraded, app.StateStopped}); got != app.StateDegraded {
		t.Errorf("worstState = %v, want degraded", got)
	}
	if got := truncate("abcdefgh", 5); got != "abcd…" {
		t.Errorf("truncate = %q", got)
	}
}

// errString is a tiny error type so tests can inject a failure without
// importing errors.
type errString string

func (e errString) Error() string { return string(e) }

func TestStartsOnSplash(t *testing.T) {
	m := newTestModel(t)
	if m.screen != screenSplash {
		t.Fatalf("screen = %v, want splash", m.screen)
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	got := viewText(m)
	for _, want := range []string{"Dashboard", "Deployments", "Servers", "Scan", "Settings", "Quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("splash missing %q:\n%s", want, got)
		}
	}
	if m.Init() == nil {
		t.Error("Init should still animate the spinner on the splash")
	}
}

func TestSplashEnterOpensDashboard(t *testing.T) {
	m := newTestModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = send(t, m, press("enter"))

	if m.screen != screenDashboard {
		t.Fatalf("screen = %v, want dashboard", m.screen)
	}
	got := viewText(m)
	for _, want := range []string{"dashboard", "OVERVIEW", "HOSTS", "ATTENTION", "prod", "staging", "web-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard missing %q:\n%s", want, got)
		}
	}
}

func TestSplashMenuScopesTheDashboard(t *testing.T) {
	cases := []struct {
		item    int
		scope   dashScope
		want    []string
		notWant []string
	}{
		{1, scopeDeployments, []string{"DEPLOYMENTS", "prod"}, []string{"HOSTS"}},
		{2, scopeServers, []string{"SERVERS", "HOSTS", "web-1"}, []string{"web-1 · web-2"}},
	}
	for _, tc := range cases {
		m := newTestModel(t)
		m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
		for range tc.item {
			m = send(t, m, press("j"))
		}
		m = send(t, m, press("enter"))
		if m.screen != screenDashboard || m.dash.scope != tc.scope {
			t.Fatalf("item %d: screen=%v scope=%v, want dashboard/%v", tc.item, m.screen, m.dash.scope, tc.scope)
		}
		got := viewText(m)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("item %d: missing %q:\n%s", tc.item, want, got)
			}
		}
		for _, bad := range tc.notWant {
			if strings.Contains(got, bad) {
				t.Errorf("item %d: should not contain %q", tc.item, bad)
			}
		}
	}
}

func TestDashboardCardOpensScopedManageAndEscReturns(t *testing.T) {
	m := newTestModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = send(t, m, press("enter")) // splash -> dashboard
	if m.screen != screenDashboard {
		t.Fatal("expected dashboard")
	}

	// The first focusable card is the first environment.
	m = send(t, m, press("enter"))
	if m.screen != screenManage {
		t.Fatalf("screen = %v, want manage", m.screen)
	}
	if m.scopeEnv != "prod" {
		t.Fatalf("scopeEnv = %q, want prod", m.scopeEnv)
	}
	for _, a := range m.table.apps {
		if a.Environment != "prod" {
			t.Errorf("scoped table leaked %s from %s", a.ID, a.Environment)
		}
	}
	if got := viewText(m); !strings.Contains(got, "dashboard › prod") {
		t.Errorf("manage header should show a breadcrumb:\n%s", got)
	}

	m = send(t, m, press("esc"))
	if m.screen != screenDashboard {
		t.Fatalf("esc should return to the dashboard, got %v", m.screen)
	}
	if m.scopeEnv != "prod" {
		t.Error("dashboard should preserve the scope on return")
	}
}

func TestDashboardFocusAndHelp(t *testing.T) {
	m := newTestModel(t)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = send(t, m, press("enter"))

	start := m.dash.cursor
	m = send(t, m, press("tab"))
	if m.dash.cursor == start {
		t.Error("tab should move the card cursor")
	}
	m = send(t, m, press("?"))
	if !m.showHelp {
		t.Error("? should open help on the dashboard")
	}
	m = send(t, m, press("esc"))
	if m.showHelp || m.screen != screenDashboard {
		t.Error("esc should close help before leaving the dashboard")
	}
	if got := viewText(m); !strings.Contains(got, "ATTENTION") {
		t.Errorf("dashboard should still render:\n%s", got)
	}
}

func TestWordmarkFitsEightyColumns(t *testing.T) {
	for i, line := range wordmark {
		if w := lipglossWidth(strings.TrimRight(line, " ")); w > 60 {
			t.Errorf("wordmark line %d is %d columns, want <= 60", i, w)
		}
	}
}
