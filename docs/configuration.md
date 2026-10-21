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
3. `$XDG_CONFIG_HOME/wmdep`
4. `~/.config/wmdep`

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
