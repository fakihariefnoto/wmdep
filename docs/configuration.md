# Configuring wmdep

wmdep is configured with plain YAML files. There is no database to seed and no
server-side agent to install: point it at a directory, describe your hosts and
deployments, and it connects over SSH.

This guide covers every field wmdep reads, the validation rules it enforces, and
recipes for the most common service layouts.

- [Where the config lives](#where-the-config-lives)
- [config.yaml](#configyaml)
- [hosts.yaml](#hostsyaml)
- [environments/](#environments)
- [pipelines.yaml](#pipelinesyaml)
- [Validation and safe writes](#validation-and-safe-writes)
- [Recipes](#recipes)
- [Troubleshooting](#troubleshooting)
- [Complete example](#complete-example)

---

## Where the config lives

wmdep looks for a config directory in this order:

1. `--config <dir>` on the command line
2. `$WMDEP_CONFIG_DIR`
3. the nearest **`.wmdep/`** at or above the working directory (stopping at the
   git root)
4. `$XDG_CONFIG_HOME/wmdep`, else `~/.config/wmdep`

A repository can therefore carry its own config and every command picks it up
automatically:

```
my-service/
├── .wmdep/
│   ├── config.yaml
│   ├── hosts.yaml
│   ├── pipelines.yaml
│   └── environments/prod.yaml
└── src/…
```

Create it with `wmdep config init --repo`. A `.wmdep/` that exists but fails to
load is a hard error; wmdep never falls back to the user config, because that
could target the wrong hosts.

A complete directory looks like this:

```
wmdep/
├── config.yaml            defaults, UI preferences, enabled environments
├── hosts.yaml             host inventory (shared by environments)
├── pipelines.yaml         deploy pipelines (shared by deployments)
└── environments/
    ├── prod.yaml
    ├── staging.yaml
    └── dev.yaml
```

`wmdep config init` creates this tree with working examples. Everything after
that is yours to edit, either by hand or from the Deployments/Servers screens.

> `wmdep` is not a state store. The YAML is the source of truth; the only local
> state it keeps is deploy history in `$XDG_STATE_HOME/wmdep/runs.jsonl`.

---

## config.yaml

The root file. All fields are optional except `version`.

```yaml
version: 1

defaults:
  ssh_timeout: 10s          # connection + per-command timeout
  poll_interval: 5s         # how often visible deployments are polled
  log_tail: 500             # lines of history before following
  max_streams_per_host: 3   # reserved: stream cap not enforced yet

# "all", or a list of environment names to load.
environments: [prod, staging]

ui:
  theme: dark               # dark | light
  splash: true              # false skips the menu screen
  confirm_prod: true        # reserved: extra confirmation for prod actions
```

### defaults

| Field | Type | Default | Meaning |
|---|---|---|---|
| `ssh_timeout` | duration | `10s` | Dial and per-command timeout |
| `poll_interval` | duration | `5s` | Status polling interval |
| `log_tail` | integer | `500` | Log history lines fetched before following |
| `max_streams_per_host` | integer | `3` | Reserved; stream capping is not enforced yet |

Durations accept `300ms`, `10s`, `1m30s`, or a bare integer (seconds).

### environments

Accept either the scalar `all` or a list:

```yaml
environments: all
# or
environments: [prod, staging]
```

Environment files not listed are ignored. `--env <name>` further narrows a
single run to one environment.

### Environment variables in config

Values can reference the environment, which keeps one repo config usable on a
laptop and in CI:

```yaml
hosts:
  web-1:
    address: ${WEB1_HOST:?set WEB1_HOST}
    user: ${DEPLOY_USER:-deploy}
    auth: agent
```

- `${NAME}` → the value, or empty when unset
- `${NAME:-default}` → the value, or `default` when unset/empty
- `${NAME:?message}` → fail validation with `message` when unset/empty

Only the braced form is expanded. `$NAME` and `$(command)` are left alone, so
shell snippets in pipeline steps keep working. Write `$${NAME}` for a literal
`${NAME}`.

Expansion happens at read time; the writer always saves the raw placeholder.
Files containing `${…}` are marked read-only in the form editors, so a save can
never bake a resolved hostname into the repository. Edit those by hand or via
`wmdep config edit`.

### ui

| Field | Type | Default | Meaning |
|---|---|---|---|
| `theme` | `dark` / `light` | `dark` | Midnight or light palette |
| `splash` | boolean | `true` | Show the launch menu |
| `confirm_prod` | boolean | `true` | Reserved for prod confirmations |

The splash can also be skipped per run with `--no-splash`, and
`--screen dashboard|manage` chooses the starting screen.

---

## hosts.yaml

Every host referenced by an environment or deployment lives here, keyed by a
short alias.

```yaml
hosts:
  web-1:
    address: 10.0.0.11
    port: 22
    user: deploy
    auth: agent
    tags: [web, prod]

  web-2:
    address: 10.0.0.12
    user: deploy
    auth: key
    key: ~/.ssh/id_ed25519
    tags: [web, prod]

  stage-1:
    address: stage.example.com
    user: ubuntu
    auth: password
    tags: [staging]

  db-1:
    address: 10.0.0.20
    user: deploy
    auth: agent
    proxy_jump: bastion
```

### Fields

| Field | Required | Meaning |
|---|---|---|
| `address` | yes | Hostname or IP |
| `port` | no | Defaults to `22` |
| `user` | yes | SSH user |
| `auth` | no | `agent` (default), `key`, or `password` |
| `key` | with `auth: key` | Path to a private key; `~` is expanded |
| `proxy_jump` | no | Another host **id** in this file to jump through (one hop) |
| `tags` | no | Free-form labels, shown in the UI |

### Authentication

**`agent`** uses the running ssh-agent (`SSH_AUTH_SOCK`). This is the recommended
default; it keeps keys out of files and works with hardware tokens.

**`key`** reads a private key file. Encrypted keys must be unlocked in the
agent, or use agent auth instead.

**`password`** prompts once via `wmdep auth set <host>` and stores the secret in
the OS keyring under service `wmdep`, account `<host id>`. It is never written
to YAML.

```sh
wmdep auth set web-1       # prompt, store in the keyring
wmdep auth forget web-1    # remove it
```

On headless Linux without a secret service, keyring storage fails; use
ssh-agent or a key file there.

### SSH config

If `address` or `port` is omitted, wmdep falls back to `~/.ssh/config` for that
alias (`HostName`, `Port`), and to its `User` when `user` is empty. Explicit
values in `hosts.yaml` always win.

### Host keys

Host keys are verified against `~/.ssh/known_hosts`. Connect once with plain
`ssh <host>` to record a new key. wmdep refuses unknown keys rather than asking
mid-TUI, and it never auto-accepts a changed key.

---

## environments/

One file per environment, named after the environment (`prod.yaml` → `prod`).
The `name` field is optional and defaults to the filename.

```yaml
name: prod

# Hosts this environment includes. Deployments may reference any host from
# hosts.yaml, but listing them here keeps the dashboard scoped and tidy.
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
      expect: "200"
    deploy: api-prod
    tags: [public]

  - id: redis
    host: web-2
    provider: docker
    container: redis
    health:
      type: command
      cmd: redis-cli ping
      expect: PONG
```

### Deployment fields

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Unique within the environment; shown in the UI |
| `host` | yes | Host id from `hosts.yaml` |
| `provider` | yes | `systemd`, `docker`, `compose`, `pm2`, or `command` |
| `unit` | systemd | Unit name, e.g. `api.service` |
| `container` | docker | Container name |
| `project` | compose | Compose project name |
| `service` | compose | Service inside the project |
| `process` | pm2 | PM2 process name |
| `command` | command | Status command; exit 0 means running |
| `workdir` | no | Working directory, shown in detail |
| `health` | no | Probe used to mark a service degraded |
| `deploy` | no | Pipeline name from `pipelines.yaml` |
| `tags` | no | Free-form labels |

Provider-specific fields are required: a systemd deployment without `unit`, or a
docker deployment without `container`, fails validation.

### Health checks

Health checks run **on the server**, so `127.0.0.1` means the server itself.

| Type | Fields | Passes when |
|---|---|---|
| `none` | — | Ignored |
| `http` | `url`, `expect` (optional), `timeout` | `curl` exit 0; with `expect`, the status code matches |
| `tcp` | `address`, `timeout` | A TCP connection succeeds (`bash`'s `/dev/tcp`, so bash must exist) |
| `command` | `cmd`, `expect` (optional), `timeout` | Exit 0; with `expect`, stdout matches |

```yaml
health: { type: http, url: http://127.0.0.1:8080/health, expect: "200" }
health: { type: tcp, address: 127.0.0.1:5432 }
health: { type: command, cmd: "pg_isready -q", expect: "" }
```

A failing probe shows the deployment as **degraded** while the supervisor still
reports it running — useful for catching "process up, app broken".

---

## pipelines.yaml

Pipelines are reusable deploy recipes. A deployment opts in with `deploy: <name>`.

```yaml
pipelines:
  api-prod:
    steps:
      - build:  { local: "go build -trimpath -o dist/api ./cmd/api" }
      - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
      - run:    { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
      - wait:   { health: true, timeout: 60s }
    rollback:
      - run: { remote: "mv /srv/api/api.prev /srv/api/api && systemctl restart api" }
```

### Step types

Exactly one key per step.

| Step | Fields | What it does |
|---|---|---|
| `build` | `local` **or** `remote` + `cmd` | Runs a build locally, or `cd <remote> && <cmd>` on the server |
| `pull` | `remote`, `branch` or `commit` | `git fetch` then `reset --hard origin/<branch>` (or checkout the commit) |
| `upload` | `local`, `remote`, `mode` | SFTP upload, written to a temp path and renamed into place |
| `download` | `remote`, `local` | SFTP download to the operator's machine |
| `run` | `remote`, `timeout` | Shell command on the server; non-zero exits fail the step |
| `wait` | `health: true` and `timeout`, or `sleep` | Polls the app health check, or sleeps |
| `notify` | `message` | Records a message in the run output |

Timeouts default to 10 minutes per step; `run.timeout` and `wait.timeout`
override that.

### Build locally or on the server

Both models are first-class and can be mixed:

```yaml
# artifact built here, uploaded, activated
steps:
  - build:  { local: "go build -o dist/api ./cmd/api" }
  - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
  - run:    { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }

# checkout and build on the server
steps:
  - pull:  { remote: /srv/web, branch: main }
  - build: { remote: /srv/web, cmd: "npm ci && npm run build" }
  - run:   { remote: "systemctl restart web" }
```

### Where the binary is built

This is about **your applications**. wmdep itself is distributed separately as a
normal Go release (GitHub archives, Homebrew, `go install`); it is never built
on your servers.

Pipelines support two artifact models, and they can be mixed in one pipeline:

| | Build locally, upload | Build on the server |
|---|---|---|
| Steps | `build.local` → `upload` → `run` | `pull` → `build.remote` → `run` |
| Needs on laptop | compiler/toolchain, SFTP access | only SSH |
| Needs on server | a shell | git + toolchain, writable checkout |
| Artifact | travels over SFTP | stays on the server |
| Reproducibility | high: one artifact, deployed everywhere | depends on server state and installed versions |
| OS/arch | you must build for the **server's** platform | native |
| Speed | build once + upload time | build time on every server |

**Local build + upload** is the default for compiled services:

```yaml
steps:
  - build:  { local: "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/api ./cmd/api" }
  - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
  - run:    { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
```

- `build.local` runs with `sh -c` on the machine running wmdep, in wmdep's
  working directory. Relative paths such as `dist/api` resolve there.
- wmdep does **not** detect the server's OS/architecture. Set `GOOS`/`GOARCH`
  (or equivalent) yourself; otherwise you can upload a binary the server cannot
  execute.
- `upload` writes to `<remote>.wmdep-tmp`, applies the mode, then renames over
  the target. A dropped connection cannot leave a half-written file at the
  final path.
- Copying is not activating. The `run` step is what moves the file into place
  and restarts the service.
- The artifact is not stored anywhere after the run. If you need history, keep
  versioned filenames or a `.prev` copy in the pipeline.

**Build on the server** is the default for interpreted services:

```yaml
steps:
  - pull:  { remote: /srv/web, branch: main }
  - build: { remote: /srv/web, cmd: "npm ci && npm run build" }
  - run:   { remote: "systemctl restart web" }
```

- `pull` runs `git fetch` and then `git reset --hard origin/<branch>` in the
  checkout. **Local changes on the server in that directory are discarded.**
  Use `commit:` to check out a specific revision instead of a branch.
- `build.remote` runs `cd <remote> && <cmd>` on the server.
- The server needs the full toolchain and dependency cache; builds are usually
  slower and can drift if the server's runtime is not pinned.

**Recommendation:** build compiled binaries locally (cross-compiled), and build
interpreted apps on the server. A pipeline can do both: upload a compiled
sidecar, then pull and build the frontend.

### What wmdep is not

- **Not an artifact registry.** It moves a file from A to B during a deploy; it
  does not store, version, or serve artifacts.
- **Not a build orchestrator.** There is no build matrix, cache, or dependency
  resolution. A `build` step is exactly the command you write.
- **Not a scheduler.** `wmdep deploy` runs one deployment at a time, from the
  TUI or the CLI. It does not coordinate a fleet-wide rollout or queue jobs
  across machines.

### Rollback

If any step fails, wmdep runs the `rollback` steps in order and reports the
failure. Rollback steps use the same step types, so a rollback can restore a
`.prev` artifact, revert a symlink, or restart the previous unit.

The engine does not invent rollbacks. If a pipeline has none, a failed deploy
leaves the host exactly as the failed step left it — so keep a `.prev` copy or
use `git reset` for anything that matters.

### Two-press deploys

From the dashboard or service screen, `d` arms and a second `d` runs. The result
appears in the deploy output pane and in the status bar; failures ring the
terminal bell.

### Headless deploys (Makefiles and CI)

The same pipelines run without the TUI:

```sh
wmdep deploy api --env prod --dry-run   # print the plan
wmdep deploy api --env prod --yes       # execute (prod requires --yes)
```

Exit codes: `0` success, `1` deploy failed, `2` usage or config error. A
production deploy without `--yes` is refused, so CI cannot restart prod by
accident.

A Makefile is a thin wrapper; it is never the config format:

```make
ENV ?= staging
APP ?= api

deploy:
	wmdep deploy $(APP) --env $(ENV) --yes

check:
	wmdep config validate --strict

status:
	wmdep status --env $(ENV)
```

GitHub Actions needs the SSH key and known_hosts, plus any `${…}` values:

```yaml
- run: wmdep config validate --strict
- run: wmdep deploy api --env prod --yes
  env:
    WEB1_HOST: ${{ vars.WEB1_HOST }}
```

Deploys are serialised per host by the engine's step execution; two CI jobs
targeting the same host should still be avoided.

---

## Validation and safe writes

Run validation directly, or let it gate a commit:

```sh
wmdep config validate
wmdep config validate --strict    # warnings become errors
wmdep config doctor               # also checks key files and permissions
```

`validate` checks:

- unknown fields (strict YAML decoding)
- a valid `version`
- every host reference in environments and deployments
- required provider targets (`unit`, `container`, `project` + `service`, …)
- health fields for the chosen probe type
- unique deployment ids within an environment
- pipeline references, and that each step sets exactly one type
- `proxy_jump` targets, self-jumps, and port range
- duplicate or missing required fields

Warnings (not fatal) cover unused hosts, missing key files, and password auth.

Every diagnostic carries `file:line` and a YAML path, for example:

```
environments/prod.yaml:14: app "api" references unknown host "web-9" (at apps.0.host)
```

### Editing from the UI

Add/edit/delete writes back to the same YAML files through a guarded writer:

- edits the parsed node tree, so comments and key order survive
- validates the whole directory before committing; invalid saves are refused
- writes atomically (temp file, fsync, rename) and keeps a `.bak`
- refuses to overwrite a file that changed on disk since it was loaded
- takes a lock so two wmdep instances cannot write at once

If you prefer your editor, `wmdep config edit` opens the directory in `$EDITOR`
and validates when you exit. `wmdep config fmt` normalises indentation without
dropping comments.

---

## Recipes

### Go service under systemd

```yaml
- id: api
  host: web-1
  provider: systemd
  unit: api.service
  workdir: /srv/api
  health: { type: http, url: http://127.0.0.1:8080/health, expect: "200" }
  deploy: api-prod
```

```yaml
api-prod:
  steps:
    - build:  { local: "go build -trimpath -o dist/api ./cmd/api" }
    - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
    - run:    { remote: "cp /srv/api/api /srv/api/api.prev || true; mv /srv/api/api.new /srv/api/api && systemctl restart api" }
    - wait:   { health: true, timeout: 60s }
```

### Node app built on the server

```yaml
- id: web
  host: web-1
  provider: systemd
  unit: web.service
  health: { type: http, url: http://127.0.0.1:3000/health }
  deploy: web-prod
```

```yaml
web-prod:
  steps:
    - pull:  { remote: /srv/web, branch: main }
    - build: { remote: /srv/web, cmd: "npm ci && npm run build" }
    - run:   { remote: "systemctl restart web" }
    - wait:  { health: true, timeout: 90s }
```

### Docker container

```yaml
- id: redis
  host: web-2
  provider: docker
  container: redis
  health: { type: command, cmd: "redis-cli ping", expect: PONG }
```

Restart and stop use `docker restart|stop`; `reload` is systemd-only and reports unsupported for Docker. Logs come from
`docker logs --tail N -f`.

### Compose service

```yaml
- id: db-proxy
  host: web-2
  provider: compose
  project: shop
  service: db-proxy
```

Commands run as `docker compose -p <project> … <service>`.

### PM2 process

```yaml
- id: worker
  host: web-1
  provider: pm2
  process: worker
```

### Escape hatch: command provider

For anything without a native supervisor, describe how to check it:

```yaml
- id: xray
  host: web-1
  provider: command
  command: "systemctl is-active xray.service"
```

Exit 0 is `running`; anything else is `failed`. The command provider cannot
start/stop; use it for observation only.

---

## Troubleshooting

**`host key not found in known_hosts`** — run `ssh <host>` once, or use
`ssh-keyscan` and verify the fingerprint. This is deliberate; wmdep does not
trust unknown keys.

**`SSH_AUTH_SOCK is not set`** — start ssh-agent (`eval "$(ssh-agent -s)"`) or
switch the host to `auth: key`.

**`no keyring password for <host>`** — run `wmdep auth set <host>`.

**`known_hosts is missing`** — create it by connecting once with ssh.

**Everything shows `unreachable`** — check `wmdep config doctor`, then
`wmdep status` (headless) for the raw error. Common causes: wrong user, port,
proxy jump, or a firewall.

**A service is always `degraded`** — the supervisor says running but the health
probe fails. Probe URLs run on the server; `127.0.0.1` is correct there. Try the
probe manually over ssh.

**Edits from the UI refuse to save** — the file changed on disk after wmdep
loaded it. Reload (`R`) and try again, or edit the file and let wmdep reload.

**Unknown fields are rejected** — YAML is decoded strictly so typos do not
silently do nothing. Remove the field or check this guide for the exact name.

---

## Complete example

```
wmdep/
├── config.yaml
├── hosts.yaml
├── pipelines.yaml
└── environments/
    ├── prod.yaml
    └── staging.yaml
```

`config.yaml`

```yaml
version: 1
defaults:
  ssh_timeout: 10s
  poll_interval: 5s
  log_tail: 500
environments: [prod, staging]
ui:
  theme: dark
```

`hosts.yaml`

```yaml
hosts:
  web-1:
    address: 10.0.0.11
    user: deploy
    auth: agent
  web-2:
    address: 10.0.0.12
    user: deploy
    auth: agent
  stage-1:
    address: stage.example.com
    user: ubuntu
    auth: key
    key: ~/.ssh/stage
```

`environments/prod.yaml`

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
    health: { type: command, cmd: "redis-cli ping", expect: PONG }
```

`environments/staging.yaml`

```yaml
name: staging
hosts: [stage-1]
apps:
  - id: web
    host: stage-1
    provider: systemd
    unit: web.service
    deploy: web-prod
```

`pipelines.yaml`

```yaml
pipelines:
  api-prod:
    steps:
      - build:  { local: "go build -o dist/api ./cmd/api" }
      - upload: { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
      - run:    { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
      - wait:   { health: true, timeout: 60s }
    rollback:
      - run: { remote: "mv /srv/api/api.prev /srv/api/api && systemctl restart api" }

  web-prod:
    steps:
      - pull:  { remote: /srv/web, branch: main }
      - build: { remote: /srv/web, cmd: "npm ci && npm run build" }
      - run:   { remote: "systemctl restart web" }
      - wait:  { health: true, timeout: 90s }
```

Validate it:

```sh
wmdep --config ./wmdep config validate
wmdep --config ./wmdep config doctor
```
