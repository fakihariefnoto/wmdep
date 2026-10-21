package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// Palette. Truecolor hex is used so state colors are distinct; lipgloss
// downgrades them for 256/16-color terminals. Light mode gets darker variants.
var (
	colAccent   = lipgloss.Color("#2563EB") // electric blue
	colAccent2  = lipgloss.Color("#22D3EE") // cyan
	colDim      = lipgloss.Color("#94A3C8")
	colFaint    = lipgloss.Color("#5B6B8C")
	colOK       = lipgloss.Color("#22C55E")
	colWarn     = lipgloss.Color("#F59E0B")
	colErr      = lipgloss.Color("#EF4444")
	colText     = lipgloss.Color("#E8EEFF")
	colSurface  = lipgloss.Color("#141B30")
	colSurface2 = lipgloss.Color("#1C2540")
)

// theme bundles every style the UI renders with. Keeping them in one struct
// means a light theme is a second constructor, not a rewrite.
type theme struct {
	dark bool

	title     lipgloss.Style
	subtitle  lipgloss.Style
	dim       lipgloss.Style
	faint     lipgloss.Style
	bold      lipgloss.Style
	accent    lipgloss.Style
	selected  lipgloss.Style
	statusBar lipgloss.Style

	pane        lipgloss.Style
	paneFocused lipgloss.Style
	paneTitle   lipgloss.Style
	paneTitleHi lipgloss.Style

	ok      lipgloss.Style
	warn    lipgloss.Style
	err     lipgloss.Style
	unknown lipgloss.Style

	key     lipgloss.Style
	keyDesc lipgloss.Style

	cardBG lipgloss.Style
}

func newTheme(dark bool) theme {
	t := theme{dark: dark}

	accent, accent2 := colAccent, colAccent2
	dim, faint, text := colDim, colFaint, colText
	ok, warn, errc := colOK, colWarn, colErr
	surface := colSurface
	if !dark {
		accent = lipgloss.Color("#1D4ED8")
		accent2 = lipgloss.Color("#0E7490")
		dim = lipgloss.Color("#4B5563")
		faint = lipgloss.Color("#9CA3AF")
		text = lipgloss.Color("#0B1020")
		ok = lipgloss.Color("#15803D")
		warn = lipgloss.Color("#B45309")
		errc = lipgloss.Color("#B91C1C")
		surface = lipgloss.Color("#EEF2FF")
	}

	t.title = lipgloss.NewStyle().Bold(true).Foreground(accent2)
	t.subtitle = lipgloss.NewStyle().Foreground(dim)
	t.dim = lipgloss.NewStyle().Foreground(dim)
	t.faint = lipgloss.NewStyle().Foreground(faint)
	t.bold = lipgloss.NewStyle().Bold(true).Foreground(text)
	t.accent = lipgloss.NewStyle().Foreground(accent)
	t.selected = lipgloss.NewStyle().Bold(true).Foreground(accent2)
	t.statusBar = lipgloss.NewStyle().Foreground(dim)

	t.pane = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(faint).
		Padding(0, 1)
	t.paneFocused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1)
	t.paneTitle = lipgloss.NewStyle().Bold(true).Foreground(dim)
	t.paneTitleHi = lipgloss.NewStyle().Bold(true).Foreground(accent2)

	t.ok = lipgloss.NewStyle().Foreground(ok)
	t.warn = lipgloss.NewStyle().Foreground(warn)
	t.err = lipgloss.NewStyle().Foreground(errc)
	t.unknown = lipgloss.NewStyle().Foreground(faint)
	t.cardBG = lipgloss.NewStyle().Background(surface)

	t.key = lipgloss.NewStyle().Foreground(accent2)
	t.keyDesc = lipgloss.NewStyle().Foreground(dim)
	return t
}

// stateGlyph is the one-character state indicator used wherever a whole column
// is not available.
func stateGlyph(s app.State) string {
	switch s {
	case app.StateRunning:
		return "●"
	case app.StateStopped:
		return "○"
	case app.StateDegraded:
		return "▲"
	case app.StateFailed:
		return "✕"
	default:
		return "?"
	}
}

func (t theme) stateStyle(s app.State) lipgloss.Style {
	switch s {
	case app.StateRunning:
		return t.ok
	case app.StateStopped:
		return t.dim
	case app.StateDegraded:
		return t.warn
	case app.StateFailed:
		return t.err
	default:
		return t.unknown
	}
}

// worstState aggregates a host's apps into the single state its tree row
// shows. Failure beats everything, then degraded, then running.
func worstState(states []app.State) app.State {
	if len(states) == 0 {
		return app.StateUnknown
	}
	rank := map[app.State]int{
		app.StateFailed:   4,
		app.StateDegraded: 3,
		app.StateUnknown:  2,
		app.StateRunning:  1,
		app.StateStopped:  0,
	}
	worst := states[0]
	for _, s := range states[1:] {
		if rank[s] > rank[worst] {
			worst = s
		}
	}
	return worst
}

// humanUptime renders a duration the way an operator reads it: 3d4h, 2h10m,
// 42s.
func humanUptime(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// humanBytes renders memory sizes compactly.
func humanBytes(n int64) string {
	if n <= 0 {
		return "-"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1fG", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.0fM", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.0fK", float64(n)/kb)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// pct renders a percentage, or "-" when the provider has no number.
func pct(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", v)
}

// truncate cuts a plain (unstyled) string to n runes, appending an ellipsis.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// pad right-pads a plain string to width n.
func pad(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// clip truncates each line to w visible cells and returns exactly h lines, so
// a pane can never overflow its box no matter what content arrives.
func clip(lines []string, w, h int) []string {
	if h <= 0 || w <= 0 {
		return nil
	}
	out := make([]string, 0, h)
	for _, l := range lines {
		if len(out) >= h {
			break
		}
		out = append(out, ansi.Truncate(l, w, "…"))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}
