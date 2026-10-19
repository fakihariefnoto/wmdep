package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/config"
)

// formField is one row in the editor. Select fields carry options and show the
// chosen option; text fields are edited in place.
type formField struct {
	label   string
	value   string
	options []string
	opt     int
	help    string
}

func (f formField) isSelect() bool { return len(f.options) > 0 }

func (f formField) display() string {
	if f.isSelect() {
		if f.opt >= 0 && f.opt < len(f.options) {
			return f.options[f.opt]
		}
		return ""
	}
	return f.value
}

// form is a small keyboard form: tab/arrows move, enter cycles a select, typing
// edits text. It is shared by the deployment and host editors.
type form struct {
	title      string
	kind       string // "deployment" | "host"
	fields     []formField
	cursor     int
	err        string
	originalID string
}

func (f *form) field(label string) *formField {
	for i := range f.fields {
		if f.fields[i].label == label {
			return &f.fields[i]
		}
	}
	return nil
}

func (f *form) value(label string) string {
	if fld := f.field(label); fld != nil {
		return fld.display()
	}
	return ""
}

func (f *form) set(label, value string) {
	fld := f.field(label)
	if fld == nil {
		return
	}
	if fld.isSelect() {
		for i, o := range fld.options {
			if o == value {
				fld.opt = i
				return
			}
		}
		return
	}
	fld.value = value
}

func (f *form) move(delta int) {
	f.cursor = clampIndex(f.cursor+delta, len(f.fields))
}

func (f *form) cycle(delta int) {
	if f.cursor < 0 || f.cursor >= len(f.fields) {
		return
	}
	fld := &f.fields[f.cursor]
	if !fld.isSelect() || len(fld.options) == 0 {
		return
	}
	fld.opt = ((fld.opt+delta)%len(fld.options) + len(fld.options)) % len(fld.options)
}

func (f *form) insert(text string) {
	if f.cursor < 0 || f.cursor >= len(f.fields) {
		return
	}
	fld := &f.fields[f.cursor]
	if fld.isSelect() {
		return
	}
	fld.value += text
}

func (f *form) backspace() {
	if f.cursor < 0 || f.cursor >= len(f.fields) {
		return
	}
	fld := &f.fields[f.cursor]
	if fld.isSelect() || fld.value == "" {
		return
	}
	r := []rune(fld.value)
	fld.value = string(r[:len(r)-1])
}

func (f *form) optionsFor(label string, options []string) {
	if fld := f.field(label); fld != nil {
		fld.options = options
		fld.opt = 0
	}
}

// deploymentFormFields builds the provider-aware field list.
func deploymentFormFields() []formField {
	return []formField{
		{label: "id", help: "unique within the environment"},
		{label: "env", options: nil, help: "environment file to write"},
		{label: "host", options: nil, help: "host from hosts.yaml"},
		{label: "provider", options: kindOptions(), help: "how the app is supervised"},
		{label: "target", help: "unit, container, project/service, process, or command"},
		{label: "workdir", help: "optional working directory"},
		{label: "health", options: []string{"none", "http", "tcp", "command"}, help: "optional probe"},
		{label: "health target", help: "URL, address, or command"},
		{label: "expect", help: "status code, stdout, or empty"},
		{label: "pipeline", options: []string{"none"}, help: "deploy recipe (pipelines.yaml)"},
		{label: "tags", help: "comma separated"},
	}
}

func kindOptions() []string {
	out := make([]string, 0, len(app.KindNames))
	for _, k := range app.KindNames {
		out = append(out, string(k))
	}
	return out
}

// newDeploymentForm starts an empty form with sensible defaults for the
// environment and host the operator was looking at.
func newDeploymentForm(b *config.Bundle, env, host string) form {
	f := form{title: "new deployment", kind: "deployment", fields: deploymentFormFields()}

	envs := b.EnvNames()
	f.optionsFor("env", envs)
	f.optionsFor("host", b.HostOrder)
	f.optionsFor("pipeline", append([]string{"none"}, pipelineNames(b)...))

	if env != "" {
		f.set("env", env)
	}
	if host != "" {
		f.set("host", host)
	}
	f.set("provider", string(app.KindSystemd))
	return f
}

func editDeploymentForm(b *config.Bundle, a app.App) form {
	f := newDeploymentForm(b, a.Environment, a.Host)
	f.title = "edit deployment"
	f.originalID = a.ID
	f.set("id", a.ID)
	f.set("provider", string(a.Kind))
	f.set("target", a.Target())
	f.set("workdir", a.Workdir)
	if a.Health != nil && a.Health.Kind != app.HealthNone {
		f.set("health", string(a.Health.Kind))
		switch a.Health.Kind {
		case app.HealthHTTP:
			f.set("health target", a.Health.URL)
		case app.HealthTCP:
			f.set("health target", a.Health.Address)
		case app.HealthCommand:
			f.set("health target", a.Health.Cmd)
		}
		f.set("expect", a.Health.Expect)
	}
	if a.Deploy != "" {
		f.set("pipeline", a.Deploy)
	}
	f.set("tags", strings.Join(a.Tags, ", "))
	return f
}

func duplicateDeploymentForm(b *config.Bundle, a app.App) form {
	f := editDeploymentForm(b, a)
	f.title = "duplicate deployment"
	f.originalID = ""
	f.set("id", a.ID+"-copy")
	return f
}

func pipelineNames(b *config.Bundle) []string {
	out := make([]string, 0, len(b.Pipelines))
	for name := range b.Pipelines {
		out = append(out, name)
	}
	return out
}

