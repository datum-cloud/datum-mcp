<p align="center">
  <img width="60" src="assets/logo.png" alt="Datum logo">
</p>

<h1 align="center">Datum MCP Server</h1>

<p align="center">
  Empower agents to help you manage your network infrastructure.
</p>

---

Datum provides an open-source network cloud platform for building and operating network-sensitive apps across managed global infra and your own clouds.  
This repo ships the **Datum MCP server** — a small Go binary that exposes deterministic tools for CRDs and validation to Model Context Protocol (MCP) clients (e.g., Claude Desktop) and to simple CLI/HTTP tests.

- Website: https://www.datum.net

---

## What it does

The server discovers Datum CRD schemas (and optionally your control-plane OpenAPI) and exposes these tools:

- **`datum_list_crds`** – list `(apiVersion, kind)` pairs known to the control plane.
- **`datum_list_supported`** – list legal field paths (prefers `spec.*` when present).
- **`datum_prune_crd`** – strip unsupported fields (**422** if anything was removed).
- **`datum_validate_crd`** – local schema validation (parse + allow‑list check).
- **`datum_refresh_discovery`** – refresh the discovery cache.

> **Discovery behavior (concise):**  
> • Default is **GitHub only** (freshest published Datum CRDs).  
> • If you set `DATUM_OPENAPI_BASE` for a run, the server also ingests your control‑plane **OpenAPI** and adds any **new** `(apiVersion, kind)` pairs found there.  
> • When a pair exists in both sources, **GitHub wins** (its schema is authoritative).

---

## Requirements

- Go **1.20+** (`go version`)
- Git + internet access (for GitHub CRDs)
- Optional: `curl`, `jq` for quick tests
- Optional: Claude Desktop (for MCP usage)

---

## Install

Build from source:

```bash
git clone https://github.com/datum-cloud/datum-mcp.git
cd datum-mcp
go mod tidy
go build -o datum-mcp ./cmd/mcp

# (optional) put on PATH
sudo mv ./datum-mcp /usr/local/bin
```

---

## Configuration

Environment variables (all optional, recommended to set **per run**):

- `DATUM_OPENAPI_BASE` — if set, the server also fetches `<DATUM_OPENAPI_BASE>/openapi/v3` (JSON or YAML). Use inline on the command you run so it doesn’t persist.
- `DATUM_ACCESS_TOKEN` — bearer token to use when calling the control‑plane OpenAPI.
- `HTTPS_PROXY` / `HTTP_PROXY` — standard proxy settings if required by your network.

Logging: minimal logs go to **stderr** prefixed with `[datum-mcp]`.

---

## Quick start: manual CLI/HTTP smoke test

Run the server with an HTTP port for easy `curl` tests (**MCP still uses STDIO**):

```bash
datum-mcp --port 7777
# [datum-mcp] STDIO mode ready
# HTTP listening on 127.0.0.1:7777
```

In another terminal:

```bash
# 1) List CRDs discovered from GitHub
curl -s http://127.0.0.1:7777/datum/list_crds | jq

# 2) Pick one api/kind
API=$(curl -s http://127.0.0.1:7777/datum/list_crds | jq -r '.crds[0][0]')
KIND=$(curl -s http://127.0.0.1:7777/datum/list_crds | jq -r '.crds[0][1]')

# 3) List supported fields (safe quoting with jq -n)
curl -s -X POST http://127.0.0.1:7777/datum/list_supported \
  -H 'content-type: application/json' \
  -d "$(jq -n --arg api "$API" --arg kind "$KIND" '{apiVersion:$api, kind:$kind}')" | jq

# 4) Create a minimal YAML and validate it
cat > /tmp/datum.yaml <<YAML
apiVersion: ${API}
kind: ${KIND}
metadata:
  name: example
spec: {}
YAML

curl -s -X POST http://127.0.0.1:7777/datum/validate_crd \
  -H 'content-type: application/json' \
  -d "$(jq -n --rawfile y /tmp/datum.yaml '{yaml:$y}')" | jq

# 5) Exercise prune (returns 422 if anything is removed)
echo $'\noops: true' >> /tmp/datum.yaml
curl -s -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:7777/datum/prune_crd \
  -H 'content-type: application/json' \
  -d "$(jq -n --rawfile y /tmp/datum.yaml '{yaml:$y}')"
```

> The HTTP port is for manual testing only. **MCP clients (like Claude) use STDIO**, not HTTP.

---

## Use with Claude Desktop (MCP over STDIO)

Claude talks to MCP servers over STDIO — point it at the **binary**, not the port.

1. Open Claude config:
   - macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`  
   - Linux: `~/.config/Claude/claude_desktop_config.json`  
   - Windows: `%APPDATA%\\Claude\\claude_desktop_config.json`

2. Add an entry (use your absolute path):

```json
{
  "mcpServers": {
    "datum_mcp": {
      "command": "/absolute/path/to/datum-mcp",
      "args": [],
      "env": {
        "DATUM_OPENAPI_BASE": "",
        "DATUM_ACCESS_TOKEN": ""
      }
    }
  }
}
```

3. Restart Claude Desktop.

4. In a new chat, ask Claude to call tools:
   - `datum_list_crds`
   - `datum_list_supported` with an `apiVersion` and `kind`
   - `datum_validate_crd` with your YAML
   - `datum_prune_crd` with your YAML to remove unsupported fields

---

## (Optional) Integrate with `datumctl`

Expose the MCP server via `datumctl` in one of two simple ways:

**Shell alias (fastest):**
```bash
alias datumctl-mcp="datum-mcp"
```

**Subcommand:** have `datumctl` spawn the installed `datum-mcp` (from `PATH` or `DATUM_MCP_BIN`) and pass STDIO through, so users can run:
```bash
datumctl mcp
```

Since the server is pure Go, you can also embed it in‑process, but the standalone binary keeps upgrades and distribution trivial.

---

## Project layout

```
cmd/
  mcp/           # main.go (STDIO MCP; optional HTTP for testing)
internal/
  discovery/     # discovery.go: GitHub CRDs + optional /openapi/v3 ingestion
  mcp/           # service.go (tools), stdio/http layers
```

Build:

```bash
go build -o datum-mcp ./cmd/mcp
```

---

## Troubleshooting

- **Claude: “failed to start server”**  
  Check the absolute path in `claude_desktop_config.json` and executable bit:  
  `chmod +x /absolute/path/to/datum-mcp`.

- **`list_crds` is empty**  
  No internet or GitHub blocked. To include your CP OpenAPI for a single run:
  ```bash
  DATUM_OPENAPI_BASE="https://<cp>/api" DATUM_ACCESS_TOKEN="<token>" datum-mcp --port 7777
  ```
  Then simply run `datum-mcp` with no env to go back to GitHub‑only.

- **`prune_crd` returns HTTP 422**  
  That’s expected when unsupported fields were removed. Fix the YAML and retry.

- **Corporate proxy**  
  Set `HTTPS_PROXY` / `HTTP_PROXY` before launching `datum-mcp`.

- **Go toolchain mismatch**  
  If `go mod tidy` complains about version, set `go 1.20` in `go.mod` or upgrade Go.

---

## License

See <a href="./LICENSE">LICENSE</a>.
