# AGENTS.md

## Purpose

Keep `opengo` a small, readable reference client for direct ChatGPT subscription authentication and the Codex Responses backend.

## Before changing code

- Read `README.md` and trace the complete request path in `main.go`.
- Keep the authenticated chat proof of concept working independently of later experiments.
- Put distinct capabilities in separate commits so the minimal base remains easy to inspect.

## Implementation rules

- Prefer the Go standard library.
- Add a dependency only when implementing the equivalent correctly would require substantial custom code.
- Never hardcode private credentials or print tokens, authorization headers, or JWT contents.
- Keep credentials in the operating system's user configuration directory with mode `0600`.
- Keep the direct-backend implementation separate from Codex app-server integrations.
- Preserve bounded response-body reads and explicit error handling at network and file boundaries.
- Do not add abstractions for a single implementation.

## Validation

Run these checks before committing:

```sh
gofmt -w *.go
go test ./...
go vet ./...
build_dir="$(mktemp -d)"
go build -o "$build_dir/opengo" .
```

Never commit authentication files, logs, caches, or built binaries.
