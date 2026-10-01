# whatsapp-mcp

WhatsApp MCP server that connects Claude to a personal WhatsApp account. Two-component architecture: a Go bridge talking to WhatsApp's multidevice API with SQLite history, and a Python MCP server exposing the data as Claude tools.

This is the `weirdapps/whatsapp-mcp` fork of `lharries/whatsapp-mcp`, ahead of upstream. Fork additions on top of the upstream code: `whatsmeow` API compatibility fix, the port to the `mcp` v2 SDK (`FastMCP` to `MCPServer`), a pytest suite, `start-bridge.sh` launcher, GitHub Actions CI, Dependabot (github-actions + gomod + uv), SonarCloud config, Ruff config, and `SECURITY.md`.

## Tech Stack

**Go bridge** (`whatsapp-bridge/`)
- Go 1.25.0 (pinned in `go.mod`); CGO required (for `go-sqlite3`)
- `go.mau.fi/whatsmeow` for the WhatsApp multidevice API
- `github.com/mattn/go-sqlite3` (CGO-dependent SQLite driver)
- `github.com/mdp/qrterminal` for the pairing QR code
- `google.golang.org/protobuf`
- Exposes REST API on `http://localhost:8080/api` (send + download endpoints)

**Python MCP server** (`whatsapp-mcp-server/`)
- `pyproject.toml` requires `python>=3.11`; `.python-version` pins 3.11; CI runs on 3.12
- Managed with `uv` (`uv.lock` committed)
- Dependencies: `mcp[cli]>=2.0.0` (v2 SDK; the high-level server class is `MCPServer`, imported from `mcp.server`), `httpx>=0.28.1`, `requests>=2.34.2`, plus `sqlite3` from stdlib
- Dev group (`[dependency-groups] dev`): `pytest`, `pytest-asyncio`
- Optional: `ffmpeg` for Opus/OGG audio conversion (voice notes)

## Build

```bash
# Go bridge (CGO required; works on macOS arm64 out of the box)
cd whatsapp-bridge && go build -o whatsapp-bridge .

# Python MCP server
cd whatsapp-mcp-server && uv sync

# Python MCP server, with test deps
cd whatsapp-mcp-server && uv sync --group dev
```

## Run

```bash
# Option 1: convenience script (execs the pre-built binary in whatsapp-bridge/)
./start-bridge.sh

# Option 2: from source
# Terminal 1: Go bridge
cd whatsapp-bridge && go run main.go

# Terminal 2: MCP server (or launched automatically by an MCP client over stdio)
cd whatsapp-mcp-server && uv run main.py
```

First run: QR code displayed in terminal; scan with WhatsApp mobile to pair. Session persists indefinitely (WhatsApp multidevice linked-device protocol). No periodic re-auth needed; re-pair only if the device is manually unlinked from WhatsApp settings on the phone, or the phone stays offline for around 30+ days.

## Tests

```bash
cd whatsapp-mcp-server && uv run pytest -v   # 12 tests
cd whatsapp-bridge && go test ./...          # API guard, name cleaning, send allowlist, token
```

The Python suite lives in `whatsapp-mcp-server/tests/test_mcp_server.py` and targets the MCP surface: the server object is a v2 `MCPServer`, all 12 tools register, input schemas are still derived from type hints (optionals, defaults, required lists), a `call_tool` round trip works against an in-memory `Client`, `audio.py` rejects a missing input file, each read tool returns output that validates against its declared schema (the dataclass-serialisation regressions below), and bridge calls carry the API token. It needs no running bridge and no `messages.db`: the data layer is patched with `unittest.mock.patch.object(main, "whatsapp_*")`. The Go tests (`whatsapp-bridge/security_test.go`) cover `requireLocalClient`, `safeMediaName`, `resolveSendPath` and token creation.

## Code Organization

```
whatsapp-mcp/
├── start-bridge.sh              # Convenience launcher for Go bridge binary
├── whatsapp-bridge/
│   ├── main.go                  # All Go logic: whatsmeow client, SQLite schema, REST API (~1350 lines)
│   ├── go.mod / go.sum
│   └── store/                   # Runtime SQLite databases (*.db gitignored)
│       ├── messages.db          # Message + chat history (Python reads this directly)
│       └── whatsapp.db          # whatsmeow session/device credentials
└── whatsapp-mcp-server/
    ├── main.py                  # 12 MCP tool definitions (@mcp.tool() on an MCPServer)
    ├── whatsapp.py              # Business logic: SQLite queries + REST calls to bridge
    ├── audio.py                 # ffmpeg wrapper: converts audio to Opus OGG for voice messages
    ├── tests/
    │   └── test_mcp_server.py   # MCP surface tests (registration, schemas, call_tool)
    ├── pyproject.toml / uv.lock / .python-version
    └── .venv/                   # Created by uv sync (uv writes its own .gitignore inside)
```

