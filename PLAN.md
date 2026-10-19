# wmdep — Watch My Deployment

A terminal UI for watching and operating services spread across many servers, over SSH/SFTP.

Status: M1 complete (skeleton); M2 (SSH core) next
Target: Go 1.26+, macOS/Linux (Windows best-effort)

---

## 1. Problem statement

Deployments are rarely one app on one box. Typical reality:

- 3 servers, each running 4 services (systemd or docker)
- 1 staging box running a docker compose stack
- a side VM running a single binary under pm2
- logs, restarts, and deploys each need a different SSH incantation

`wmdep` gives one keyboard-driven surface: see every service on every host, watch health and logs, and run deploys — without leaving the terminal and without a control plane server.

**Non-goal:** replacing Kubernetes, Ansible, or a CI system. `wmdep` is an operator cockpit, not a scheduler.

---

## 2. Product definition

### Core objects

| Object | Meaning |
|---|---|
| **Host** | SSH endpoint: alias, address, port, user, auth, jump host, tags |
| **App** | A manageable unit on a host: systemd unit, docker container, compose project, pm2 process, or raw command |
| **Environment** | Logical grouping of hosts/apps (prod, staging, dev) |
| **Deploy** | A named pipeline (build → upload → activate → verify) targeting one app or a group |
| **Run** | One execution of a deploy or command, recorded with output and result |

### Primary view (shipped in M1)

Three panes plus a log tail, no tabs yet:

```
┌ hosts ──────────┬ apps · prod ─────────────────────────┬ detail ─────────────┐
│ ✕ ▾ prod     3  │ APP      HOST     STATE      UP      │ api                 │
│ ●   web-1    2  │ api      web-1    ▲ degraded -       │ prod · web-1 · systemd│
│ ✕   web-2    1  │ worker   web-1    ● running  2h10m   │ target api.service  │
│ ? ▾ staging  1  │ redis    web-2    ● running  3h04m   │ deploy api-prod     │
│ ?   stage-1  1  │                                     │ health http …       │
├─────────────────┴─────────────────────────────────────┴─────────────────────┤
│ logs · prod/web-1/api · following                                           │
│ 18:22:01 INFO  request handled path=/api/7 status=200 dur=31ms              │
│ 18:22:03 ERROR upstream redis timeout after 2s                              │
├─────────────────────────────────────────────────────────────────────────────┤
│ pane: hosts   tab next pane • ↑/k up • ↓/j down • d deploy • r restart …    │
└─────────────────────────────────────────────────────────────────────────────┘
```

Planned views (not built yet): **Deploys** history (M5) and **Settings** (M6).
The Files browser is a focused mode (M6), not a tab.

### Key interactions

Shipped in M1 (and documented in README):

| Key | Action |
|---|---|
| `tab` / `shift+tab` | next / previous pane |
| `↑↓` `k`/`j` | move |
| `←→` `h`/`l` | collapse / expand environment |
| `enter` / `space` | select; in the tree toggles an environment, in the table jumps to logs |
| `g` / `G` | top / bottom |
| `/` | filter by id, host, env, provider, or target; `esc` clears |
| `R` | poll now |
| `r` / `x` | restart / start-stop |
| `L` / `f` / `c` | focus logs / toggle follow / clear |
| `d` | deploy preview (wired, execution lands in M5) |
| `F` | SFTP browser (wired, lands in M6) |
| `?` / `q` | help / quit |

Planned extras: `y` copy selected log line, `:` command palette
(`deploy api to web-2`), and a global `host:`/`app:`/`env:` filter syntax (M6).
Headless equivalents today: `wmdep status`; `wmdep logs` and `wmdep deploy` follow
in M4/M5.

---

## 3. Architecture

### Stack

Dependencies in `go.mod` today are the first four rows. The SSH, SQLite, and
keyring rows are design decisions that land in M2 and M5, not current deps.

