package cli

// Starter files written by `wmdep config init`. They are intentionally small
// but exercised by the loader, so a fresh install validates cleanly.
const starterConfig = `# wmdep configuration
#
# The config directory is split so shared things are written once:
#
#   config.yaml          this file: defaults and which environments load
#   hosts.yaml           the host inventory, referenced by name
#   pipelines.yaml       deploy pipelines, referenced by name
#   environments/*.yaml  one file per environment

version: 1

defaults:
  ssh_timeout: 10s
  poll_interval: 5s
  log_tail: 500
  max_streams_per_host: 3

# "all", or a list of environment names to load:
#   environments: [prod, staging]
environments: all

ui:
  theme: dark
  confirm_prod: true
`

const starterHosts = `# Host inventory. Everything here is referenced by name from environments.
#
# auth:
#   agent     use ssh-agent (recommended)
#   key       use the private key in "key"
#   password  prompt once, then store the secret in the OS keyring
#             (never in this file)

hosts:
  web-1:
    address: 10.0.0.11
    port: 22
    user: deploy
    auth: agent
    tags: [web]

  web-2:
    address: 10.0.0.12
    port: 22
    user: deploy
    auth: agent
    tags: [web]

  stage-1:
    address: stage.example.com
    user: ubuntu
    auth: key
    key: ~/.ssh/stage
    tags: [staging]
`

const starterPipelines = `# Deploy pipelines. An app opts in with "deploy: <pipeline name>".
#
# Step types: build (local or remote), pull, upload, download, run, wait, notify.
# Both deploy models are first-class: build an artifact here and upload it, or
# pull and build on the server.

pipelines:
  api-prod:
    steps:
      - build:   { local: "go build -o dist/api ./cmd/api" }
      - upload:  { local: dist/api, remote: /srv/api/api.new, mode: "0755" }
      - run:     { remote: "mv /srv/api/api.new /srv/api/api && systemctl restart api" }
      - wait:    { health: true, timeout: 60s }
    rollback:
      - run:     { remote: "mv /srv/api/api.prev /srv/api/api && systemctl restart api" }

  web-prod:
    steps:
      - pull:    { remote: /srv/web, branch: main }
      - build:   { remote: /srv/web, cmd: "npm ci && npm run build" }
      - run:     { remote: "systemctl restart web" }
      - wait:    { health: true, timeout: 90s }
`

// starterStaging is a second environment so the starter config demonstrates
// multi-environment layouts (and so host inventory has no unused entries).
const starterStaging = `# Each environment is one file. Enable or disable them in config.yaml.

name: staging
hosts: [stage-1]

apps:
  - id: web
    host: stage-1
    provider: systemd
    unit: web.service
    health:
      type: http
      url: http://127.0.0.1:8080/health
      expect: "200"
`

const starterEnv = `# One file per environment. Apps name a host from hosts.yaml.

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
      expect: "200"
    deploy: api-prod

  - id: worker
    host: web-1
    provider: systemd
    unit: worker.service

  - id: redis
    host: web-2
    provider: docker
    container: redis
    health:
      type: command
      cmd: "redis-cli ping"
      expect: PONG

  - id: web
    host: web-2
    provider: systemd
    unit: web.service
    deploy: web-prod
`