MCP tools registered in `whatsapp-mcp-server/main.py` (12 total): `search_contacts`, `list_messages`, `list_chats`, `get_chat`, `get_direct_chat_by_contact`, `get_contact_chats`, `get_last_interaction`, `get_message_context`, `send_message`, `send_file`, `send_audio_message`, `download_media`.

## Key Conventions

- Python reads `messages.db` directly via SQLite for all read tools. The Go REST API (`localhost:8080/api`) is used only for writes (send, download).
- **Serialise at the tool boundary.** `whatsapp.py` returns dataclasses (`Chat`, `Contact`, `Message`, `MessageContext`); `main.py` must convert them with `dataclasses.asdict` before returning, because the MCP SDK validates every tool result against the output schema derived from that tool's return annotation. Handing a dataclass back where the annotation says `dict` fails with `Input should be a valid dictionary` and the tool returns an error to the model. Whenever you add a tool or change a return type, keep `main.py`'s annotation, `whatsapp.py`'s actual return, and the conversion in agreement. Note `whatsapp.list_messages` returns a preformatted transcript `str`, not a list.
- `MESSAGES_DB_PATH` in `whatsapp.py` is hardcoded as a relative path from `whatsapp-mcp-server/` to `../whatsapp-bridge/store/messages.db`; both components must live in the same working copy on the same host.
- Go bridge must be running before the Python MCP server handles any tool call.
- CGO is required for the Go bridge. Windows needs `CGO_ENABLED=1` + a C compiler (MSYS2 `ucrt64`).
- No `.env`; configuration is hardcoded (port 8080, DB path). One optional variable, `WHATSAPP_SEND_ALLOWED_DIRS`, replaces the folders `/api/send` may attach from.
- Every `/api/` call needs the header `X-Bridge-Token` with the contents of `whatsapp-bridge/store/api_token`; the bridge also refuses requests with an `Origin` header, a non-loopback `Host`, or a non-JSON body.
- Audio voice messages require `.ogg` Opus format; `audio.py` auto-converts via ffmpeg. Without ffmpeg, raw file send works but the message won't appear as a voice note in WhatsApp.

## Reset

To force re-authentication: stop the bridge, delete `whatsapp-bridge/store/whatsapp.db` and `whatsapp-bridge/store/messages.db`, then restart the bridge (QR code will appear again).

## CI

CI lives at `.github/workflows/ci.yml` (runs on push and PR to `main`):
- `go-build` job: `actions/setup-go@v7` with `go-version-file: whatsapp-bridge/go.mod`, then `go build -v .`, `go vet ./...` and `go test ./...` in `whatsapp-bridge/`.
- `python-lint` job: `actions/setup-python@v7` pinned to `3.12`, installs `ruff`, then `ruff check .` and `ruff format --check .` in `whatsapp-mcp-server/`. Ruff config (rules, line length, target `py311`) is in `whatsapp-mcp-server/pyproject.toml`.
- `python-test` job: `astral-sh/setup-uv@v9.0.0` (exact tag: setup-uv stopped publishing moving major tags after `v7`, so `@v9` fails to resolve) with caching, then `uv sync --locked --group dev` and `uv run pytest -v` in `whatsapp-mcp-server/`. `--locked` fails the build if `uv.lock` drifts from `pyproject.toml`. The Python version comes from `.python-version` (3.11), so tests run on the declared minimum rather than the 3.12 the lint job uses.

Dependabot (`.github/dependabot.yml`) tracks three ecosystems weekly: `github-actions`, `gomod` (bridge), and `uv` (server). The `uv` ecosystem is used instead of `pip` so `pyproject.toml` and `uv.lock` stay in sync. SonarCloud is configured via `sonar-project.properties` (organization `weirdapps`, project key `weirdapps_whatsapp-mcp`, Python 3.11).

## Fork hygiene

Origin `weirdapps/whatsapp-mcp`, upstream `lharries/whatsapp-mcp`. Never run `gh issue comment` from this clone: `gh` defaults to upstream, and comments should stay on repos owned by `weirdapps`.