| Concern | Choice | Why |
|---|---|---|
| TUI runtime | `charm.land/bubbletea/v2` | Elm-style model, testable, ecosystem |
| Widgets | `charm.land/bubbles/v2` (table, viewport, spinner, help) | Standard, composable |
| Styling | `charm.land/lipgloss/v2` | Adaptive colors, layouts |
| CLI | `spf13/cobra` | Subcommands for headless mode |
| SSH | `golang.org/x/crypto/ssh` | No external binary dependency |
| SFTP | `github.com/pkg/sftp` | Mature, stream-friendly |
| ssh config | `kevinburke/ssh_config` | Honor `~/.ssh/config`, `Include`, ProxyJump |
| Known hosts | `golang.org/x/crypto/ssh/knownhosts` | Host key verification by default |
| Persistence | SQLite (`modernc.org/sqlite`, pure Go) + YAML config | History/metrics in DB; human-editable config |
| Config | `gopkg.in/yaml.v3`; paths from `$WMDEP_CONFIG_DIR` / `$XDG_CONFIG_HOME` / `~/.config`, resolved directly (no xdg library) | `~/.config/wmdep/` |
| Secrets | OS keyring (`zalando/go-keyring`) or env/agent; never in YAML | Avoid plaintext passwords |
| Logging | `log/slog` to file, TUI-safe | Never write to stdout while TUI runs |

### Process model

Single process. One **connection manager** owns at most one SSH client per host, reused by exec/SFTP/log streams, with keepalive and lazy reconnect. All remote work is async (`tea.Cmd` → messages), never blocking the UI loop.

```
main.go                     # cobra entry; main package at the repo root
internal/
  app/           domain model (Host, App, Status, Key, health)
  config/        multi-file loader, line-indexed diagnostics, validation
  cli/           TUI wiring, status, config validate|init|show, version
  provider/      Provider interface, registry, fake (M1); systemd|docker|compose|pm2|command (M3)
  watch/         polling scheduler, jitter, backoff, filtering
  ui/            root model, panes, theme, keymap
  sshx/          (M2) dial, auth, pool, keepalive, known_hosts, jump hosts
  sftp/          (M2) file ops, streaming transfer, atomic writes
  deploy/        (M5) pipeline engine, steps, hooks, rollback
  store/         (M5) SQLite: runs, status samples, schema
```

### Provider interface

Each app type implements one interface; the UI never shells out directly.

```go
type Provider interface {
    Status(ctx context.Context, app app.App, host app.Host) (app.Status, error)
    Start(ctx context.Context, app app.App, host app.Host) error
    Stop(ctx context.Context, app app.App, host app.Host) error
    Restart(ctx context.Context, app app.App, host app.Host) error
    Logs(ctx context.Context, app app.App, host app.Host, opts LogOpts) (io.ReadCloser, error)
    Metrics(ctx context.Context, app app.App, host app.Host) (app.Metrics, error)
}
```

`host` is passed explicitly rather than looked up inside the provider: the
resolved target is what the connection pool keys on, and tests can substitute a
host without constructing a bundle. This is the interface M1 ships.

Implementations:

- **systemd**: `systemctl show -p ActiveState,SubState,MainPID,ActiveEnterTimestamp`, `systemctl restart`, `journalctl -u <unit> -f -o short-iso --no-pager`
- **docker**: `docker inspect -f '{{json .State}}'`, `docker restart`, `docker logs -f --tail N`, `docker stats --no-stream --format json`
- **compose**: resolve project + service, `docker compose -p <p> ps --format json`, `... restart <svc>`, `... logs -f`
- **pm2**: `pm2 jlist`, `pm2 restart <name>`, `pm2 logs --raw`
- **command**: user-defined status/start/stop/log commands; status parsed via exit code or JSONPath/regex

All remote commands are argv-safe (no shell interpolation of user input) or explicitly escaped.

### Data model (config)

Config is **one file per environment** under `~/.config/wmdep/environments/`. Shared hosts and deploy pipelines live in top-level files and are referenced by name, so two environments can point at the same bastion or the same pipeline definition without copy-paste.

```
~/.config/wmdep/
  config.yaml                  # defaults, ui prefs, which envs are enabled
  hosts.yaml                   # shared host inventory
  pipelines.yaml               # shared deploy pipelines
  environments/
    prod.yaml
    staging.yaml
    dev.yaml
  known_hosts                  # optional override; defaults to ~/.ssh/known_hosts
```

`wmdep` loads every enabled environment on start; `--env prod` narrows to one; `wmdep config validate` checks all files plus cross-references (missing host, unknown pipeline, duplicate app id within an env).

