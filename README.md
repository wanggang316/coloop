# coloop

A lightweight, stable local **scheduled-task manager** for macOS and Linux.
Single Go binary, no database, no Node build chain — it schedules local
scripts and agent invocations, shows a task list / detail / live-log UI, and
alerts to Feishu and Telegram.

## Features

- **Two execution modes** — run a local **shell** script, or invoke an
  **agent** through a reusable **runner** (a named command template such as
  `claude -p "{{.Prompt}}"`, defined once and selected by name), each with its
  own `working_dir` and `env`.
- **Schedule or trigger** — standard 5-field cron expressions for automatic
  runs, plus manual one-click / one-command triggers.
- **Observable** — embedded web UI with three views (task list, task detail,
  live log) backed by a JSON REST + SSE API. Claude Code agent runs
  (`--output-format stream-json`) render as an event table — a run-summary
  header (status, duration, tool-call / event counts, cwd), an activity
  minimap, and one expandable badge row per event (tool calls, agent
  messages, results) — with a one-click switch back to the raw line view.
- **Alerts** — named Feishu (webhook or CLI) and Telegram channels defined
  once and selected per task, gated by an
  `on_failure` / `on_success` / `always` / `never` policy.
- **Lightweight & stable** — one cross-compiled binary, config in a single
  YAML file, run records on the filesystem (no DB, no schema, no migrations).
  Loopback-only by default.

## Architecture

A single headless engine (cron scheduler + single-flight dispatch + per-run
live-log pub/sub) exposes one **JSON REST + SSE** contract. The Web UI and the
CLI are both just clients of that API.

```
clients   Web UI · CLI ──┐
                          │  JSON REST + SSE   (the only seam)
engine    scheduler · executors · run store · notifiers
state     ~/.coloop/    config.yaml + runs/<task>/<id>/{meta.json,output.log}
```

## Install

Build from source and install in one step (requires Go 1.26+):

```sh
git clone https://github.com/wanggang316/coloop.git
cd coloop
./scripts/install.sh             # build + install to /usr/local/bin, seed ~/.coloop/config.yaml
./scripts/install.sh --service   # ...and enable start-at-boot (launchd / systemd)
```

No Go toolchain on the target? Run `make dist` once on a machine that has Go,
copy the repo over, and install the matching prebuilt binary with
`./scripts/install.sh --no-build`.

| flag | meaning |
|------|---------|
| `--prefix DIR` | install location (binary goes in `DIR/bin`; default `/usr/local`) |
| `--config PATH` | config to seed and point the service at (default `~/.coloop/config.yaml`) |
| `--service` | also install + enable the autostart service |
| `--no-build` | skip building; use a prebuilt binary from `dist/` |

The installer is idempotent — re-run it to upgrade the binary; an existing
config is never overwritten. Remove everything with `./scripts/uninstall.sh`
(add `--purge` to also delete `~/.coloop`).

## Build

For development. Requires Go 1.26+.

```sh
make build            # -> bin/coloop
make dist             # cross-compiled binaries for darwin/linux amd64+arm64
make test             # unit + integration tests
make race             # tests under the race detector
```

## Configure

Copy the example and edit it:

```sh
mkdir -p ~/.coloop
cp config.example.yaml ~/.coloop/config.yaml
```

Key fields (see `config.example.yaml` for the full annotated reference):

| field | meaning |
|-------|---------|
| `listen` | HTTP bind address (default `127.0.0.1:8765`, loopback only) |
| `shell` | default interpreter for shell tasks — just the path, e.g. `/bin/sh` |
| `data_dir` | where run records/logs go (default: the config's directory) |
| `tasks[].type` | `shell` or `agent` |
| `tasks[].cron` | 5-field cron; empty = manual only |
| `tasks[].working_dir` | cwd for the command (the cron gotcha — set this) |
| `tasks[].timeout` | e.g. `30s`, `10m`, `1h` (default `1h`) |
| `tasks[].alert` | `on_failure` (default) / `on_success` / `always` / `never` |

The daemon **hot-reloads** the config on file change; an invalid edit is
rejected and the running config is kept.

## Run

```sh
coloop --config ~/.coloop/config.yaml serve
# open http://127.0.0.1:8765
```

### CLI

```
coloop [--config PATH] <command>

  serve                       run the daemon (scheduler + API + UI)
  ls                          list tasks and their last status
  show <task>                 print a task's definition and recent runs
  add  --name N --type T …    create a task from flags
  edit <task> [flags]         change a task's fields (only the flags you pass)
  rm   <task>                 delete a task (run history is kept)
  run <task>                  trigger a task, print the new run id
  logs [-f] <task> [id]       show a run's log (latest if id omitted; -f follows)
  validate [path]             validate a config file offline
  reload                      ask the daemon to reload its config
```

`add` / `edit` take the task fields as flags — `--name --type --cron
--working-dir --command --runner --prompt --cmd-tmpl --env KEY=VAL (repeatable)
--timeout --alert --notifiers a,b --enabled`. `edit` applies only the flags you
pass, leaving the rest as-is. Examples:

```sh
coloop add --name nightly-build --type shell --cron "0 3 * * *" \
             --working-dir ~/proj --command ./build.sh --alert on_failure
coloop edit nightly-build --cron "0 4 * * *"
coloop rm nightly-build
```

The client commands target the daemon over HTTP, resolving the address from
`--addr`, `$COLOOP_ADDR`, or the `listen` field of `--config`.

### HTTP API

```
GET    /healthz
GET    /api/tasks
POST   /api/tasks                          # create a task
GET    /api/tasks/{name}
PUT    /api/tasks/{name}                    # replace a task
DELETE /api/tasks/{name}                    # delete a task
POST   /api/tasks/{name}/run
GET    /api/tasks/{name}/runs[?limit=N]
GET    /api/tasks/{name}/runs/{id}
GET    /api/tasks/{name}/runs/{id}/log     # full log (text)
GET    /api/tasks/{name}/runs/{id}/stream  # live log (SSE)
POST   /api/reload
```

## Deploy

`./scripts/install.sh --service` is the easy path: it renders and enables a
per-user **launchd** agent (macOS) or **systemd** user service (Linux) that runs
`coloop serve` with restart-on-crash and start-at-boot.

To wire it up by hand, see the header of the template for your platform:

- **macOS (launchd):** [`deploy/com.coloop.coloop.plist`](deploy/com.coloop.coloop.plist)
- **Linux (systemd):** [`deploy/coloop.service`](deploy/coloop.service)

To migrate a machine, copy the binary and the `~/.coloop/` directory — the
schedule is rebuilt from `config.yaml`.

## Security

The API has no authentication and triggering a task runs the command you
configured. The default `listen` is therefore **loopback only**. Binding a
non-loopback address exposes unauthenticated command execution to the network —
only do so behind your own auth/reverse-proxy. The daemon warns at startup if
`listen` has an empty host (binds all interfaces).
