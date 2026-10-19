package ui

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// detailLines renders the right pane: everything known about the selected app.
// Label values are padded so the pane reads as a small form.
func detailLines(a app.App, h app.Host, st app.Status, now time.Time, th theme) []string {
	kv := func(label, value string) string {
		if value == "" {
			value = "-"
		}
		return th.dim.Render(pad(label, 9)) + th.bold.Render(value)
	}
	kvStyled := func(label, value string, st lipgloss.Style) string {
		if value == "" {
			value = "-"
		}
		return th.dim.Render(pad(label, 9)) + st.Render(value)
	}

	state := st.State
	if state == "" {
		state = app.StateUnknown
	}

	out := []string{
		th.title.Render(a.ID),
		th.subtitle.Render(fmt.Sprintf("%s · %s · %s", a.Environment, a.Host, a.Kind)),
		"",
		kv("target", a.Target()),
	}

	if a.Workdir != "" {
		out = append(out, kv("workdir", a.Workdir))
	}
	if a.Deploy != "" {
		out = append(out, kv("deploy", a.Deploy))
	}
	if a.Health != nil && a.Health.Kind != app.HealthNone {
		out = append(out, kv("health", healthSummary(a.Health)))
	}
	if h.Auth == app.AuthPassword {
		out = append(out, kv("auth", "password (keyring)"))
	} else if h.Key != "" {
		out = append(out, kv("auth", string(h.Auth)+" "+h.Key))
	}
	if a.Tags != nil {
		out = append(out, kv("tags", joinTags(a.Tags)))
	}

	out = append(out, "", th.paneTitle.Render("status"))

	if st.Err != nil {
		out = append(out,
			kvStyled("state", "unreachable", th.err),
			kvStyled("error", st.ErrText(), th.err),
		)
		if !st.At.IsZero() {
			out = append(out, kv("checked", humanAgo(now, st.At)))
		}
		return out
	}

	out = append(out, kvStyled("state", string(state)+" ("+st.Detail+")", th.stateStyle(state)))
	out = append(out, kv("uptime", humanUptime(st.Uptime(now))))
	if !st.Since.IsZero() && state == app.StateRunning {
		out = append(out, kv("since", st.Since.Format("Mon 15:04")))
	}
	out = append(out, kv("cpu", pct(st.CPU)))
	out = append(out, kv("memory", humanBytes(st.MemBytes)))
	if !st.At.IsZero() {
		out = append(out, kv("observed", humanAgo(now, st.At)))
	}
	return out
}

// healthSummary renders a health check as a short human phrase.
func healthSummary(h *app.Health) string {
	switch h.Kind {
	case app.HealthHTTP:
		if h.Expect != "" {
			return fmt.Sprintf("http %s → %s", h.URL, h.Expect)
		}
		return "http " + h.URL
	case app.HealthCommand:
		return "cmd " + h.Cmd
	case app.HealthTCP:
		return "tcp " + h.Address
	default:
		return string(h.Kind)
	}
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}

// humanAgo renders "3s ago", "2m ago".
func humanAgo(now, then time.Time) string {
	d := now.Sub(then)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < 2*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
