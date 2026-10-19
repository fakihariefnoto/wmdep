package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// validate runs cross-reference checks over the resolved bundle: hosts that
// apps point at, providers that lack their required target, pipelines that do
// not exist, and so on. Errors block startup; warnings are reported.
func (b *Bundle) validate(errs *[]Diag) {
	b.validateHosts(errs)
	b.validatePipelines(errs)

	seenApp := map[string]string{} // "env/id" -> where it was first defined

	for _, env := range b.Envs {
		envFile := filepath.Join(LayoutFor(b.Dir).Environments, env.Name+".yaml")

		for i, hostID := range env.Hosts {
			if _, ok := b.Hosts[hostID]; !ok {
				*errs = append(*errs, Diag{
					File: envFile, Line: b.lineOf(envFile, fmt.Sprintf("hosts.%d", i)),
					Path: fmt.Sprintf("hosts.%d", i), Severity: "error",
					Msg: fmt.Sprintf("environment %q references unknown host %q", env.Name, hostID),
				})
			}
		}
		envHosts := map[string]bool{}
		for _, h := range env.Hosts {
			envHosts[h] = true
		}

		// App index inside the file, so diagnostics point at the right entry.
		// The map is filled at load time and includes apps that failed to
		// resolve, which keeps the numbering stable.
		for _, a := range env.Apps {
			idx := env.AppIndex[a.Key()]
			errf := func(field, format string, args ...any) {
				path := fmt.Sprintf("apps.%d.%s", idx, field)
				*errs = append(*errs, Diag{
					File: envFile, Line: b.lineOfAny(envFile, path, fmt.Sprintf("apps.%d", idx)), Path: path,
					Severity: "error", Msg: fmt.Sprintf(format, args...),
				})
			}
			warnf := func(field, format string, args ...any) {
				path := fmt.Sprintf("apps.%d.%s", idx, field)
				b.Warnings = append(b.Warnings, Diag{
					File: envFile, Line: b.lineOfAny(envFile, path, fmt.Sprintf("apps.%d", idx)), Path: path,
					Severity: "warning", Msg: fmt.Sprintf(format, args...),
				})
			}

			if a.ID == "" {
				errf("id", "app is missing an id")
				continue
			}
			if _, dup := seenApp[env.Name+"/"+a.ID]; dup {
				errf("id", "duplicate app id %q in environment %q", a.ID, env.Name)
			} else {
				seenApp[env.Name+"/"+a.ID] = envFile
			}

			if a.Host == "" {
				errf("host", "app %q does not name a host", a.ID)
			} else if _, ok := b.Hosts[a.Host]; !ok {
				errf("host", "app %q references unknown host %q", a.ID, a.Host)
			} else if !envHosts[a.Host] {
				warnf("host", "app %q runs on %q, which environment %q does not list", a.ID, a.Host, env.Name)
			}

			switch a.Kind {
			case app.KindSystemd:
				if a.Unit == "" {
					errf("unit", "app %q is a systemd app but has no unit", a.ID)
				}
			case app.KindDocker:
				if a.Container == "" {
					errf("container", "app %q is a docker app but has no container", a.ID)
				}
			case app.KindCompose:
				if a.Project == "" {
					errf("project", "app %q is a compose app but has no project", a.ID)
				}
				if a.Service == "" {
					errf("service", "app %q is a compose app but has no service", a.ID)
				}
			case app.KindPM2:
				if a.Process == "" {
					errf("process", "app %q is a pm2 app but has no process", a.ID)
				}
			case app.KindCommand:
				if a.Command == "" {
					errf("command", "app %q is a command app but has no command", a.ID)
				}
			}

			if a.Health != nil {
				h := a.Health
				if !h.Kind.Valid() {
					errf("health.type", "app %q has unknown health type %q (want http, command, or tcp)", a.ID, h.Kind)
				}
				switch h.Kind {
				case app.HealthHTTP:
					if h.URL == "" {
						errf("health.url", "app %q has an http health check with no url", a.ID)
					}
				case app.HealthCommand:
					if h.Cmd == "" {
						errf("health.cmd", "app %q has a command health check with no cmd", a.ID)
					}
				case app.HealthTCP:
					if h.Address == "" {
						errf("health.address", "app %q has a tcp health check with no address", a.ID)
					}
				case app.HealthNone:
					warnf("health", "app %q has an empty health check; it will be ignored", a.ID)
				}
			}

			if a.Deploy != "" {
				if _, ok := b.Pipelines[a.Deploy]; !ok {
					errf("deploy", "app %q references unknown pipeline %q", a.ID, a.Deploy)
				}
			}
		}
	}

	// Hosts that nothing references are almost always a typo or leftover.
	used := map[string]bool{}
	for _, env := range b.Envs {
		for _, h := range env.Hosts {
			used[h] = true
		}
		for _, a := range env.Apps {
			used[a.Host] = true
		}
	}
	for id := range b.Hosts {
		if !used[id] {
			b.Warnings = append(b.Warnings, Diag{
				File:     LayoutFor(b.Dir).Hosts,
				Severity: "warning",
				Msg:      fmt.Sprintf("host %q is defined but not used by any environment", id),
			})
		}
	}
}