```yaml
# config.yaml
version: 1
defaults:
  ssh_timeout: 10s
  poll_interval: 5s
  log_tail: 500
# Scalar "all", or a list of names. (There is no `enabled:` key; the field is
# the environments list itself, which is what the loader parses.)
environments: [prod, staging]
ui:
  theme: dark
  confirm_prod: true
```

```yaml
# hosts.yaml
hosts:
  web-1:
    address: 10.0.0.11
    port: 22
    user: deploy
    auth: agent              # agent | key | password
    key: ~/.ssh/id_ed25519   # when auth: key
    proxy_jump: bastion
    tags: [web]
  stage-1:
    address: stage.example.com
    user: ubuntu
    auth: key
    key: ~/.ssh/stage
```

```yaml
# environments/prod.yaml
name: prod
hosts: [web-1, web-2]
apps:
  - id: api
    host: web-1
    provider: systemd
    unit: api.service
    workdir: /srv/api
    health:
      type: http
      url: http://127.0.0.1:8080/health
      expect: 200
    deploy: api-prod          # optional pipeline reference

  - id: redis
    host: web-1
    provider: docker
    container: redis
    health: { type: command, cmd: "redis-cli ping", expect: PONG }
```

```yaml
# pipelines.yaml
pipelines:
  api-prod:
    steps:
      - build:   { local: "go build -o dist/api ./cmd/api" }
      - upload:  { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
      - run:     { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
      - wait:    { health: true, timeout: 60s }
    rollback:
      - run: { remote: "mv /srv/api/api.prev /srv/api/api && systemctl restart api" }

  web-prod:
    steps:
      - pull:    { remote: /srv/web, branch: main }          # git fetch + reset --hard origin/main
      - build:   { remote: /srv/web, cmd: "npm ci && npm run build" }
      - run:     { remote: "systemctl restart web" }
      - wait:    { health: true, timeout: 90s }
```

Step types (v1): `build` (local or remote), `pull` (git on server), `upload` (SFTP), `download`, `run` (remote command), `wait` (health/sleep), `notify`. Both deploy models are first-class; a pipeline mixes them freely.

### SQLite schema (initial)

```sql
CREATE TABLE hosts   (id TEXT PRIMARY KEY, last_seen_at INTEGER, fingerprint TEXT);
CREATE TABLE apps    (id TEXT, host_id TEXT, provider TEXT, PRIMARY KEY (id, host_id));
CREATE TABLE runs    (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,            -- deploy | restart | stop | start
  app_id TEXT, host_id TEXT,
  started_at INTEGER, finished_at INTEGER,
  status TEXT,                   -- running | ok | failed | canceled
  exit_code INTEGER, output BLOB, error TEXT
);
CREATE TABLE samples (
  app_id TEXT, host_id TEXT, ts INTEGER,
  state TEXT, cpu REAL, mem_bytes INTEGER,
  PRIMARY KEY (app_id, host_id, ts)
);
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
```

Retention: samples downsampled after 24h, pruned after 7d (configurable).

---

## 4. Key design decisions

1. **No agent on servers.** Everything runs through plain SSH. Works on any box with a shell.
2. **Host key verification on by default.** `knownhosts`; unknown host = explicit prompt, never silent trust. `--insecure-host-key` exists but is loud.
3. **Auth order:** ssh-agent → key → keyring password. Passwords are never written to config; `auth: password` triggers a one-time keyring prompt and stores the secret in the OS keychain under `wmdep:<host>`. Agent/key remain the recommended path; password is supported for hosts that cannot do keys.
4. **Connection pool, not connection-per-action.** One client per host, mutex-guarded, keepalive every 30s, auto-redial with backoff. Cap concurrent log streams per host (e.g. 3) so we do not exhaust `MaxSessions`.
5. **Poll only what is visible.** Overview polls visible apps; collapsed hosts stop polling. Global poll interval floor of 2s, per-host jitter to avoid thundering herd.
6. **Deploys are explicit and auditable.** Always show a plan preview (steps + target hosts), require confirmation for prod (configurable), stream step output, record the run. Dry-run mode prints commands without executing.
7. **Atomic file replacement** on SFTP: upload to `.tmp`, fsync, rename. Optional `.prev` backup for rollback.
8. **Config is source of truth; DB is history.** One file per environment, shared hosts/pipelines referenced by name. Editing YAML by hand stays valid. `wmdep config validate` for CI.
9. **TUI and headless share one core.** The UI calls the same service layer as `wmdep deploy`; no logic in views.
10. **Graceful degradation.** A dead host shows an error badge and retries; it never blocks the rest of the UI.
11. **Deploy step kinds are typed.** `pull`, `build`, `upload`, `download`, `run`, `wait`, `notify` are distinct types with their own validation and timeouts; `run` and `build.cmd` still carry a shell command because that is the operator's escape hatch. Local-build-and-upload and server-side-git-pull coexist in the same pipeline language.

