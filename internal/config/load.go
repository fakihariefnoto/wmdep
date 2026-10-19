package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// Diag is one diagnostic: an error or warning with the file, line, and YAML
// path that produced it. Load never fails fast: an operator fixing a config
// wants every problem in one pass.
type Diag struct {
	File     string
	Line     int
	Path     string
	Msg      string
	Severity string // "error" | "warning"
}

// Error renders the diagnostic as "file:line: msg (at path)".
func (d Diag) Error() string {
	var b strings.Builder
	b.WriteString(d.File)
	if d.Line > 0 {
		fmt.Fprintf(&b, ":%d", d.Line)
	}
	b.WriteString(": ")
	b.WriteString(d.Msg)
	if d.Path != "" {
		fmt.Fprintf(&b, " (at %s)", d.Path)
	}
	return b.String()
}

// Environment is a resolved environment: its apps plus the host IDs it names.
type Environment struct {
	Name  string
	Hosts []string
	Apps  []app.App

	// AppIndex maps an app to its position in the environment file, so
	// diagnostics point at the line the operator wrote. It is kept separate
	// from Apps because an app that fails to resolve is not in Apps, yet still
	// occupies a position in the file.
	AppIndex map[app.Key]int
}

// Bundle is a fully loaded and resolved config directory. It is what the UI
// and the headless commands both consume.
type Bundle struct {
	Dir       string
	Config    File
	Hosts     map[string]app.Host
	HostOrder []string
	Pipelines map[string]Pipeline
	Envs      []*Environment
	Apps      []app.App

	// Warnings are non-fatal problems (missing key file, unused host, ...).
	// They are reported but do not block startup.
	Warnings []Diag

	lines  map[string]map[string]int // file -> yaml path -> line
	stamps map[string]fileStamp      // file -> content stamp at load
}

// fileStamp is what the writer compares before overwriting a file, so an
// external edit is never silently clobbered.
type fileStamp struct {
	modTime time.Time
	size    int64
	sum     [32]byte
}

// HostsOf returns the hosts referenced by an environment, in file order,
// skipping any that failed to resolve.
func (b *Bundle) HostsOf(env string) []app.Host {
	var out []app.Host
	for _, e := range b.Envs {
		if e.Name != env {
			continue
		}
		for _, id := range e.Hosts {
			if h, ok := b.Hosts[id]; ok {
				out = append(out, h)
			}
		}
	}
	return out
}

// EnvNames returns loaded environment names in load order.
func (b *Bundle) EnvNames() []string {
	out := make([]string, 0, len(b.Envs))
	for _, e := range b.Envs {
		out = append(out, e.Name)
	}
	return out
}

// Defaults with sensible fallbacks applied, so callers never see zero values.
func (b *Bundle) Defaults() Defaults {
	d := b.Config.Defaults
	if d.SSHTimeout.Std() <= 0 {
		d.SSHTimeout = Duration(10_000_000_000) // 10s
	}
	if d.PollInterval.Std() <= 0 {
		d.PollInterval = Duration(5_000_000_000) // 5s
	}
	if d.LogTail <= 0 {
		d.LogTail = 500
	}
	if d.MaxStreams <= 0 {
		d.MaxStreams = 3
	}
	return d
}

// Options controls loading.
type Options struct {
	// OnlyEnv narrows loading to a single environment, as with --env.
	OnlyEnv string
	// Strict turns warnings into errors.
	Strict bool
}