func (b *Bundle) validateHosts(errs *[]Diag) {
	l := LayoutFor(b.Dir)
	for id, h := range b.Hosts {
		at := func(field string) string {
			return "hosts." + id + "." + field
		}
		hostLine := func(field string) int {
			return b.lineOfAny(l.Hosts, at(field), "hosts."+id)
		}
		if h.Address == "" {
			*errs = append(*errs, Diag{
				File: l.Hosts, Line: hostLine("address"), Path: at("address"),
				Severity: "error", Msg: fmt.Sprintf("host %q has no address", id),
			})
		}
		if h.User == "" {
			*errs = append(*errs, Diag{
				File: l.Hosts, Line: hostLine("user"), Path: at("user"),
				Severity: "error", Msg: fmt.Sprintf("host %q has no user", id),
			})
		}
		if h.Port != 0 && (h.Port < 1 || h.Port > 65535) {
			*errs = append(*errs, Diag{
				File: l.Hosts, Line: b.lineOf(l.Hosts, at("port")), Path: at("port"),
				Severity: "error", Msg: fmt.Sprintf("host %q has invalid port %d", id, h.Port),
			})
		}
		switch h.Auth {
		case app.AuthKey:
			if h.Key == "" {
				*errs = append(*errs, Diag{
					File: l.Hosts, Line: b.lineOf(l.Hosts, at("key")), Path: at("key"),
					Severity: "error", Msg: fmt.Sprintf("host %q uses key auth but names no key file", id),
				})
			} else if p := expandHome(h.Key); !fileExists(p) {
				b.Warnings = append(b.Warnings, Diag{
					File: l.Hosts, Line: b.lineOf(l.Hosts, at("key")), Path: at("key"),
					Severity: "warning", Msg: fmt.Sprintf("host %q: key file %s does not exist", id, p),
				})
			}
		case app.AuthPassword:
			// Nothing to check: the secret lives in the OS keyring. Warn only
			// so nobody is surprised by a prompt on first connect.
			b.Warnings = append(b.Warnings, Diag{
				File: l.Hosts, Line: b.lineOf(l.Hosts, at("auth")), Path: at("auth"),
				Severity: "warning",
				Msg:      fmt.Sprintf("host %q uses password auth; the secret is stored in the OS keyring, not here", id),
			})
		}
		if h.ProxyJump != "" && h.ProxyJump != id {
			if _, ok := b.Hosts[h.ProxyJump]; !ok {
				*errs = append(*errs, Diag{
					File: l.Hosts, Line: b.lineOf(l.Hosts, at("proxy_jump")), Path: at("proxy_jump"),
					Severity: "error",
					Msg:      fmt.Sprintf("host %q jumps through unknown host %q", id, h.ProxyJump),
				})
			}
		}
		if h.ProxyJump == id {
			*errs = append(*errs, Diag{
				File: l.Hosts, Line: b.lineOf(l.Hosts, at("proxy_jump")), Path: at("proxy_jump"),
				Severity: "error", Msg: fmt.Sprintf("host %q cannot jump through itself", id),
			})
		}
	}
}

func (b *Bundle) validatePipelines(errs *[]Diag) {
	l := LayoutFor(b.Dir)
	for name, p := range b.Pipelines {
		base := "pipelines." + name
		if len(p.Steps) == 0 {
			*errs = append(*errs, Diag{
				File: l.Pipelines, Line: b.lineOf(l.Pipelines, base+".steps"), Path: base + ".steps",
				Severity: "error", Msg: fmt.Sprintf("pipeline %q has no steps", name),
			})
		}
		for i, s := range p.Steps {
			b.validateStep(l.Pipelines, name, fmt.Sprintf("%s.steps.%d", base, i), s, errs)
		}
		for i, s := range p.Rollback {
			b.validateStep(l.Pipelines, name, fmt.Sprintf("%s.rollback.%d", base, i), s, errs)
		}
	}
}

func (b *Bundle) validateStep(file, pipeline, path string, s Step, errs *[]Diag) {
	kind, ok := s.Kind()
	if !ok {
		*errs = append(*errs, Diag{
			File: file, Line: b.lineOfAny(file, path, path[:strings.LastIndex(path, ".")]),
			Path: path, Severity: "error",
			Msg: fmt.Sprintf("pipeline %q: a step must set exactly one step type (build, pull, upload, download, run, wait, notify)", pipeline),
		})
		return
	}
	fail := func(format string, args ...any) {
		*errs = append(*errs, Diag{
			File: file, Line: b.lineOf(file, path+"."+kind), Path: path + "." + kind,
			Severity: "error", Msg: fmt.Sprintf("pipeline %q: ", pipeline) + fmt.Sprintf(format, args...),
		})
	}

	switch kind {
	case "build":
		bld := s.Build
		if bld.Local == "" && bld.Remote == "" {
			fail("build needs either local or remote")
		}
		if bld.Local != "" && bld.Remote != "" {
			fail("build takes either local or remote, not both")
		}
		if bld.Remote != "" && bld.Cmd == "" {
			fail("remote build needs cmd")
		}
	case "pull":
		if s.Pull.Remote == "" {
			fail("pull needs remote")
		}
	case "upload":
		if s.Upload.Local == "" || s.Upload.Remote == "" {
			fail("upload needs local and remote")
		}
	case "download":
		if s.Download.Remote == "" || s.Download.Local == "" {
			fail("download needs remote and local")
		}
	case "run":
		if s.Run.Remote == "" {
			fail("run needs remote")
		}
	case "wait":
		if !s.Wait.Health && s.Wait.Sleep.Std() <= 0 {
			fail("wait needs either health: true or a sleep duration")
		}
	case "notify":
		if s.Notify.Message == "" {
			fail("notify needs message")
		}
	}
}

// expandHome turns a leading ~ into the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}
