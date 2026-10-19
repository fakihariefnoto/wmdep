# wmdep — Watch My Deployment

A terminal cockpit for services spread across many servers. One screen shows every
app on every host, tails logs, and runs deploys — over plain SSH, with no agent on
the servers and no control-plane server to run.

Repository: <https://github.com/fakihariefnoto/wmdep> · module:
`github.com/fakihariefnoto/wmdep`

```
wmdep  watch my deployment  ·  prod,staging  ·  4 apps                             updated just now
╭──────────────────╮╭────────────────────────────────────────────────────╮╭────────────────────────╮
│ hosts            ││ apps · prod                                        ││ detail                 │
│ ▲ ▾ prod      3  ││  APP             HOST          STATE       UP      ││ api                    │
│ ▲   web-1     2  ││  api             web-1         ▲ degraded  -       ││ prod · web-1 · systemd │
│ ●   web-2     1  ││  worker          web-1         ● running   2h0m    ││                        │
│ ? ▾ staging   1  ││  redis           web-2         ● running   3h0m    ││ target   api.service   │
│ ?   stage-1   1  ││                                                    ││ workdir  /srv/api      │
│                  ││                                                    ││ deploy   api-prod      │
│                  ││                                                    ││ health   http http://… │
│                  ││                                                    ││                        │
│                  ││                                                    ││ status                 │
│                  ││                                                    ││ state    degraded (he… │
│                  ││                                                    ││ uptime   1h0m          │
│                  ││                                                    ││ cpu      4%            │
│                  ││                                                    ││ memory   40M           │
│                  ││                                                    ││ observed just now      │
│                  ││                                                    ││                        │
│                  ││                                                    ││                        │
│                  ││                                                    ││                        │
│                  ││                                                    ││                        │
╰──────────────────╯╰────────────────────────────────────────────────────╯╰────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│ logs · prod/web-1/api · following                                                                │
│ 18:22:01 INFO  request handled path=/api/7 status=200 dur=31ms                                   │
│ 18:22:02 WARN  pool saturated idle=0 waiting=3                                                   │
│ 18:22:03 ERROR upstream redis timeout after 2s                                                   │
│                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
pane: hosts     tab next pane • ↑/k up • ↓/j down • d deploy • r restart • L logs • ? help • q quit
```

## Status

**M1 (skeleton) is complete.** The UI, config loader, validation, filtering, and
watch scheduler are real. Remote I/O is not: every app is served by an in-memory
provider, which is why the header shows a `demo` chip.

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
$EDITOR ~/.config/wmdep/hosts.yaml
./wmdep config validate
./wmdep status
./wmdep                      # the TUI
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

| Key | Action |
|---|---|
| `tab` / `shift+tab` | next / previous pane |
| `↑↓` or `k`/`j` | move |
| `←`/`→` or `h`/`l` | collapse / expand environment |
| `enter` / `space` | select |
| `g` / `G` | top / bottom |
| `/` | filter apps (id, host, env, provider, target) |
| `R` | poll now |
| `r` / `x` | restart / start-stop (works against the demo provider) |
| `L` / `f` / `c` | focus logs / toggle follow / clear |
| `?` | full key reference |
| `q` / `ctrl+c` | quit |

`d` (deploy) and `F` (files) are wired to honest "arrives in M5/M6" notices.

## Development

```sh
make build     # binary with version metadata
make test      # unit tests
make race      # tests under the race detector
make fmt vet   # formatting and vet
make demo      # build, scaffold ./demo if needed, run the TUI
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
