package ui

import (
	"time"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// appTable is the middle pane: the list of apps matching the current filter,
// with one row of live status each.
type appTable struct {
	tbl    table.Model
	apps   []app.App
	status map[app.Key]app.Status
	now    time.Time
	th     theme
	plan   colPlan
	width  int
	height int
}

func newAppTable(th theme) *appTable {
	t := &appTable{th: th}
	cols, plan := columns(80)
	t.plan = plan
	t.tbl = table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithStyles(table.DefaultStyles()),
	)
	// Cells are already styled by row(); the table's own styles only need to
	// supply padding (on both header and cells, so columns line up) and the
	// selection emphasis, or colors are applied twice.
	selBG := lipgloss.Color("236")
	if !th.dark {
		selBG = lipgloss.Color("254")
	}
	t.tbl.SetStyles(table.Styles{
		Header:   th.paneTitle.Padding(0, 1),
		Cell:     lipgloss.NewStyle().Padding(0, 1),
		Selected: lipgloss.NewStyle().Bold(true).Background(selBG),
	})
	return t
}

// colPlan records which optional columns fit at a given width. APP and STATE
// are always present; the rest are added as room allows, so a narrow pane loses
// the least useful columns instead of truncating the row.
type colPlan struct {
	host bool
	up   bool
	cpu  bool
	mem  bool
}

// columns distributes the available width. bubbles/table pads every cell with
// one space on each side, so each column costs its width plus two.
//
// APP and STATE are always present. HOST, UP, CPU, and MEM are added greedily
// while they fit, so a narrow pane loses the least useful column one at a time
// instead of truncating the row.
func columns(w int) ([]table.Column, colPlan) {
	appW, hostW, stateW, upW, cpuW, memW := 8, 12, 10, 6, 5, 6
	const pad = 2

	plan := colPlan{}
	total := appW + stateW + 2*pad // APP + STATE
	if total+hostW+pad <= w {
		plan.host = true
		total += hostW + pad
	}
	if total+upW+pad <= w {
		plan.up = true
		total += upW + pad
	}
	if total+cpuW+pad <= w {
		plan.cpu = true
		total += cpuW + pad
	}
	if total+memW+pad <= w {
		plan.mem = true
		total += memW + pad
	}

	// Any remaining width goes to the app name, which is the column operators
	// scan first.
	appW += max(0, w-total)
	if appW < 6 {
		appW = 6
	}

	cols := []table.Column{{Title: "APP", Width: appW}}
	if plan.host {
		cols = append(cols, table.Column{Title: "HOST", Width: hostW})
	}
	cols = append(cols, table.Column{Title: "STATE", Width: stateW})
	if plan.up {
		cols = append(cols, table.Column{Title: "UP", Width: upW})
	}
	if plan.cpu {
		cols = append(cols, table.Column{Title: "CPU", Width: cpuW})
	}
	if plan.mem {
		cols = append(cols, table.Column{Title: "MEM", Width: memW})
	}
	return cols, plan
}

func (a *appTable) setSize(w, h int) {
	cols, plan := columns(w)
	a.width, a.height = w, h
	a.plan = plan

	// Rows carry one cell per column. Rebuilding the column set while rows from
	// the previous set are live would panic in the table's renderer, so the
	// rows are cleared and rebuilt from the cached data below.
	a.tbl.SetRows(nil)
	a.tbl.SetColumns(cols)
	a.tbl.SetWidth(w)
	a.tbl.SetHeight(h)
	if a.status != nil {
		a.build(a.tbl.Cursor())
	}
}

// setApps replaces the rows, keeping the previously selected app selected when
// it is still present.
func (a *appTable) setApps(apps []app.App, status map[app.Key]app.Status, now time.Time, th theme) {
	cursor := 0
	if prev, ok := a.selected(); ok {
		for i, ap := range apps {
			if ap.Key() == prev.Key() {
				cursor = i
				break
			}
		}
	}
	a.apps, a.status, a.now, a.th = apps, status, now, th
	a.build(cursor)
}

// build renders every row for the current plan and restores the cursor.
func (a *appTable) build(cursor int) {
	rows := make([]table.Row, 0, len(a.apps))
	for _, ap := range a.apps {
		rows = append(rows, a.row(ap, a.status[ap.Key()], a.now, a.th))
	}
	a.tbl.SetRows(rows)
	if len(rows) > 0 {
		a.tbl.SetCursor(clampIndex(cursor, len(rows)))
	}
}

func (a *appTable) row(ap app.App, st app.Status, now time.Time, th theme) table.Row {
	state := st.State
	if state == "" {
		state = app.StateUnknown
	}
	stateCell := th.stateStyle(state).Render(stateGlyph(state) + " " + string(state))
	if st.Err != nil {
		stateCell = th.err.Render("✕ unreachable")
	}

	up := "-"
	if state == app.StateRunning {
		up = humanUptime(st.Uptime(now))
	}

	row := table.Row{th.bold.Render(ap.DisplayName())}
	if a.plan.host {
		row = append(row, th.dim.Render(ap.Host))
	}
	row = append(row, stateCell)
	if a.plan.up {
		row = append(row, th.dim.Render(up))
	}
	if a.plan.cpu {
		row = append(row, th.dim.Render(pct(st.CPU)))
	}
	if a.plan.mem {
		row = append(row, th.dim.Render(humanBytes(st.MemBytes)))
	}
	return row
}

// selected returns the app under the cursor.
func (a *appTable) selected() (app.App, bool) {
	i := a.tbl.Cursor()
	if i < 0 || i >= len(a.apps) {
		return app.App{}, false
	}
	return a.apps[i], true
}

func (a *appTable) move(delta int) {
	switch {
	case delta < 0:
		a.tbl.MoveUp(-delta)
	case delta > 0:
		a.tbl.MoveDown(delta)
	}
}

func (a *appTable) toTop()    { a.tbl.GotoTop() }
func (a *appTable) toBottom() { a.tbl.GotoBottom() }

func (a *appTable) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	a.tbl, cmd = a.tbl.Update(msg)
	return cmd
}

func (a *appTable) view() string { return a.tbl.View() }
