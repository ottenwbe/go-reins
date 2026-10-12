# go-reins

A minimal AI agent harness written in Go. It is a learning project
for understanding how agent tooling is built: a small agent loop that
talks to local inference backends (Ollama, llama.cpp) through a
swappable interface, with tools the operator can configure and gate.

## Agent vs. harness

- **Agent** — the LLM in a loop that decides what to do next (reason,
  act, observe, repeat).
- **Harness** — the scaffolding around it: tools, approvals, backend
  plumbing, configuration.

This project implements both, with a strict seam between them.

## Quick start

```sh
make build          # go-reins binary with a fresh SBOM embedded
./go-reins run "figure out which system you run on"
./go-reins chat     # interactive REPL: transcript, approvals, ctrl+c quits
```

With Ollama running locally, no flags are needed: the default backend
is `ollama` and the default model is `gemma4`. Every mutating tool
call asks for confirmation before it runs.

## Commands

### `run` — one task, one answer

```sh
./go-reins run "explain the CAP theorem in 3 sentences"
./go-reins run --backend llamacpp --model qwen2.5 "hello"
```

In a terminal, `run` shows a bubbletea view: a spinner while the
model works, an approval dialog (`y`/`n`) on each mutating tool
call, and the answer as the final frame. Two flags report on the
completed task: `--turns` (how many round trips it took) and
`--history` (dump the full conversation with roles, to stderr).

Piped into something else, `run` switches to the scriptable path:
the answer on stdout, prompts and diagnostics on stderr, tool
approvals as a plain `y/N` prompt on stdin — or pre-scripted:

```sh
printf 'y\ny\n' | ./go-reins run --tools shell "check disk usage and OS"
./go-reins run --yes --turns --history "what OS is this?" < /dev/null
```

When stdin holds no answer (EOF: cron, CI, `< /dev/null`), the call
is denied and the prompt says so — unattended runs must opt in to
auto-approval with `--yes`; it is never the default.

### `chat` — an interactive session

```sh
./go-reins chat
```

A REPL with a scrolling transcript, a multi-line input area (enter
sends, ctrl+j inserts a newline), approval dialogs, and a status
line that shows which tool the agent is using while it works. The
conversation persists across prompts for the whole session; ctrl+c
quits. Requires a terminal.

Slash commands manage the live session and never reach the model:

| Command | Effect |
| --- | --- |
| `/models` | list the models the current backend can serve |
| `/model <name>` | switch the model used from the next prompt on |
| `/backend <ollama\|llamacpp>` | swap the backend mid-conversation |

The header line always shows the backend and model the next request
will use. A backend switch keeps the conversation history — messages
are backend-agnostic — and lists the new backend's models right
after, so the next `/model` is one paste away. The model is not
validated against that list, and a configured `--url` carries over
unchanged on a switch.

### `licenses` — what the binary carries

```sh
./go-reins licenses
```

Lists every module the binary links against with its SPDX license
identifier. The data comes from a CycloneDX SBOM generated at build
time and embedded via `go:embed`, so the command needs no Go
toolchain and no module cache on the machine it runs on. `make
build` regenerates the document; `make check` fails when the
committed one is stale.

> **Experimental.** The licenses command and the SBOM machinery are a
> learning ground for what Go and a CLI can do — `go:embed`, build
> info, the module cache, the CycloneDX schema. Do not rely on it
> for license compliance decisions.

### `info` — how this binary was built

```sh
./go-reins info
```

Reports the release version (stamped in at build time via
`-ldflags`, `git describe` by default or `make build VERSION=1.0.0`),
the git revision and build time the Go toolchain embeds, the Go
version, and the platform.

Shell completion for bash, zsh, fish, and powershell ships with the
standard cobra `completion` command.

