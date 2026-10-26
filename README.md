# wmdep — Watch My Deployment

**A terminal cockpit for services spread across many servers.**

wmdep gives you one keyboard-driven screen for every service you run: live
status, CPU and memory, logs, and deploys — over plain SSH and SFTP, with no
agent to install and no control plane to operate.

[![CI](https://github.com/fakihariefnoto/wmdep/actions/workflows/ci.yml/badge.svg)](https://github.com/fakihariefnoto/wmdep/actions/workflows/ci.yml)
[![Go 1.26+](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

```
1 Dashboard  2 Deployments  3 Servers  4 Scan  5 Settings     1-5 open · tab host · ↑↓ service
╭ fleet ──────────────────────────────────────────────────────────────╮
│ 7 deployments   ● 5 up   ✕ 1 down   ▲ 1 degraded                    │
│ CPU 14% avg   MEM 1.2G   3 hosts   1 unreachable                    │
╰─────────────────────────────────────────────────────────────────────╯
╭ web-1 ──────────────────────────────────────────────────────────────╮
│ 10.0.0.11:2222 · deploy  [prod]  ▲ degraded  CPU 9%  MEM 380M  2 dep │
╰─────────────────────────────────────────────────────────────────────╯
╭ api ────────────────────────────╮ ╭ worker ─────────────────────────╮
│ ● running   2h10m               │ │ ● running   8d11h               │
│ CPU 12%   MEM 340M              │ │ CPU 4%   MEM 40M                │
│ api-prod                        │ │ no pipeline                     │
╰─────────────────────────────────╯ ╰─────────────────────────────────╯
```

## Why wmdep

Most deployment dashboards assume you can install an agent, run a control
plane, or adopt Kubernetes. wmdep assumes only two things: your servers expose
SSH, and you like the terminal.

- **No agent on the servers.** Everything runs through standard SSH.
- **No central server.** Your laptop is the control plane.
- **Config is files.** YAML you can review, diff, and commit; the UI edits the
  same files without losing your comments.
- **Built for many small deployments**, not one big cluster.

## Features

- **Fleet dashboard** grouped by host, with per-host and per-service CPU, memory,
  uptime, and health.
- **Multiple providers**: systemd, Docker, Compose, PM2, and a raw-command
  escape hatch.
- **Live logs** with follow/pause and level colouring.
- **Deploy pipelines** that build locally and upload, or pull and build on the
  server — with health waits and rollback.
- **Host discovery**: scan a server and import the services it actually runs.
- **Safe config editing** from the UI: validated writes, comment-preserving,
  atomic, with backups.
- **Headless commands** for scripts and CI: `status`, `config validate`,
  `config doctor`.

## Installation

**Homebrew** (after the first tagged release):

```sh
brew install fakihariefnoto/tap/wmdep
```

**Prebuilt binaries:** every release publishes `tar.gz` archives for macOS and
Linux (amd64/arm64) plus `checksums.txt` on the
[Releases page](https://github.com/fakihariefnoto/wmdep/releases).

**Go install:**

```sh
go install github.com/fakihariefnoto/wmdep@latest
```

**Build a checkout** (Go 1.26 or newer):

```sh
git clone https://github.com/fakihariefnoto/wmdep
cd wmdep
make build
./wmdep version
```

## Quick start

```sh
wmdep config init       # ~/.config/wmdep, or .wmdep/ inside a repo
wmdep config init --repo # explicitly write <git-root>/.wmdep
wmdep config doctor     # checks keys, permissions, and layout
wmdep config validate   # validates every file and reference
wmdep status            # one-shot status table (no TUI)
wmdep                   # splash → dashboard → manage
```

Point it at a different directory with `--config` or `$WMDEP_CONFIG_DIR`:

```sh
wmdep --config ./wmdep config init
wmdep --config ./wmdep
```

Want to try the UI without any servers? Use the in-memory demo provider:

```sh
wmdep --demo
```

`--no-splash` and `--screen dashboard|manage` control where the TUI starts.

## Navigation

| Key | Action |
|---|---|
| `1`–`5` | Dashboard · Deployments · Servers · Scan · Settings |
| `tab` / `shift+tab` | Move focus between hosts |
| `↑↓` `j`/`k` | Choose a service within the focused host |
| `enter` | Open the focused service |
| `r` / `x` / `u` | Restart / start-stop / reload |
| `d` then `d` | Deploy the selected service |
| `l` | Open the service and its logs |
| `?` / `q` | Help / quit |

Inside a service view, the action bar is explicit: restart, start/stop, reload,
deploy, edit, files, follow logs, back.

## Configuration

wmdep is configured with four kinds of YAML:

| File | Purpose |
|---|---|
| `config.yaml` | defaults, UI preferences, enabled environments |
| `hosts.yaml` | host inventory: address, user, auth, jump host |
| `pipelines.yaml` | reusable deploy recipes |
| `environments/*.yaml` | which deployments live where |

Minimal example:

```yaml
# environments/prod.yaml
name: prod
hosts: [web-1]
apps:
  - id: api
    host: web-1
    provider: systemd
    unit: api.service
    health: { type: http, url: http://127.0.0.1:8080/health, expect: "200" }
    deploy: api-prod
```

Repo-local config is picked up automatically from `.wmdep/` (nearest, stopping
at the git root), and values can use `${VAR}` / `${VAR:-default}` /
`${VAR:?message}` so the same files work on a laptop and in CI.

**Full reference:** [docs/configuration.md](docs/configuration.md) — every field,
provider, health check, pipeline step, validation rule, headless deploy, recipe,
and troubleshooting entry.

## Commands

| Command | Description |
|---|---|
| `wmdep` | Open the TUI |
| `wmdep deploy <app>` | Run a deployment's pipeline (headless; `--dry-run`, `--yes`) |
| `wmdep status` | Poll every deployment once and print a table |
| `wmdep config init` | Scaffold a starter config directory |
| `wmdep config validate` | Validate files and cross-references (`--strict`) |
| `wmdep config doctor` | Check keys, permissions, empty environments |
| `wmdep config show` | Print the resolved hosts, apps, and pipelines |
| `wmdep config fmt` | Normalise YAML without losing comments |
| `wmdep config edit` | Open the directory in `$EDITOR`, validate on exit |
| `wmdep auth set <host>` | Store a password in the OS keyring |
| `wmdep auth forget <host>` | Remove a stored password |
| `wmdep version` | Print version information |

Global flags: `--config`, `--env`, `--demo`, `--no-splash`, `--screen`.

## Releases

wmdep ships through three channels in one pass, driven by
[GoReleaser](https://goreleaser.com):

1. **GitHub Releases** — archives and `checksums.txt`
2. **Homebrew tap** — `Formula/wmdep.rb` committed to
   [`fakihariefnoto/homebrew-tap`](https://github.com/fakihariefnoto/homebrew-tap)
3. **`go install`** — via the module path

Maintainers release from a clean `main`:

```sh
make release V=minor     # or patch | major | 0.2.0
```

The script runs checks (clean tree, on `main`, not ahead of origin, tag unused),
tags, pushes, and lets GoReleaser publish both the GitHub release and the tap
formula. `TAP_GITHUB_TOKEN` is only needed to push to the tap; the script falls
back to the keychain or the credential git already stores. The same flow can run
in GitHub Actions by pushing a tag or dispatching the **release** workflow.

Verify the artifacts without publishing:

```sh
make snapshot
```

The complete checklist, credentials model, failure recovery, and the `brews`
deprecation note live in [RELEASING.md](RELEASING.md).

## How it works

```
laptop                                servers
┌──────────────────────────┐          ┌──────────────────────┐
│ TUI (Bubble Tea)         │          │ systemd              │
│   dashboard / lists      │  SSH     │ docker / compose     │
│   service view / logs    │ ───────▶ │ pm2                  │
│   editors / scan         │  SFTP    │ command provider     │
│                          │          │                      │
│ config loader + writer   │          │ no agent             │
│ provider registry        │          │                      │
│ deploy engine            │          │                      │
└──────────────────────────┘          └──────────────────────┘
```

- One SSH connection per host, reused for status, actions, logs, and transfers.
- Status polling only covers what is on screen; failures back off.
- Deploys are explicit: steps run with timeouts, health waits gate success, and
  rollback steps run on failure.
- Host keys are verified against `~/.ssh/known_hosts`; wmdep refuses unknown
  keys instead of silently trusting them.

## Project status

wmdep is usable today against real servers. The core is tested; some polish is
still in progress.

| Area | Status |
|---|---|
| SSH transport, auth, known_hosts, pooled connections | ✅ |
| systemd / Docker / Compose / PM2 / command providers | ✅ |
| Dashboard, service view, logs, searchable lists | ✅ |
| Config validation, doctor, safe editing, host scan | ✅ |
| Deploy engine, health waits, rollback, output pane | ✅ |
| Headless status / deploy / validate / doctor / fmt / edit | ✅ |
| Releases: GoReleaser → GitHub + Homebrew tap + go install | ✅ configured |
| SFTP browser | List and download ✅, upload/rename/delete planned |
| Run history | JSONL + Activity card ✅, SQLite planned |
| Desktop notifications | Terminal bell ✅, native popups planned |
| Windows | Not supported (macOS/Linux only) |

See [PLAN.md](PLAN.md) for the design and milestone history.

## Development

```sh
make build       # build ./wmdep with version metadata
make test        # unit tests
make race        # tests under the race detector
make lint        # gofmt check + go vet
make demo        # scaffold ./demo, run the TUI on fake data
make snapshot    # GoReleaser build into dist/ without publishing
```

Layout:

```
main.go                CLI entry
internal/app           domain model
internal/config        YAML loading, validation, writer, formatting
internal/sshx          SSH pool, exec, streaming, SFTP
internal/provider      provider interface + fake
internal/provider/sshprov  systemd, docker, compose, pm2, command, discovery
internal/watch         poll scheduler (visible-only, backoff)
internal/deploy        pipeline engine
internal/store         deploy run history
internal/ui            splash, dashboard, lists, service view, editors
internal/cli           cobra commands
```

Guidelines: keep remote work inside `tea.Cmd`, never block `Update`; add a test
for every parser and every user-visible state; `gofmt`, `go vet`, and `go test
-race ./...` must stay clean.

## Contributing

Issues and pull requests are welcome. A useful report includes:

- what you expected and what happened,
- `wmdep version`,
- the output of `wmdep config doctor` (redact hostnames and paths),
- the relevant config snippet, with secrets removed.

For code, please keep commits in Conventional Commits format; the release
changelog is generated from them.

## License

[MIT](LICENSE).
