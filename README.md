# opengo

`opengo` is a minimal Go CLI that signs in with a ChatGPT subscription and sends conversations directly to the Codex Responses backend. It does not run Codex app-server and does not require an OpenAI API key.

## Quick start

Requirements:

- Go 1.25 or newer
- macOS for automatic browser opening
- A ChatGPT account with Codex access
- **Enable device code authorization for Codex** turned on under ChatGPT **Settings → Security and login**. On a managed workspace, an admin must enable device code login in workspace permissions. See [OpenAI's authentication guide](https://learn.chatgpt.com/docs/auth#preferred-device-code-authentication-beta).

```sh
go install github.com/simonbalfe/opengo@latest
opengo auth
opengo
```

Choose another model with:

```sh
./opengo -model gpt-5.6-sol
```

Enter `/exit` to leave the chat.

## How it works

```text
opengo
  -> OpenAI device-code login
  -> local OAuth token storage
  -> ChatGPT Codex Responses backend
  -> streamed text in the terminal
```

### Authentication

1. `opengo auth` requests a device code from `auth.openai.com`.
2. It opens the verification page in the default macOS browser.
3. It polls until the user approves the code.
4. It exchanges the approval for access and refresh tokens.
5. It saves the tokens with file mode `0600` and refreshes them when needed.

On macOS, credentials are stored at:

```text
~/Library/Application Support/opengo/auth.json
```

The credential file is outside this repository and must never be committed.

### Conversation loop

Each prompt is appended to an in-memory conversation. `opengo` sends that history to the Codex Responses endpoint, reads the server-sent event stream, prints text deltas immediately, and adds the final answer to the next request's history.

The default model is `gpt-5.6-sol`.

## What this proves

- A small third-party client can authenticate through a ChatGPT subscription.
- The client can refresh its own OAuth tokens.
- The client can call the Codex backend without Codex app-server.
- Streaming multi-turn conversation works with only the Go standard library.

## Deliberate limits

This is a proof of concept, not a full coding agent. It currently has no tools, filesystem access, command execution, skills, approvals, sandbox, persisted conversations, or context compaction.

The direct ChatGPT Codex endpoint is not the public OpenAI API contract and may change. Codex app-server is the better integration boundary when a product needs the complete Codex agent runtime.

## Development

```sh
gofmt -w *.go
go test ./...
go vet ./...
build_dir="$(mktemp -d)"
go build -o "$build_dir/opengo" .
```
