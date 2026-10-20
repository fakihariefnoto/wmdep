package ui

import (
	"sort"
	"strings"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
)

type treeRowKind int

const (
	treeEnv treeRowKind = iota
	treeHost
)

// treeRow is one visible line in the host pane: either an environment header
// or a host inside it.
type treeRow struct {
	kind  treeRowKind
	env   string
	host  string
	apps  int
	state app.State
}

// hostTree is the left pane: environments, and the hosts inside them. It is
// rendered by hand rather than with bubbles/tree because the row content (a
// state dot plus counts) is not what that component models.
type hostTree struct {
	rows      []treeRow
	cursor    int
	offset    int
	collapsed map[string]bool
	width     int
	height    int

	// scope limits the tree to one environment, and to one host inside it.
	// The dashboard sets it when a card is opened.
	scopeEnv  string
	scopeHost string
}

// setScope narrows the tree. Empty values mean "no scope".
func (t *hostTree) setScope(env, host string) {
	t.scopeEnv, t.scopeHost = env, host
	t.cursor = 0
	t.offset = 0
}

func newHostTree() *hostTree {
	return &hostTree{collapsed: map[string]bool{}}
}

// rebuild recomputes the visible rows from the bundle and current statuses,
// preserving the cursor by (env, host) identity where possible.
func (t *hostTree) rebuild(b *config.Bundle, status map[app.Key]app.Status) {
	type key struct {
		env, host string
	}
	prev := t.selectedKey()

	statesFor := func(env, host string) (int, []app.State) {
		var states []app.State
		n := 0
		for _, a := range b.Apps {
			if a.Environment != env {
				continue
			}
			if host != "" && a.Host != host {
				continue
			}
			n++
			if st, ok := status[a.Key()]; ok {
				states = append(states, st.State)
			} else {
				states = append(states, app.StateUnknown)
			}
		}
		return n, states
	}

	rows := make([]treeRow, 0, len(b.Envs)*2)
	for _, env := range b.Envs {
		if t.scopeEnv != "" && env.Name != t.scopeEnv {
			continue
		}
		// A host-scoped view does not need the environment header: the
		// breadcrumb already says which environment we are in.
		if t.scopeHost == "" {
			n, states := statesFor(env.Name, "")
			rows = append(rows, treeRow{
				kind: treeEnv, env: env.Name, apps: n, state: worstState(states),
			})
		}
		if t.collapsed[env.Name] {
			continue
		}
		hosts := append([]string(nil), env.Hosts...)
		sort.Strings(hosts)
		for _, h := range hosts {
			if t.scopeHost != "" && h != t.scopeHost {
				continue
			}
			hn, hstates := statesFor(env.Name, h)
			rows = append(rows, treeRow{
				kind: treeHost, env: env.Name, host: h, apps: hn, state: worstState(hstates),
			})
		}
	}
	t.rows = rows

	// When scoped to one host, the cursor must start on that host row: the
	// manage screen derives its table filter from the selection, and landing on
	// the environment row would show every app in the environment.
	if t.scopeHost != "" {
		for i, r := range rows {
			if r.kind == treeHost && r.host == t.scopeHost && r.env == t.scopeEnv {
				t.cursor = i
				t.offset = 0
				return
			}
		}
	}

	// Restore cursor: prefer the exact row, else the same environment.
	t.cursor = clampIndex(t.cursor, len(rows))
	if prev != (key{}) {
		for i, r := range rows {
			if r.env == prev.env && r.host == prev.host {
				t.cursor = i
				return
			}
		}
	}
}

func (t *hostTree) selectedKey() (k struct {
	env, host string
}) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return
	}
	r := t.rows[t.cursor]
	return struct{ env, host string }{r.env, r.host}
}

// selected returns the filter implied by the cursor: an environment, and a
// host when a host row is selected.
func (t *hostTree) selected() (env, host string, ok bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return "", "", false
	}
	r := t.rows[t.cursor]
	return r.env, r.host, true
}

func (t *hostTree) move(delta int) {
	t.cursor = clampIndex(t.cursor+delta, len(t.rows))
	t.ensureVisible()
}

func (t *hostTree) toTop() {
	t.cursor = 0
	t.ensureVisible()
}

func (t *hostTree) toBottom() {
	t.cursor = len(t.rows) - 1
	t.ensureVisible()
}

// toggle collapses or expands the selected environment. On a host row it jumps
// focus into the app list, which is what an operator means by "select this
// host".
func (t *hostTree) toggle() bool {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return false
	}
	r := t.rows[t.cursor]
	if r.kind != treeEnv {
		return false
	}
	t.collapsed[r.env] = !t.collapsed[r.env]
	return true
}

// collapse and expand are explicit, so h/l do not depend on current state.
func (t *hostTree) collapse() {
	if r, ok := t.row(); ok && r.kind == treeEnv {
		t.collapsed[r.env] = true
	}
}

func (t *hostTree) expand() {
	if r, ok := t.row(); ok && r.kind == treeEnv {
		t.collapsed[r.env] = false
	}
}

func (t *hostTree) row() (treeRow, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return treeRow{}, false
	}
	return t.rows[t.cursor], true
}

func (t *hostTree) ensureVisible() {
	if t.height <= 0 {
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// lines renders the pane content.
func (t *hostTree) lines(th theme, focused bool) []string {
	out := make([]string, 0, len(t.rows))
	end := min(len(t.rows), t.offset+t.height)
	for i := t.offset; i < end; i++ {
		r := t.rows[i]
		out = append(out, t.renderRow(th, r, i == t.cursor && focused, i == t.cursor))
	}
	return out
}

func (t *hostTree) renderRow(th theme, r treeRow, focused, isCursor bool) string {
	glyph := th.stateStyle(r.state).Render(stateGlyph(r.state))

	var body string
	if r.kind == treeEnv {
		marker := "▾"
		if t.collapsed[r.env] {
			marker = "▸"
		}
		body = marker + " " + th.bold.Render(r.env)
	} else {
		body = "  " + th.dim.Render(r.host)
	}

	count := th.faint.Render(itoa(r.apps))
	line := glyph + " " + body

	// Right-align the app count by measuring the visible width.
	gap := t.width - lipglossWidth(line) - lipglossWidth(count) - 1
	if gap < 1 {
		gap = 1
	}
	line += strings.Repeat(" ", gap) + count

	switch {
	case focused:
		return th.selected.Render(line)
	case isCursor:
		return th.accent.Render(line)
	default:
		return line
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func clampIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