// Load reads a config directory and resolves it into a Bundle. The returned
// slice holds every error found; a non-empty error slice means the config is
// not usable.
func Load(dir string, opts Options) (*Bundle, []Diag) {
	var errs []Diag
	b := &Bundle{
		Dir:       dir,
		Hosts:     map[string]app.Host{},
		Pipelines: map[string]Pipeline{},
		lines:     map[string]map[string]int{},
		stamps:    map[string]fileStamp{},
	}
	l := LayoutFor(dir)

	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return b, []Diag{{
			File: dir, Severity: "error",
			Msg: fmt.Sprintf("config directory not found; run `wmdep config init` to create it"),
		}}
	}

	// config.yaml
	cfgNode, ok := b.read(l.Config, &b.Config, &errs, true)
	if !ok {
		return b, errs
	}
	if b.Config.Version != Version {
		line := b.lineOf(l.Config, "version")
		errs = append(errs, Diag{
			File: l.Config, Line: line, Path: "version", Severity: "error",
			Msg: fmt.Sprintf("unsupported config version %d (this build understands %d)", b.Config.Version, Version),
		})
	}
	_ = cfgNode

	// hosts.yaml
	var hostsFile HostsFile
	if _, ok := b.read(l.Hosts, &hostsFile, &errs, true); !ok {
		return b, errs
	}
	b.resolveHosts(hostsFile, l, &errs)

	// pipelines.yaml is optional: a pure "watch" config needs none.
	if fileExists(l.Pipelines) {
		var pf PipelinesFile
		if _, ok := b.read(l.Pipelines, &pf, &errs, true); ok {
			b.Pipelines = pf.Pipelines
			if b.Pipelines == nil {
				b.Pipelines = map[string]Pipeline{}
			}
		}
	}

	// environments/*.yaml
	b.loadEnvironments(l, opts, &errs)

	// cross-reference validation
	b.validate(&errs)

	if opts.Strict {
		for _, w := range b.Warnings {
			w.Severity = "error"
			errs = append(errs, w)
		}
		b.Warnings = nil
	}

	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].File != errs[j].File {
			return errs[i].File < errs[j].File
		}
		return errs[i].Line < errs[j].Line
	})
	return b, errs
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// read parses a YAML file strictly (unknown fields are errors) and records its
// line index. required controls the missing-file message.
func (b *Bundle) read(path string, into any, errs *[]Diag, required bool) (*yaml.Node, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return nil, true
		}
		*errs = append(*errs, Diag{File: path, Severity: "error", Msg: "cannot read file: " + err.Error()})
		return nil, false
	}

	// Record what was loaded so a later save can refuse to overwrite an
	// external edit.
	st := fileStamp{sum: sha256.Sum256(raw), size: int64(len(raw))}
	if info, statErr := os.Stat(path); statErr == nil {
		st.modTime = info.ModTime()
	}
	b.stamps[path] = st

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		*errs = append(*errs, Diag{File: path, Severity: "error", Msg: "invalid YAML: " + err.Error()})
		return nil, false
	}
	b.lines[path] = map[string]int{}
	if len(root.Content) > 0 {
		indexLines(root.Content[0], "", b.lines[path])
	}

	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		*errs = append(*errs, Diag{File: path, Severity: "error", Msg: "invalid config: " + err.Error()})
		return nil, false
	}
	return &root, true
}

// indexLines walks a YAML node tree recording the line for each value path,
// e.g. "apps.2.host". It is what lets diagnostics point at a line.
func indexLines(n *yaml.Node, path string, out map[string]int) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			indexLines(c, path, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			child := n.Content[i+1]
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if _, seen := out[childPath]; !seen {
				out[childPath] = n.Content[i].Line
			}
			indexLines(child, childPath, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			indexLines(c, fmt.Sprintf("%s.%d", path, i), out)
		}
	default:
		if _, seen := out[path]; !seen {
			out[path] = n.Line
		}
	}
}

// lineOf returns the recorded line for a YAML path, or 0.
func (b *Bundle) lineOf(file, path string) int {
	if idx, ok := b.lines[file]; ok {
		if l, ok := idx[path]; ok {
			return l
		}
	}
	return 0
}

// lineOfAny returns the first recorded line among several candidate paths. It
// is used when the offending key is absent (a missing field has no line of its
// own), so the diagnostic can still point at the enclosing entry.
func (b *Bundle) lineOfAny(file string, paths ...string) int {
	for _, p := range paths {
		if l := b.lineOf(file, p); l > 0 {
			return l
		}
	}
	return 0
}

