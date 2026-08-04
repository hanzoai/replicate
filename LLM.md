# hanzoai/replicate — agent guide

Disaster recovery for SQLite. A background process watches the WAL, turns changes
into immutable LTX files, and replicates them to cloud storage. Built on
`modernc.org/sqlite` — pure Go, no CGO.

## Critical rules

- **Lock page at 1GB**: SQLite reserves the page at 0x40000000. Always skip it. See
  [docs/SQLITE_INTERNALS.md](docs/SQLITE_INTERNALS.md)
- **LTX files are immutable**: never modify one after creation. See
  [docs/LTX_FORMAT.md](docs/LTX_FORMAT.md)
- **Single replica per database**: each DB replicates to exactly one destination
- **`replicate ltx`**, not `replicate wal` (deprecated). `-level 0`–`9` or
  `-level all` inspects specific compaction levels — `cmd/replicate/ltx.go`
- **`replicate reset`** clears corrupted local LTX state for a database —
  `cmd/replicate/reset.go`. The `auto-recover` replica option does it automatically
  on LTX errors, and is off by default (`replica.go`)
- **Retention is ON by default** (`Store.RetentionEnabled`). Turn it off only where
  cloud lifecycle policies already handle cleanup — `store.go`
- **The IPC socket is OFF by default**. `socket.enabled: true` in config —
  `server.go`
- **`$PID` expands in config**, alongside the usual `$ENV_VAR` —
  `cmd/replicate/main.go`
- **Return errors, never log and continue.** This is the single most common thing
  review sends back, and in a disaster recovery tool a swallowed error is a backup
  that silently is not one. The one exception is DEBUG logging for best-effort work
  where failure cannot affect correctness and a valid fallback exists — reading the
  SHM `mxFrame` optimisation hint, for instance. The decision framework is in
  [docs/PATTERNS.md](docs/PATTERNS.md#error-handling)

## Layer boundaries

| Layer | File | Responsibility |
|-------|------|----------------|
| DB | `db.go` | Database state, restoration, WAL monitoring, library API (`SyncStatus`, `SyncAndWait`, `EnsureExists`) |
| Replica | `replica.go` | Replication mechanics only |
| Storage | `**/replica_client.go` | Backend implementations (incl. `ReplicaClientV3` for v0.3.x restore) |
| IPC | `server.go` | Unix socket control API (register/unregister, /txid, pprof) |
| Leasing | `leaser.go`, `s3/leaser.go` | Distributed lease acquisition via conditional writes |

Database state logic belongs in the DB layer, never in Replica.

## Build

```bash
go build -o bin/replicate ./cmd/replicate
go test -race -v ./...
pre-commit run --all-files
```

Race detector always: the whole program is a WAL reader racing a writer.

## Documentation

| Document | When to read |
|----------|--------------|
| [docs/PATTERNS.md](docs/PATTERNS.md) | Writing code here — patterns and anti-patterns |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Component detail |
| [docs/SQLITE_INTERNALS.md](docs/SQLITE_INTERNALS.md) | WAL format, lock page |
| [docs/LTX_FORMAT.md](docs/LTX_FORMAT.md) | The replication format |
| [docs/TESTING_GUIDE.md](docs/TESTING_GUIDE.md) | Test strategies |
| [docs/REPLICA_CLIENT_GUIDE.md](docs/REPLICA_CLIENT_GUIDE.md) | Adding a storage backend |
| [docs/PROVIDER_COMPATIBILITY.md](docs/PROVIDER_COMPATIBILITY.md) | Provider-specific S3/cloud config |
| [CONTRIBUTING.md](CONTRIBUTING.md) | What is accepted — bug fixes welcome, features need discussion |
| [AI_PR_GUIDE.md](AI_PR_GUIDE.md) | The evidence a change is expected to show |

## Agents and commands in this repo

`.claude/agents/` — `sqlite-expert` (WAL and page management),
`replica-client-developer` (storage backends), `ltx-compaction-specialist`,
`test-engineer`, `performance-optimizer`.

`.claude/commands/` — `/analyze-ltx`, `/debug-ipc`, `/debug-wal`,
`/test-compaction`, `/trace-replication`, `/validate-replica`,
`/add-storage-backend`, `/fix-common-issues`, `/run-comprehensive-tests`.

## Before submitting

- Follow [docs/PATTERNS.md](docs/PATTERNS.md)
- `go test -race`
- `pre-commit run --all-files`
- Page iteration: test against a database over 1GB, or the lock page is untested
- Show the investigation, not just the fix ([AI_PR_GUIDE.md](AI_PR_GUIDE.md))

`LLM.md` is the canonical guide; `CLAUDE.md` and `AGENTS.md` are symlinks to it.
