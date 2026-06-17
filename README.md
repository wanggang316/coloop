# the-loop

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
  live log) backed by a JSON REST + SSE API.
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
state     ~/.the-loop/    config.yaml + runs/<task>/<id>/{meta.json,output.log}
```

## Build

Requires Go 1.26+.

```sh
make build            # -> bin/the-loop
make dist             # cross-compiled binaries for darwin/linux amd64+arm64
make test             # unit + integration tests
make race             # tests under the race detector
```

## Configure

Copy the example and edit it:

```sh
mkdir -p ~/.the-loop
cp config.example.yaml ~/.the-loop/config.yaml
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
the-loop --config ~/.the-loop/config.yaml serve
# open http://127.0.0.1:8765
```

### CLI

```
the-loop [--config PATH] <command>

  serve                       run the daemon (scheduler + API + UI)
  ls                          list tasks and their last status
  run <task>                  trigger a task, print the new run id
  logs [-f] <task> [id]       show a run's log (latest if id omitted; -f follows)
  validate [path]             validate a config file offline
  reload                      ask the daemon to reload its config
```

The client commands target the daemon over HTTP, resolving the address from
`--addr`, `$LOOP_ADDR`, or the `listen` field of `--config`.

### HTTP API

```
GET  /healthz
GET  /api/tasks
GET  /api/tasks/{name}
POST /api/tasks/{name}/run
GET  /api/tasks/{name}/runs[?limit=N]
GET  /api/tasks/{name}/runs/{id}
GET  /api/tasks/{name}/runs/{id}/log       # full log (text)
GET  /api/tasks/{name}/runs/{id}/stream    # live log (SSE)
POST /api/reload
```

## Deploy

- **Linux (systemd):** see header of [`deploy/the-loop.service`](deploy/the-loop.service).
- **macOS (launchd):** see header of [`deploy/com.theloop.the-loop.plist`](deploy/com.theloop.the-loop.plist).

Both run `the-loop serve` with restart-on-crash and start-at-boot. To migrate a
machine, copy the binary and the `~/.the-loop/` directory — the schedule is
rebuilt from `config.yaml`.

## Security

The API has no authentication and triggering a task runs the command you
configured. The default `listen` is therefore **loopback only**. Binding a
non-loopback address exposes unauthenticated command execution to the network —
only do so behind your own auth/reverse-proxy. The daemon warns at startup if
`listen` has an empty host (binds all interfaces).