---

## 5. Milestones

Estimates assume one developer, part-time.

### M0 — Decisions & spikes (3–4 days) — done
- Confirm provider priority (assume systemd + docker first), and whether remote builds are in scope (yes — as a step type).
- Spike: Bubble Tea table + viewport layout with resizing.
- Spike: SSH pool with exec + SFTP + log streaming against a local container (`sshd` in Docker).
- Spike: keyring password auth on macOS and headless Linux (secret-service present vs absent).
- Freeze config schema v1: per-environment files, shared `hosts.yaml`, shared `pipelines.yaml`, cross-reference validation.

**Exit:** both spikes run, keyring fallback behavior decided, schema documented.

*Actual:* the layout spike replaced the `bubbles/table` + viewport assumption with a
custom tree and an adaptive table (columns are dropped as width shrinks, because
the stock table truncates instead). The SSH/keyring spikes moved into M2 rather
than blocking the skeleton; config schema v1 is frozen and implemented.

### M1 — Skeleton (4–5 days) — done
- Cobra root + `wmdep` (TUI), `wmdep config validate`, `wmdep status`.
- Multi-file config loader: `config.yaml` + `hosts.yaml` + `pipelines.yaml` + `environments/*.yaml`, defaults, validation, XDG paths, `--config`, `--env`.
- Cross-reference checks with file:line errors (unknown host, unknown pipeline, duplicate app id, unreachable proxy_jump).
- App shell: header, host tree, app table, detail pane, status bar, help overlay, keymap.
- Fake in-memory provider so UI is developable without servers.

**Exit:** `wmdep` starts, renders multiple environments from fixtures, resizes cleanly, `q` quits, `config validate` catches broken refs.

*Actual:* met. The M1 build also ships the log pane, filter mode, start/stop
actions against the provider, and adaptive table columns; those were pulled
forward because the fake provider made them cheap to exercise. The stock
`bubbles/table` could not clip columns, so the table rebuilds its column plan
whenever the pane resizes.

### M2 — SSH core (5–7 days)
- `sshx`: dial with agent/key/password, `~/.ssh/config` + ProxyJump, known_hosts verification, timeouts, keepalive.
- Keyring password flow: prompt on first connect, store under `wmdep:<host>`, clear failure path when no secret service is available (session-only prompt, never written to disk).
- Connection pool with per-host locking and reconnect/backoff.
- Exec helper capturing stdout/stderr/exit code with context cancellation.
- SFTP wrapper: stat, list, read, atomic write, mkdir, rename.
- Error taxonomy surfaced as friendly UI messages (auth failed, host unreachable, timeout, permission denied).

**Exit:** connect to 2+ hosts with agent, key, and password; run commands concurrently; upload/download files; kill a connection and see auto-recovery.

### M3 — Providers & status (4–5 days)
- systemd provider (status, start/stop/restart, logs, metrics via `systemctl show` + `/proc` or `ps`).
- docker provider (inspect, restart, logs, stats).
- `command` provider for escape hatch.
- compose + pm2 providers are **stretch**: if they slip, they land in v0.2, matching the cut line in §8.
- Watch scheduler: visible-only polling, jitter, backoff, aggregated health — already implemented in M1; M3 only adds provider-specific health checks (HTTP/TCP/command probes).

**Exit:** overview shows live state for systemd and docker apps on two hosts; stopping a unit updates within one poll interval.

### M4 — Logs (3–4 days) — partly done
- [done in M1] ring buffer, follow mode, level colouring, per-app cancellation.
- Streaming logs into the pane from real providers, with `journalctl --since` and
  `docker logs --tail` backfill.
- Pause/scroll without dropping the stream, filter (`/`), wrap toggle, timestamps.
- Copy line (`y`), open in `$PAGER`, search within the buffer.

