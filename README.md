# flomo-cli

An offline-first CLI for capturing, syncing, and searching [flomo](https://flomoapp.com/) memos. It mirrors memos into SQLite and exposes stable, paginated JSON/JSONL output for coding agents and scripts.

## Install and update

Installation and updates use the same command (Go 1.23 or newer):

```sh
go install github.com/songquanpeng/flomo-cli/cmd/flomo@latest
```

The executable is installed into `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset.
Ensure that directory is included in your shell's `PATH`; with the default Go layout:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Run the command again whenever you want to update. `flomo update` prints this exact command
from the CLI, and `make install` / `make update` are convenience aliases for it.

Then start using it:

```sh
flomo login --account you@example.com
flomo sync
flomo search "人工智能 写作" --json
flomo add "新的想法" --tag 灵感
flomo edit <slug> --add-tag 已整理 --remove-tag 收件箱
flomo attachments list <slug> --json
```

Run `flomo --help`, `flomo <command> --help`, or `flomo agent-guide`; the CLI contains its complete operating guidance. Paginated text output prints the exact next command, while JSON includes a machine-readable `next.command`. Credentials are stored with mode `0600`, passwords are never saved, and `FLOMO_TOKEN` can provide an ephemeral token without writing it to disk.

`flomo sync` also downloads missing image and recording attachments under the private state directory and stores their absolute paths and SHA-256 values with the SQLite metadata. `flomo attachments list --json` exposes those paths and recording transcripts to agents; `--skip-attachments` disables downloading for a particular sync.

The web endpoints used by flomo's own client are not a documented public API and may change. Their protocol is isolated in `internal/flomo` so the rest of the CLI remains stable if the transport needs updating.

## Architecture

- `cmd/flomo` is the executable entry point used by `go install`; `internal/cli` owns command parsing and presentation.
- `internal/domain` is the stable model owned by this project.
- `internal/service` depends on a `MemoGateway` port, never the concrete Web API client, and owns attachment caching.
- `internal/flomo` is the volatile adapter: signing, versioned endpoints, private wire DTOs, browser request profile, pacing, compression, and DTO-to-domain mapping live only here.
- `internal/storage` owns migrations, SQLite persistence, tag relations, pagination, and FTS5 trigram search.

Adapter contract tests pin the currently observed v1 login/sync and v2 write shapes. A future flomo protocol change should therefore be contained to `internal/flomo` and detected without destabilizing CLI JSON or the local schema.

The browser profile is centralized in `internal/flomo/web_profile.go`. It mirrors the current Chromium CORS headers used by flomo Web. Consecutive v1 memo-sync calls follow flomo Web's 500ms interval, and transient nginx-generated 403 responses are retried with bounded backoff only for idempotent GET requests. Writes are never retried automatically.

## Releases

Every pushed Git tag runs tests and produces GitHub Release archives for Linux, macOS, and Windows on both `amd64` and `arm64`. The release also includes `checksums.txt`. Normal pushes and pull requests run tests, the race detector, vet, and a build through GitHub Actions.
