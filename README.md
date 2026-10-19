# wmdep — Watch My Deployment

A terminal cockpit for services spread across many servers. One screen shows every
app on every host, tails logs, and runs deploys — over plain SSH, with no agent on
the servers and no control-plane server to run.

Repository: <https://github.com/fakihariefnoto/wmdep> · module:
`github.com/fakihariefnoto/wmdep`

```
wmdep  dashboard                                                             prod,staging · 3 hosts

OVERVIEW
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ fleet                                                                                            │
│ 4 deployments    ● 2 up   ✕ 1 down   ▲ 1 degraded                                                │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
╭───────────────────────────────────────────────╮╭───────────────────────────────────────────────╮
│ prod                                          ││ staging                                       │
│ 3 deployments   ▲ 2 issues                    ││ 1 deployment   ● healthy                      │
│ web-1 · web-2                                 ││ stage-1                                       │
│ ▲ api  ● worker  ✕ redis                      ││ ● web                                         │
╰───────────────────────────────────────────────╯╰───────────────────────────────────────────────╯

HOSTS
╭──────────────────────────────╮╭──────────────────────────────╮╭──────────────────────────────╮
│ web-1                        ││ web-2                        ││ stage-1                      │
│ 10.0.0.11:2222 · deploy      ││ 10.0.0.12:22 · deploy        ││ stage.example.com:22 · ubun… │
│ 2 dep   1 issue              ││ 1 dep   1 issue              ││ 1 dep   healthy              │
│ ▲ health check failing       ││ ✕ failed                     ││ ● web running                │
╰──────────────────────────────╯╰──────────────────────────────╯╰──────────────────────────────╯

ATTENTION
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ✕ redis     web-2 · prod    failed                                                               │
│ ▲ api       web-1 · prod    degraded  health check failing                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯






↑↓←→ cards · enter open · a add · e edit · x delete · s scan · r refresh · ? help
```

## Status

**M1, M1b, and M1c are complete.** Config loading/validation, the screen router,
splash, dashboard cards, searchable Deployments and Servers lists, and the
add/edit/duplicate/delete editors are real. The guarded writer preserves
comments, validates before saving, keeps a `.bak`, and refuses to overwrite an
external edit.

SSH is live: agent, key, and keyring-password auth; known_hosts verification;
one pooled connection per host; systemd, docker, compose, pm2, and command
providers; `d` twice runs the app's pipeline with rollback. `--demo` keeps the
in-memory provider for trying the UI without servers.

Known hosts must exist (`ssh host` once) — wmdep refuses unknown keys. Store a
password with `wmdep auth set <host>`.

Host scan works: Servers → select a host → `s` discovers systemd/docker/pm2
services, checklist import writes valid deployments.

**Next:** M6 SFTP browser/settings/notifications, run history, streaming deploy
output. Release scaffolding (GoReleaser, CI, tap workflow) is in place.

## Navigation

```
splash (ASCII menu) ──enter──▶ dashboard (cards) ──enter──▶ manage (3 panes)
        │                          │  a/e/x                      │
        │                          └──▶ editors                  │
        └── q ──▶ quit             esc ◀──────────────────────────┘
```

- `wmdep` starts on the splash; `--no-splash` or `ui.splash: false` skips it.
- `--screen dashboard|manage` starts directly on a screen (scripts, muscle memory).
- `esc` walks back: help → dirty editor → dashboard → splash.

## Design notes

- Truecolor palette: violet/pink accents, green/amber/red states, near-black card
  surface; light theme uses darker variants. `NO_COLOR` and 256/16-colour
  terminals degrade automatically.
- Every state carries a glyph **and** a word, so colour is never the only signal.
- Cards use a six-row rhythm, one-cell gutters, rounded borders, and an accent
  border on the focused card. The grid is 1/2/3 columns by width.
- The wordmark is a six-row ANSI-shadow logo with a violet→pink gradient.

| Area | State |
|---|---|
| Config: multi-file, cross-ref validation with `file:line` | done |
| TUI: host tree, app table, detail, logs, help, filtering | done |
| Watch scheduler: visible-only polling, jitter, backoff | done |
| `status`, `config validate`, `config init`, `config show` | done |
| SSH/SFTP transport | M2 |
| systemd/docker providers | M3 |
| Log streaming | M4 |
| Deploy engine | M5 |
| SFTP browser, themes, notifications | M6 |

See [PLAN.md](PLAN.md) for the full design and milestone list.

## Quickstart

```sh
go build -o wmdep .
./wmdep config init          # writes starter config to ~/.config/wmdep
./wmdep config doctor        # keys, permissions, empty environments
./wmdep config validate
./wmdep status
./wmdep                      # splash -> dashboard -> manage
```

The starter config is valid out of the box and points at example hosts
(`10.0.0.11`, `stage.example.com`), so you can explore the UI before editing
anything. Use `--config <dir>` or `$WMDEP_CONFIG_DIR` to keep a config elsewhere:

```sh
./wmdep --config ./demo config init
./wmdep --config ./demo
```