**Exit:** follow logs from 3 apps at once without UI stalls; quitting cancels remote processes (verified with `ps` on server).

### M5 — Deploy engine (6–8 days)
- Typed step engine: `build` (local/remote), `pull` (git fetch+reset on server), `upload`, `download`, `run`, `wait`, `notify`.
- Pipeline executor with per-step timeout, cancel, streaming output.
- Plan preview, confirmation gate (prod required), sequential vs parallel fan-out across hosts.
- Rollback on failure, run history persisted, output viewer.
- Hooks (`before_deploy`, `after_deploy`), env vars, per-step timeouts, secret redaction.
- Dry-run.

**Exit:** deploy a Go binary (local build + upload) to 2 hosts, then deploy a web app (git pull + remote build) to 1 host, fail mid-way deliberately, see rollback and a recorded failed run.

### M6 — Files & polish (4–5 days)
- SFTP browser: tree/list, upload/download with progress, rename/delete (confirm), edit in `$EDITOR` via temp file + atomic write.
- Command palette, global filter, themes (dark/light), configurable keybindings.
- Notifications (desktop + terminal bell) on deploy completion/failure.
- Metrics sparklines (CPU/mem from samples).

**Exit:** operate a full workflow without touching a shell: inspect, tail, upload config, restart, deploy.

### M7 — Hardening & release (4–5 days)
- Unit tests: config (including cross-refs), provider parsers, deploy engine (fake SSH via interface), step validation.
- Integration tests: `sshd` container, real SFTP/exec paths.
- Race detector, `go vet`, `golangci-lint`; fuzz config and provider output parsers.
- Docs: README, config reference, pipeline step reference, keybindings, recipes; demo GIF.
- Release setup adapted from `~/Projects/fakih.net/omssh` (see §11):
  `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`,
  `scripts/release.sh`, `RELEASING.md`, and the `release`/`snapshot` Makefile targets.
- GoReleaser: darwin/linux amd64+arm64, Homebrew tap, checksums, tag-driven changelog.

**Exit:** tagged v0.1.0, installable via `brew install`, docs cover every provider and step type.

Total: roughly **7–9 weeks** part-time, **4–5 weeks** full-time.

---

## 6. Risks & mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| SSH session limits (`MaxSessions`) | Log streams fail on busy hosts | Cap streams per host, multiplex over one client, surface clear error |
| Remote command output unbounded | Memory blowup | Ring buffers, `--tail`, byte caps, backpressure on readers |
| Blocking calls freeze TUI | Bad UX | Every remote call in `tea.Cmd` with context + timeout |
| Password auth complexity | Scope creep | Keyring by default, session-only prompt when no secret service, never plaintext on disk |
| Config drift between hosts | Wrong-target deploys | Explicit plan preview listing host+app; environment confirmation |
| Provider output formats change | Parser breakage | Prefer `--format json` / `-o json`; fallback parsers with tests |
| Secrets in logs | Security | Redact env values, never log commands containing secrets, keyring only |
| Host key changes | MITM vs annoyance | Hard fail + instructions to update known_hosts; no auto-accept |
| Multi-file config gets out of sync | Broken refs at runtime | Cross-reference validation with file:line, `config validate` in CI |
| Remote build failures pollute server | Half-built artifacts live on prod | Build in a temp/staging dir, atomic promote, `.prev` backup, rollback step |

---

## 7. Testing strategy

- **Unit:** config validation, provider output parsers (golden files), deploy step state machine, keymap dispatch, log ring buffer.
- **Integration:** Docker Compose test fixture: `openssh-server` + systemd-like fake + docker socket. Exercise dial, exec, SFTP, log cancel.
- **UI:** model-level tests drive `Update` with `WindowSizeMsg` and `KeyPressMsg`
  and assert on the rendered frame (ANSI stripped). Golden frames are checked at
  80x24 and 140x40 for layout rules — line count never exceeds height, no line
  exceeds width. No `teatest` dependency; the model is the unit.
- **Manual matrix:** macOS/Linux, tmux/screen, 16-color and truecolor terminals, 1 host / 10 hosts, 100 apps.

---

## 8. v0.1 scope (cut line)