func (b *Bundle) resolveHosts(hf HostsFile, l Layout, errs *[]Diag) {
	for id, spec := range hf.Hosts {
		auth := app.AuthMethod(spec.Auth)
		if spec.Auth == "" {
			auth = app.AuthAgent
		}
		if !auth.Valid() {
			*errs = append(*errs, Diag{
				File: l.Hosts, Line: b.lineOf(l.Hosts, "hosts."+id+".auth"), Path: "hosts." + id + ".auth",
				Severity: "error",
				Msg:      fmt.Sprintf("unknown auth %q (want agent, key, or password)", spec.Auth),
			})
			continue
		}
		h := app.Host{
			ID:        id,
			Address:   spec.Address,
			Port:      spec.Port,
			User:      spec.User,
			Auth:      auth,
			Key:       spec.Key,
			ProxyJump: spec.ProxyJump,
			Tags:      spec.Tags,
		}
		b.Hosts[id] = h
		b.HostOrder = append(b.HostOrder, id)
	}
	sort.Strings(b.HostOrder)
}

func (b *Bundle) loadEnvironments(l Layout, opts Options, errs *[]Diag) {
	entries, err := os.ReadDir(l.Environments)
	if err != nil {
		*errs = append(*errs, Diag{
			File: l.Environments, Severity: "error",
			Msg: "cannot read environments directory: " + err.Error(),
		})
		return
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(l.Environments, name)
		var ef EnvironmentFile
		if _, ok := b.read(path, &ef, errs, false); !ok {
			continue
		}

		envName := ef.Name
		if envName == "" {
			envName = strings.TrimSuffix(name, ".yaml")
		}
		if opts.OnlyEnv != "" && envName != opts.OnlyEnv {
			continue
		}
		if !b.Config.Environments.Enabled(envName) {
			continue
		}

		env := &Environment{Name: envName, Hosts: slices.Clone(ef.Hosts), AppIndex: map[app.Key]int{}}
		for i, spec := range ef.Apps {
			a, ok := b.resolveApp(envName, spec, path, i, errs)
			if !ok {
				continue
			}
			env.Apps = append(env.Apps, a)
			// Remember where in the file this app lives, for diagnostics.
			env.AppIndex[a.Key()] = i
			b.Apps = append(b.Apps, a)
		}
		b.Envs = append(b.Envs, env)
	}

	if opts.OnlyEnv != "" {
		var found bool
		for _, e := range b.Envs {
			if e.Name == opts.OnlyEnv {
				found = true
			}
		}
		if !found {
			*errs = append(*errs, Diag{
				File: l.Environments, Severity: "error",
				Msg: fmt.Sprintf("environment %q not found or not enabled", opts.OnlyEnv),
			})
		}
	}
}

func (b *Bundle) resolveApp(env string, spec AppSpec, file string, idx int, errs *[]Diag) (app.App, bool) {
	at := func(field string) Diag {
		path := fmt.Sprintf("apps.%d.%s", idx, field)
		return Diag{File: file, Line: b.lineOf(file, path), Path: path, Severity: "error"}
	}

	kind := app.ProviderKind(spec.Provider)
	if !kind.Valid() {
		d := at("provider")
		d.Msg = fmt.Sprintf("unknown provider %q (want %s)", spec.Provider, strings.Join(kindNames(), ", "))
		*errs = append(*errs, d)
		return app.App{}, false
	}

	a := app.App{
		ID:          spec.ID,
		Host:        spec.Host,
		Environment: env,
		Kind:        kind,
		Unit:        spec.Unit,
		Container:   spec.Container,
		Project:     spec.Project,
		Service:     spec.Service,
		Process:     spec.Process,
		Command:     spec.Command,
		Workdir:     spec.Workdir,
		Deploy:      spec.Deploy,
		Tags:        spec.Tags,
	}
	if spec.Health != nil {
		a.Health = &app.Health{
			Kind:    app.HealthKind(spec.Health.Type),
			URL:     spec.Health.URL,
			Cmd:     spec.Health.Cmd,
			Address: spec.Health.Address,
			Expect:  spec.Health.Expect,
			Timeout: spec.Health.Timeout.Std(),
		}
	}
	return a, true
}

func kindNames() []string {
	out := make([]string, 0, len(app.KindNames))
	for _, k := range app.KindNames {
		out = append(out, string(k))
	}
	return out
}