## Configuration

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `--backend` | `GO_REINS_BACKEND` | `ollama` | `ollama` or `llamacpp` |
| `--model` | `GO_REINS_MODEL` | `gemma4` | model name, e.g. `gemma4` or `llama3.2` |
| `--url` | `GO_REINS_URL` | per-backend default | backend base URL (validated at startup) |
| `--yes` | `GO_REINS_YES` | `false` | auto-approve tool calls |
| `--tools` | `GO_REINS_TOOLS` | all tools | comma-separated tools to load, e.g. `read,shell`; an empty list loads none |
| `--tool-risk` | `GO_REINS_TOOL_RISK` | tool's own tier | per-tool risk override, e.g. `shell=read-only` (`read-only`, `mutating`) |
| `--log-level` | `GO_REINS_LOG_LEVEL` | `error` | log level: `debug`, `info`, `warn`, `error` (stderr) |
| `--max-turns` | `GO_REINS_MAX_TURNS` | `8` | maximum backend round trips per run before the agent gives up (at least 1) |
| `--history` | `GO_REINS_HISTORY` | `false` | print the conversation after the answer (`run` only) |
| `--turns` | `GO_REINS_TURNS` | `false` | print how many turns the run took (`run` only) |
| `--config` | — | `$HOME/.go-reins.yaml` | config file path |

Values resolve as CLI flags > environment (`GO_REINS_*`) > config
file (YAML) > defaults; a flag counts only when explicitly set, so a
file or env var can supply what the user left at its default. The
whole chain lives in `internal/config` — no viper — and the resolved
configuration is validated at load, so a bad value fails at startup
instead of mid-run.

Config file example (`~/.go-reins.yaml`):

```yaml
backend: ollama
model: llama3.2
tools: [read, shell]
tool-risk:
  shell: read-only   # sandboxed shell: no approval prompt
```

`tools` selects which tools the agent loads; `tool-risk` overrides
how the approval gate scores a tool — the operator, not the tool,
decides whether a call needs confirmation. A tool named twice, a
tool that does not exist, an override for a tool that is not
loaded: all fail at startup instead of silently changing behavior.

## Architecture

The harness is built around small seams: `agent` knows only the
`backend.Backend` interface and its message types — never which
adapter is behind it, never a concrete tool. `session` is the only
place that assembles concrete implementations, which keeps
everything swappable: backends (Ollama, llama.cpp), tools with
declared risk tiers, the approval gate, and the bubbletea views
plug together behind one interface each.

The full walkthrough — package layout, design decisions, component
seams, the agent loop, and the dependency diagram — lives in
[docs/architecture.md](docs/architecture.md).

## Requirements

- Go 1.27 or later
- A running [Ollama](https://ollama.com) server (default
  `http://localhost:11434`) or a llama.cpp server (`llama-server`,
  default `http://localhost:8080`)

## Development

```sh
make build          # build with a freshly generated SBOM embedded
make check          # gofmt gate + vet + tests + SBOM freshness (what CI runs)
make fmt            # rewrite sources in gofmt style
make test           # go test ./...
make licenses       # build, then list dependency licenses
```

`make build` also stamps a version into the binary (`go-reins info`
reports it): `git describe` output by default, or an explicit `make
build VERSION=1.0.0`.

Tests cover the agent loop (fake backend), both adapters
(`httptest` servers), the shared HTTP plumbing, config resolution
and validation, tool selection and risk overrides, both tools'
windowing/capping/boundary behavior, the TUI models, the SBOM
roundtrip, and build info extraction — so no running backend is
needed. CI ([`.github/workflows/ci.yml`](.github/workflows/ci.yml))
builds all packages and runs `make check` on every push and pull
request: the same gate locally and in CI.

## Roadmap

- [x] Tool calling: text protocol (`TOOLCALL`/`TOOLRESULT`), dispatch
      to `Tool.Execute`, observations appended to the history
- [x] Shell tool + human in the loop: approvals unless `--yes`
- [x] Bubbletea front end: run view, chat REPL, headless fallback
- [x] Hand-rolled config loader replacing viper, with load-time validation
- [x] `read` tool with risk-tiered approvals and configurable tool loading
- [x] Licenses command, embedded CycloneDX SBOM, build provenance (`info`)
- [x] `--max-turns` flag
- [ ] Streaming responses
- [ ] `search` tool (glob + grep, read-only) and `edit` tool with
      diff approval in the TUI
- [ ] More backends (OpenAI-compatible cloud APIs)
