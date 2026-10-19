package ui

import (
	"strings"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// maxLogLines caps the in-memory log buffer per stream. Real streaming arrives
// in M4; this pane already renders exactly what that work will feed it.
const maxLogLines = 2000

// logPane is the bottom pane: a tail of the selected app's logs.
type logPane struct {
	key    app.Key
	hasKey bool
	lines  []string
	follow bool
	err    string
	width  int
	height int
}

func newLogPane() *logPane {
	return &logPane{follow: true}
}

func (l *logPane) reset(key app.Key) {
	l.key = key
	l.hasKey = true
	l.lines = nil
	l.err = ""
	l.follow = true
}

func (l *logPane) append(line string) {
	l.lines = append(l.lines, line)
	if len(l.lines) > maxLogLines {
		l.lines = append([]string(nil), l.lines[len(l.lines)-maxLogLines:]...)
	}
}

// title is the pane caption, e.g. "logs · prod/web-1/api · following".
func (l *logPane) title(th theme) string {
	if !l.hasKey {
		return "logs"
	}
	head := "logs · " + l.key.String()
	if l.err != "" {
		return head + " · " + th.err.Render(l.err)
	}
	if l.follow {
		return head + " · " + th.ok.Render("following")
	}
	return head + " · " + th.dim.Render("paused")
}

// lines renders the visible tail.
func (l *logPane) view(th theme) []string {
	if !l.hasKey {
		return []string{th.faint.Render("select an app to tail its logs")}
	}
	if len(l.lines) == 0 {
		if l.err != "" {
			return []string{th.err.Render(l.err)}
		}
		return []string{th.faint.Render("waiting for output…")}
	}

	start := max(0, len(l.lines)-l.height)
	tail := l.lines[start:]
	out := make([]string, 0, len(tail))
	for _, raw := range tail {
		out = append(out, colourLogLine(th, raw))
	}
	return out
}

// colourLogLine tints a line by its level word, which is what makes a log pane
// scannable rather than a wall of text.
func colourLogLine(th theme, line string) string {
	switch {
	case strings.Contains(line, " ERROR ") || strings.Contains(line, " FATA"):
		return th.err.Render(line)
	case strings.Contains(line, " WARN "):
		return th.warn.Render(line)
	case strings.Contains(line, " DEBUG "):
		return th.faint.Render(line)
	default:
		return line
	}
}