// deploymentSpec converts the form into a config spec plus its environment.
func (f *form) deploymentSpec() (string, config.AppSpec, error) {
	id := strings.TrimSpace(f.value("id"))
	env := f.value("env")
	host := f.value("host")
	provider := f.value("provider")
	target := strings.TrimSpace(f.value("target"))

	if id == "" {
		return "", config.AppSpec{}, fmt.Errorf("id is required")
	}
	if env == "" || host == "" {
		return "", config.AppSpec{}, fmt.Errorf("env and host are required")
	}
	if target == "" {
		return "", config.AppSpec{}, fmt.Errorf("target is required")
	}

	spec := config.AppSpec{
		ID:       id,
		Host:     host,
		Provider: provider,
		Workdir:  strings.TrimSpace(f.value("workdir")),
		Tags:     splitTags(f.value("tags")),
		Deploy:   strings.TrimSpace(f.value("pipeline")),
	}
	if spec.Deploy == "none" {
		spec.Deploy = ""
	}

	switch app.ProviderKind(provider) {
	case app.KindSystemd:
		spec.Unit = target
	case app.KindDocker:
		spec.Container = target
	case app.KindCompose:
		parts := strings.SplitN(target, "/", 2)
		spec.Project = parts[0]
		if len(parts) == 2 {
			spec.Service = parts[1]
		}
	case app.KindPM2:
		spec.Process = target
	case app.KindCommand:
		spec.Command = target
	}

	if kind := f.value("health"); kind != "" && kind != "none" {
		h := &config.HealthSpec{Type: kind}
		switch kind {
		case "http":
			h.URL = strings.TrimSpace(f.value("health target"))
		case "tcp":
			h.Address = strings.TrimSpace(f.value("health target"))
		case "command":
			h.Cmd = strings.TrimSpace(f.value("health target"))
		}
		h.Expect = strings.TrimSpace(f.value("expect"))
		spec.Health = h
	}
	return env, spec, nil
}

// hostFormFields lists the host editor's fields.
func hostFormFields() []formField {
	return []formField{
		{label: "id", help: "short alias used by deployments"},
		{label: "address", help: "hostname or IP"},
		{label: "port", help: "default 22"},
		{label: "user", help: "ssh user"},
		{label: "auth", options: []string{"agent", "key", "password"}},
		{label: "key", help: "private key path when auth=key"},
		{label: "proxy_jump", help: "optional host id to jump through"},
		{label: "tags", help: "comma separated"},
	}
}

func newHostForm() form {
	f := form{title: "new host", kind: "host", fields: hostFormFields()}
	f.set("auth", "agent")
	return f
}

func editHostForm(h app.Host) form {
	f := newHostForm()
	f.title = "edit host"
	f.originalID = h.ID
	f.set("id", h.ID)
	f.set("address", h.Address)
	if h.Port != 0 {
		f.set("port", strconv.Itoa(h.Port))
	}
	f.set("user", h.User)
	f.set("auth", string(h.Auth))
	f.set("key", h.Key)
	f.set("proxy_jump", h.ProxyJump)
	f.set("tags", strings.Join(h.Tags, ", "))
	return f
}

func (f *form) hostSpec() (string, config.HostSpec, error) {
	id := strings.TrimSpace(f.value("id"))
	address := strings.TrimSpace(f.value("address"))
	user := strings.TrimSpace(f.value("user"))
	if id == "" {
		return "", config.HostSpec{}, fmt.Errorf("id is required")
	}
	if address == "" {
		return "", config.HostSpec{}, fmt.Errorf("address is required")
	}
	if user == "" {
		return "", config.HostSpec{}, fmt.Errorf("user is required")
	}

	port := 0
	if raw := strings.TrimSpace(f.value("port")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return "", config.HostSpec{}, fmt.Errorf("port must be 1-65535")
		}
		port = n
	}

	spec := config.HostSpec{
		Address:   address,
		Port:      port,
		User:      user,
		Auth:      f.value("auth"),
		Key:       strings.TrimSpace(f.value("key")),
		ProxyJump: strings.TrimSpace(f.value("proxy_jump")),
		Tags:      splitTags(f.value("tags")),
	}
	if spec.Auth == "key" && spec.Key == "" {
		return "", config.HostSpec{}, fmt.Errorf("auth=key needs a key path")
	}
	return id, spec, nil
}

func splitTags(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// formView renders the editor centered on the screen.
func (m *Model) formView() string {
	f := &m.form
	th := m.th

	lines := make([]string, 0, len(f.fields)+4)
	for i, fld := range f.fields {
		label := th.dim.Render(pad(fld.label, 14))

		value := fld.display()
		var cell string
		switch {
		case i == f.cursor && fld.isSelect():
			cell = th.selected.Render("[" + value + " ▾]")
		case i == f.cursor:
			cell = "[" + th.selected.Render(value+"█") + "]"
		case fld.isSelect():
			cell = th.bold.Render("[" + value + " ▾]")
		default:
			cell = th.bold.Render("[" + value + "]")
		}
		lines = append(lines, label+cell)
	}

	if f.cursor < len(f.fields) && f.fields[f.cursor].help != "" {
		lines = append(lines, "", th.faint.Render(f.fields[f.cursor].help))
	}
	if f.err != "" {
		lines = append(lines, "", th.err.Render(f.err))
	}
	lines = append(lines, "", th.faint.Render("tab/↑↓ field · enter cycle · ctrl+s save · esc cancel"))

	w := min(max(56, m.width/2), 84)
	box := m.box(f.title, lines, w, len(lines)+3, true)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
