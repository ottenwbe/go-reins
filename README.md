# go-reins

A minimal AI agent harness written in Go. It is a learning project for
understanding how agent tooling is built: a small agent loop that talks to
local inference backends (Ollama, llama.cpp) through a swappable interface.

## Agent vs. harness

- **Agent** — the LLM in a loop that decides what to do next (reason, act,
  observe, repeat).
- **Harness** — the scaffolding around it: tools, backend plumbing,
  configuration, and (later) permissions and context management.

This project implements both, with a strict seam between them.

## Architecture

```
go-reins/
├── main.go                        # entry point, just calls cmd.Execute()
├── cmd/
│   ├── root.go                    # cobra root + viper (config file, env, flags)
│   └── ask.go                     # `go-reins ask "prompt"` + backend factory
└── internal/
    ├── backend/
    │   ├── backend.go             # the Backend interface — the swappable seam
    │   ├── ollama/ollama.go       # adapter: native /api/chat
    │   └── llamacpp/llamacpp.go   # adapter: OpenAI-style /v1/chat/completions
    └── agent/
        └── agent.go               # the agent loop + Tool interface
```

Key design decisions:

- **`internal/backend.Backend`** is the only contract the rest of the code
  relies on. Ollama and llama.cpp are two adapters over one three-method
  interface (`Name`, `Chat`). Adding a cloud API later means writing one new
  package — nothing else changes.
- **`internal/agent`** runs a reason → act → observe loop with a
  configurable turn cap (`WithMaxTurns`). Tool calling uses a text
  protocol owned by the agent: registered tools are announced in the
  system prompt, the model requests one with a `TOOLCALL <name>
  <json>` line, and the observation returns as a `TOOLRESULT` user
  message. Tool errors and unknown tools become observations, so the
  model can recover instead of the run failing.
- **Configuration** follows viper's precedence chain: CLI flags >
  environment (`GO_REINS_*`) > config file > defaults.

## Requirements

- Go 1.27 or later
- A running [Ollama](https://ollama.com) server (default
  `http://localhost:11434`) or a llama.cpp server (`llama-server`, default
  `http://localhost:8080`)

## Build

```sh
go build -o go-reins .
```

## Usage

Send a single prompt through the agent:

```sh
./go-reins ask --model llama3.2 "explain the CAP theorem in 3 sentences"
```

Use the llama.cpp backend:

```sh
./go-reins ask --backend llamacpp --model qwen2.5 "hello"
```

### Flags and configuration

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `--backend` | `GO_REINS_BACKEND` | `ollama` | `ollama` or `llamacpp` |
| `--model` | `GO_REINS_MODEL` | — | model name, e.g. `llama3.2` |
| `--url` | `GO_REINS_URL` | per-backend default | backend base URL |
| `--config` | — | `$HOME/.go-reins.yaml` | config file path |

Config file example (`~/.go-reins.yaml`):

```yaml
backend: ollama
model: llama3.2
url: http://localhost:11434
```

## Tests

```sh
go test ./...
```

Tests cover the agent loop (against a fake backend) and both adapters
(against `httptest` mock servers), so no running backend is needed.

## Roadmap

- [x] Tool calling: text protocol (`TOOLCALL`/`TOOLRESULT`), dispatch
      to `Tool.Execute`, observations appended to the history
- [ ] Wire a first real tool into `go-reins ask` (e.g. current time,
      shell command) and a `--max-turns` flag
- [ ] Streaming responses
- [ ] Interactive REPL mode
- [ ] More backends (OpenAI-compatible cloud APIs)
