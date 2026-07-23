---
name: coloop-cli
description: Operate coloop from the terminal — list, inspect, create, edit, delete, trigger, and tail scheduled tasks against a running daemon. Use when running `coloop` commands, managing tasks from the terminal instead of the Web UI or editing config.yaml by hand, triggering a run, following a run's log, or scripting against the daemon's HTTP API.
---

# coloop-cli: driving coloop from the terminal

## Overview

`coloop` is one binary that is both the daemon and a thin CLI. With no
subcommand (or `serve`) it runs the scheduler + HTTP API + Web UI. Every other
subcommand is a small HTTP client that talks to an already-running daemon — so
the daemon must be up first (except `validate`, which is offline). Task edits
made through the CLI are persisted to `config.yaml` by the daemon and hot-reload
automatically, exactly like edits made in the Web UI.

## When to Use

**Use when**:
- Listing tasks and their last status / next run (`ls`), or inspecting one (`show`).
- Creating, editing, or deleting a task without opening the Web UI (`add` / `edit` / `rm`).
- Triggering a task on demand and capturing its run id (`run`).
- Reading or live-following a run's log (`logs [-f]`).
- Validating a config file offline before deploying it (`validate`).
- Asking the daemon to re-read its config (`reload`).
- Scripting any of the above (every command exits non-zero on failure).

**Don't use when**:
- You want to change `listen`, `shell`, `data_dir`, runners, or notifier channels
  — the CLI only covers **tasks**. Edit `config.yaml` (it hot-reloads) or use the Web UI.
- The daemon is not running — client commands will fail with "cannot reach daemon".
  Start it with `coloop serve` (or the installed launchd/systemd service).

## Prerequisites: a running daemon + addressing

All commands except `validate` need a reachable daemon. The client resolves the
base URL in this order:

1. `--addr <url>` flag (e.g. `--addr http://127.0.0.1:8765`)
2. `$COLOOP_ADDR` environment variable
3. the `listen:` field of the config (`--config PATH`, default `~/.coloop/config.yaml`)
4. built-in default `http://127.0.0.1:8765`

A bare `host:port` or `:port` is normalised to an `http://` loopback URL.

## Commands

```
coloop [--config PATH] <command> [args]

  serve                       run the daemon (scheduler + API + UI)
  ls                          list tasks: name, type, cron, last status, next run
  show <task>                 print a task's full definition and its recent runs
  add  --name N --type T …    create a task from flags (see "Task flags")
  edit <task> [flags]         change a task — only the flags you pass are applied
  rm   <task>                 delete a task (its run history on disk is kept)
  run  <task>                 trigger a task now, print the new run id
  logs [-f] <task> [id]       print a run's log (latest run if id omitted; -f follows live)
  validate [path]             load + validate a config file offline, print OK or the error
  reload                      ask the daemon to re-read its config from disk
```

Aliases: `show`=`get`, `add`=`create`, `edit`=`update`, `rm`=`delete`.

## Task flags (add / edit)

`add` and `edit` build a task from the same flags. `add` requires at least
`--name` and `--type`; the daemon fills defaults (e.g. `timeout` → `1h`,
`alert` → `on_failure`) and validates before persisting.

| flag | applies to | meaning |
|------|-----------|---------|
| `--name <s>` | both | task name; on `edit` this **renames** the task |
| `--type shell\|agent` | both | execution mode |
| `--cron "<expr>"` | both | 5-field cron; empty = manual only |
| `--working-dir <dir>` | both | cwd for the command (set this — cron's usual gotcha) |
| `--command <s>` | shell | command/script to run |
| `--runner <name>` | agent | runner to render (defined in config `runners:`) |
| `--prompt <s>` | agent | prompt text passed to the runner |
| `--cmd-tmpl <s>` | agent | inline command template (legacy; prefer `--runner`) |
| `--env KEY=VAL` | both | environment variable; **repeatable** |
| `--timeout <dur>` | both | e.g. `30s` / `10m` / `1h` |
| `--alert <policy>` | both | `on_failure` / `on_success` / `always` / `never` |
| `--notifiers a,b` | both | comma-separated notifier channel names (from config) |
| `--enabled` | both | set `--enabled=false` to keep the task but stop scheduling it |

`edit` semantics: it fetches the current task, overrides **only the flags you
actually passed**, then replaces the task. Everything you don't pass stays as-is.
`--env` on `edit` merges/overrides keys (it does not clear the existing map);
`--notifiers ""` clears the list.

## Examples

```sh
# Inspect
coloop ls
coloop show db-backup

# Create a scheduled shell task
coloop add --name db-backup --type shell \
             --cron "0 3 * * *" --working-dir ~/projects/api \
             --command ./scripts/backup.sh \
             --env ENV=prod --timeout 30m --alert on_failure

# Create an agent task using a named runner
coloop add --name triage --type agent --cron "0 */2 * * *" \
             --working-dir ~/projects/web --runner claude-code \
             --prompt "Inspect today's alert logs and summarize action items." \
             --alert always --notifiers team-feishu

# Patch one field (everything else untouched)
coloop edit db-backup --cron "0 4 * * *"
coloop edit db-backup --enabled=false        # pause scheduling
coloop edit triage --name triage-v2          # rename

# Trigger + watch
id=$(coloop run db-backup)
coloop logs -f db-backup "$id"               # follow live
coloop logs db-backup                         # latest run, static

# Delete (run history kept on disk)
coloop rm db-backup

# Offline config check / hot reload
coloop validate ~/.coloop/config.yaml
coloop reload

# Target a non-default daemon
coloop --addr http://127.0.0.1:9000 ls
COLOOP_ADDR=http://host:8765 coloop ls
```

## Common Rationalizations

| Rationalization | Reality |
|---|---|
| "I'll just edit `config.yaml` directly to add a task." | Fine, and it hot-reloads — but `add`/`edit` go through the daemon's validation and won't leave the file in a rejected state. For scripted/repeatable changes the CLI is safer. |
| "`edit` replaces the whole task, so I must repass every field." | No. `edit` patches only the flags you pass; it fetches the current task first and preserves the rest. |
| "The CLI can manage runners and notifier channels too." | It can't — CLI scope is **tasks** only. Runners/notifier channels are config-only (or Web UI). |
| "`run` waits for the task to finish." | `run` only triggers and prints the run id. Use `logs -f <task> <id>` to watch it. |
| "Client commands work without the daemon." | Only `validate` is offline. Everything else needs a running daemon and fails with "cannot reach daemon" otherwise. |
| "`rm` deletes the logs too." | `rm` removes the task definition only; run records under `runs/<task>/` stay on disk. |

## Red Flags

- Running `sudo coloop …` — the CLI never needs root; only the daemon's bind port matters.
- Prefixing client commands while no daemon is running, then blaming the command.
- Using `--cmd-tmpl` for new agent tasks instead of a named `--runner`.
- Passing a non-loopback `--addr` to a daemon whose API is unauthenticated.
- Editing `config.yaml` and a CLI `edit` of the same task concurrently (last writer wins).

## Verification

- [ ] `coloop ls` returns the task table (daemon reachable).
- [ ] `coloop add …` prints `created <name>` and the task appears in `ls`.
- [ ] `coloop edit <task> --cron …` prints `updated <name>` and `show` reflects only that change.
- [ ] `coloop rm <task>` prints `deleted <name>` and it disappears from `ls`.
- [ ] `coloop validate <path>` prints `OK` for a good config and a clear error otherwise.