## Config layout

One file per environment, plus shared inventories referenced by name:

```
~/.config/wmdep/
  config.yaml            defaults, UI prefs, which environments load
  hosts.yaml             the host inventory
  pipelines.yaml         deploy pipelines
  environments/
    prod.yaml
    staging.yaml
```

`hosts.yaml`:

```yaml
hosts:
  web-1:
    address: 10.0.0.11
    port: 22
    user: deploy
    auth: agent            # agent | key | password
    tags: [web]
  stage-1:
    address: stage.example.com
    user: ubuntu
    auth: key
    key: ~/.ssh/stage
```

Passwords are never written to config: `auth: password` prompts once and stores
the secret in the OS keyring under `wmdep:<host>`.

`environments/prod.yaml`:

```yaml
name: prod
hosts: [web-1, web-2]

apps:
  - id: api
    host: web-1
    provider: systemd
    unit: api.service
    health: { type: http, url: http://127.0.0.1:8080/health, expect: "200" }
    deploy: api-prod

  - id: redis
    host: web-2
    provider: docker
    container: redis
```

Providers: `systemd`, `docker`, `compose`, `pm2`, `command` (escape hatch).

`pipelines.yaml` — both deploy models are first-class, and can mix:

```yaml
pipelines:
  api-prod:                       # build here, upload the artifact
    steps:
      - build:  { local: "go build -o dist/api ./cmd/api" }
      - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
      - run:    { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
      - wait:   { health: true, timeout: 60s }

  web-prod:                       # pull and build on the server
    steps:
      - pull:  { remote: /srv/web, branch: main }
      - build: { remote: /srv/web, cmd: "npm ci && npm run build" }
      - run:   { remote: "systemctl restart web" }
      - wait:  { health: true, timeout: 90s }
```

Step types: `build`, `pull`, `upload`, `download`, `run`, `wait`, `notify`.
Pipelines are validated today; execution lands in M5.

## Keys

| Screen | Key | Action |
|---|---|---|
| Splash | `↑↓` / `jk` | move |
| | `enter` | open the highlighted item |
| Dashboard | `tab` / arrows | move between cards |
| | `enter` | open Manage scoped to the card |
| | `a` / `e` / `x` | add / edit / delete a deployment |
| | `s` | scan a host (M2b) |
| | `r` | poll now |
| Deployments | `enter` | open Manage focused on that app |
| | `a` / `e` / `c` / `x` | add / edit / duplicate / delete |
| | `/` | search id, host, env, provider, target |
| Servers | `enter` | open Manage scoped to that host |
| | `a` / `e` / `x` | add / edit / delete a host |
| | `t` / `s` | test connection (M2) / scan (M2b) |
| Editor | `tab` / arrows | next / previous field |
| | `enter` | cycle a select |
| | `ctrl+s` / `esc` | save / cancel |
| Manage | `tab` / `shift+tab` | next / previous pane |
| | `↑↓` `k`/`j` | move |
| | `←→` `h`/`l` | collapse / expand environment |
| | `enter` / `space` | select |
| | `g` / `G` | top / bottom |
| | `/` | filter by id, host, env, provider, or target; `esc` clears |
| | `R` | poll now |
| | `r` / `x` | restart / start-stop |
| | `L` / `f` / `c` | focus logs / toggle follow / clear |
| | `d` / `F` | deploy preview / SFTP browser (M5 / M6) |
| | `?` / `q` | help / quit |

`esc` on Manage returns to the dashboard (after closing help or clearing a
filter). The dashboard keeps the last scope, so going back in is one keypress.

## Development

```sh
make build     # binary with version metadata
make test      # unit tests
make race      # tests under the race detector
make fmt vet   # formatting and vet
make demo      # build, scaffold ./demo if needed, run the TUI
make snapshot  # GoReleaser build into dist/ without publishing
make release V=0.1.0
```

Layout:

```
main.go
internal/app/       domain model, no dependencies
internal/config/    multi-file loader, diagnostics, validation
internal/provider/  Provider interface, registry, fake
internal/watch/     visible-only poll scheduler with backoff
internal/ui/        Bubble Tea model, panes, theme, keymap
internal/cli/       cobra commands, starter config
```

Notes for contributors:

- Charm libraries live at `charm.land/*` (v2 module paths) and require Go 1.26.
- `lipgloss.Width`/`Height` on a bordered style are **total** block sizes; content
  is padded, never truncated. Panes therefore clip their own content (`box`).
- Every remote call in the UI must run inside a `tea.Cmd`; Update never blocks.

## Releasing

Not wired up yet — it lands with M7. The intended setup is adapted from
`~/Projects/fakih.net/omssh`: GoReleaser for GitHub releases and the
`fakihariefnoto/homebrew-tap`, GitHub Actions for CI and release, and a
`make release V=minor` wrapper around `scripts/release.sh`. See
[PLAN.md §10](PLAN.md#10-repository-ci-and-release) for the file-by-file plan and
the differences that matter for this repo (root main package, version variables
in `internal/cli`).