In:
- systemd + docker providers (`command` escape hatch included)
- agent/key/password auth (keyring), known_hosts verification
- per-environment config files + shared hosts/pipelines
- overview, logs, deploy with rollback, run history
- deploy steps: build (local+remote), pull, upload, download, run, wait, notify
- SFTP upload/download (deploy steps; the interactive browser is M6)
- headless `status`, `logs`, `deploy`
- config validation, docs, brew/linux binaries

Out (v0.2+):
- compose/pm2 providers (stretch in M3), jump-host chains beyond one hop, interactive file editing, metrics graphs, multi-user/audit, remote build agents, Windows support.

---

## 9. Resolved decisions

1. **Deploy model:** both — local build + artifact upload, and server-side `git pull` + build, as typed pipeline steps. Mixable in one pipeline.
2. **Auth:** ssh-agent + key files + password via OS keyring. Keyring entry `wmdep:<host>`; no secret service = session-only prompt.
3. **Config:** one file per environment, plus shared `hosts.yaml` and `pipelines.yaml`, cross-referenced by name and validated.
4. **Scope:** remote builds are in v0.1 (as a step type), because the `pull` + `build` model depends on them.
5. **Config writes:** wmdep is read-only. `wmdep config init` scaffolds starter files; after that the YAML is the operator's, hand-edited. No TUI add-host wizard in v0.1.
6. **Deploy concurrency:** one deploy at a time per host, enforced by a per-host deploy lock. A second pipeline targeting a busy host is queued and shown as such.

---

## 10. Repository, CI, and release

**Repository:** <https://github.com/fakihariefnoto/wmdep>
(SSH: `git@github.com:fakihariefnoto/wmdep.git`). The Go module path matches it:
`github.com/fakihariefnoto/wmdep`.

### Git workflow

- `main` is always releasable; feature work happens on short-lived branches.
- Commits follow Conventional Commits — the release changelog groups on
  `feat:`, `fix:`, and everything else (see the GoReleaser config).
- Tags are `vX.Y.Z`, cut only from a green `main`. The tag is the single source
  of truth for the version; nothing version-like is stored in a file.
- `origin` is `https://github.com/fakihariefnoto/wmdep.git`; `main` tracks it.

### Repository state (done)

The repository already existed with a GitHub-generated README, MIT `LICENSE`, and
Go `.gitignore`. It is now wired up:

- Module path is `github.com/fakihariefnoto/wmdep`; every import and the Makefile
  ldflags were updated in the same pass.
- `git init -b main`, `origin` added, M1 committed, fetched, merged with the
  remote initial commit, and pushed:
  - keep the remote `LICENSE` (MIT, "Fakih Arief Noto") and its Go `.gitignore`
    template, extended with wmdep build output;
  - keep this repository's README, which supersedes the two-line stub.
- `go build ./...` and `go test ./...` verify the rename.

No `gh` CLI is installed on this machine, so repo creation and secret
configuration happen in the GitHub web UI or after installing `gh`.

### Release toolchain: adapt `~/Projects/fakih.net/omssh`

`omssh` already has a working three-channel release. wmdep copies it rather than
inventing a new one. Files to bring over and adapt:

| omssh file | Adaptation for wmdep |
|---|---|
| `.goreleaser.yaml` | `project_name: wmdep`; build `main: .` (main package is at the repo root, not `./cmd/wmdep`); binary `wmdep`; `owner: fakihariefnoto`, `name: wmdep`; Homebrew formula `wmdep.rb`; brew test runs `wmdep version` |
| `.github/workflows/ci.yml` | unchanged shape: Go `1.26.x`, tidy-diff check, build, gofmt, vet, `go test -race -count=1`, golangci-lint, ubuntu+macos matrix |
| `.github/workflows/release.yml` | tag `v*` and `workflow_dispatch`; test job, then `goreleaser/goreleaser-action@v6`; needs `TAP_GITHUB_TOKEN` |
| `scripts/release.sh` | `REPO_SLUG=fakihariefnoto/wmdep`; `TOKEN_SERVICE=wmdep-tap-token`; formula name `wmdep`; keeps the same bump/tag/push/token-resolution flow |
| `RELEASING.md` | same first-release checklist, s/omssh/wmdep/ |
| `Makefile` | add `snapshot` (`goreleaser release --snapshot --clean`) and `release` (`./scripts/release.sh $(V)`) targets |
| `.golangci.yml` | copy version-2 config as the starting lint set |

