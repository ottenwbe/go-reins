# AGENTS.md

Working conventions for anyone (human or AI) changing this repository.

## Workflow

- Never commit feature work directly on `main`. Create a dedicated
  branch per feature first: `feature/<topic>` (e.g. `feature/logging`).
- Open a pull request against `main`; CI must be green before merging.
- Keep commits focused; write commit messages that explain the what
  and the why.
- Remote is `git@github.com:ottenwbe/go-reins.git` (SSH), branch `main`.

## Commands

- Build: `go build -o go-reins .`
- Vet: `go vet ./...`
- Test: `go test ./...` (no live backend needed; tests use fakes and
  `httptest` servers)
- `make` wraps all of these: `make build`, `make test`, `make vet`,
  `make fmt`, `make check` (gofmt check + vet + test + SBOM freshness),
  `make licenses`, and `make sbom` (regenerate the embedded CycloneDX
  SBOM — run it whenever go.mod changes)

## Code conventions

- Standard Go formatting and idioms; comments on exported symbols.
- New capabilities go behind an interface in `internal/` when more than
  one implementation or caller is expected (see `backend.Backend`,
  `agent.Tool`, `agent.Approver`).
- CLI-facing behavior is configured via cobra flags bound to viper
  (`GO_REINS_*` env prefix); user-facing output goes to stdout,
  diagnostics (logs, prompts, history dumps) to stderr.