Key differences from omssh that the config must get right:

1. **Build target is the repo root.** `main: .` and binary `wmdep`.
2. **Version variables live in `internal/cli`**, not `main`:
   `-X github.com/fakihariefnoto/wmdep/internal/cli.Version={{.Version}}`
   (plus `.Commit` and `.Date`). The Makefile already uses these paths.
3. **Pure Go, no cgo**, so `CGO_ENABLED=0` cross-compiles every target from one
   machine. Ship darwin/linux, amd64/arm64; Windows stays unadvertised until the
   terminal/keyring paths are tested.
4. **Homebrew tap** is `fakihariefnoto/homebrew-tap`, directory `Formula`,
   homepage `https://github.com/fakihariefnoto/wmdep`.

### Release flow (once M7 lands)

```sh
# local, with a token that can push to the tap
make release V=minor        # test → tag → push → goreleaser → tap formula
# or, re-run without re-tagging:
gh workflow run release -f tag=v0.1.0
```

First-release checklist (the repository itself already exists and is pushed):

1. Confirm `fakihariefnoto/homebrew-tap` exists and has a `Formula/` directory.
2. Add `TAP_GITHUB_TOKEN` in **Settings → Secrets and variables → Actions**: a
   fine-grained PAT with `Contents: read and write` on the tap. The default
   `GITHUB_TOKEN` cannot push to a second repository.
3. Confirm the release workflow is enabled for tag pushes (`v*`).
4. Cut `v0.1.0` through `make release V=0.1.0` (or tag and push by hand) so the
   tag points at pushed code on `main`.

Install channels produced: GitHub Releases archives + `checksums.txt`, the
Homebrew tap, and `go install github.com/fakihariefnoto/wmdep@latest`.

---

## 11. Current state (M1)

Implemented and tested:

- `internal/app` — domain model (Host, App, Status, Key, provider kinds, health).
- `internal/config` — multi-file loader (`config.yaml`, `hosts.yaml`, `pipelines.yaml`, `environments/*.yaml`), strict YAML (unknown fields rejected), `file:line` diagnostics with YAML paths, full cross-reference validation, defaults with fallbacks.
- `internal/provider` — `Provider` interface, registry, deterministic in-memory `Fake` (status drift, logs, actions).
- `internal/watch` — visible-only poll scheduler: per-host concurrency cap, jitter, exponential backoff (2×→capped 5m), `Build` filtering by env/host/query.
- `internal/ui` — Bubble Tea v2 app: host tree with state aggregation, app table with live status, detail pane, log pane with level colouring, full help overlay, filter mode, focus cycling, resize handling. All remote work runs in `tea.Cmd`.
- `internal/cli` — `wmdep` (TUI), `status`, `config validate|init|show`, `version`; `--config`, `--env`, `--demo`.

Tests: config loader and diagnostics, poller filtering/backoff, UI layout and key handling, CLI end-to-end (init → validate → status → show). All pass under `-race`.

Deviations from the original plan, and why:

- **Charm modules moved to `charm.land/*`** and require Go 1.26. The toolchain auto-upgrades; `go.mod` says `go 1.26.0`.
- **`--demo` is currently always on.** M1 has no SSH provider, so the header shows a `demo` chip rather than pretending. M2 registers the real providers and leaves `--demo` as the fake path.
- **Deploy and Files keys** (`d`, `F`) show explicit "arrives in M5/M6" notices instead of being silent.
- **The log pane is already live** (M4 work) because the fake provider could feed it cheaply; M4 replaces the fake stream with `journalctl`/`docker logs` and adds search/pager.
- **Tests are model-level, not `teatest`.** Driving `Update` directly and
  asserting on the rendered frame caught the real bugs (lipgloss sizing,
  column/cell mismatch) without a terminal emulator.

Known gaps to close in M2/M3:

- Nothing dials SSH yet; `status` and the TUI both use the fake provider.
- `action` (restart/stop/start) is only exercised against the fake.
- No persistence: run history and metric samples (SQLite) are M5.
- No headless `logs` or `deploy` yet (M4/M5); `status` is the only headless query.
- `y` copy-line and the `:` command palette are not implemented (M6).
- The deploy key (`d`) and files key (`F`) are placeholders with explicit notices.

