<p align="center">
  <img width="60" src="assets/logo.png" alt="Datum logo">
</p>

<h1 align="center">Datum MCP Server</h1>

<p align="center">
  Empower agents to help you manage your network infrastructure.
</p>

---

Datum provides an open-source network cloud platform for building and operating network-sensitive apps across managed global infra and your own clouds.  
This repo ships the <strong>Datum MCP server</strong> — a small Go binary that exposes deterministic tools for CRDs and validation to Model Context Protocol (MCP) clients (e.g., Claude Desktop) and to simple CLI/HTTP tests.

- Website: https://www.datum.net

---

## What it does

The server discovers Datum CRD schemas (and optionally your control-plane OpenAPI) and exposes these tools:

- <strong>`datum_list_crds`</strong> – list `(apiVersion, kind)` pairs known to the control plane.
- <strong>`datum_list_supported`</strong> – list legal field paths (prefers `spec.*` when present).
- <strong>`datum_prune_crd`</strong> – strip unsupported fields (**422** if anything was removed).
- <strong>`datum_validate_crd`</strong> – local schema validation (parse + allow-list check).
- <strong>`datum_refresh_discovery`</strong> – refresh the discovery cache.

> The <em>skeleton</em> tool exists for orchestrators but is intentionally <strong>hidden</strong> from `tools/list`.  
> You can still call it by name: <strong>`datum_skeleton_crd`</strong>.

---

## Requirements

- Go <strong>1.20+</strong> (`go version`)
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

Environment variables (all optional):

- `DATUM_OPENAPI_BASE` — if set, the server also fetches `<DATUM_OPENAPI_BASE>/openapi/v3` (JSON or YAML).
- `DATUM_ACCESS_TOKEN` — bearer token to use when calling the control-plane OpenAPI.
- `HTTPS_PROXY` / `HTTP_PROXY` — standard proxy settings if required by your network.

Logging: minimal logs go to <strong>stderr</strong> prefixed with `[datum-mcp]`.

---

## Quick start: manual CLI/HTTP smoke test

Run the server with an HTTP port for easy `curl` tests (<strong>MCP still uses STDIO</strong>):

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

# 3) List supported fields (prefers spec.*)
curl -s -X POST http://127.0.0.1:7777/datum/list_supported   -H 'content-type: application/json'   -d "{"apiVersion":"$API","kind":"$KIND"}" | jq

# 4) Get a minimal skeleton YAML (hidden tool, callable by name)
SKELETON=$(curl -s -X POST http://127.0.0.1:7777/datum/skeleton_crd   -H 'content-type: application/json'   -d "{"apiVersion":"$API","kind":"$KIND"}" | jq -r .yaml)
echo "$SKELETON" | tee /tmp/datum.yaml

# 5) Validate the YAML (local schema check)
curl -s -X POST http://127.0.0.1:7777/datum/validate_crd   -H 'content-type: application/json'   -d "{"yaml":$(jq -Rs . </tmp/datum.yaml)}" | jq

# 6) Exercise prune (returns 422 if anything is removed)
echo $'\noops: true' >> /tmp/datum.yaml
curl -s -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:7777/datum/prune_crd   -H 'content-type: application/json'   -d "{"yaml":$(jq -Rs . </tmp/datum.yaml)}"
```

> The HTTP port is for manual testing only. <strong>MCP clients (like Claude) use STDIO</strong>, not HTTP.

---

## Use with Claude Desktop (MCP over STDIO)

Claude talks to MCP servers over STDIO — point it at the <strong>binary</strong>, not the port.

1. Open Claude config:
   - macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`  
   - Linux: `~/.config/Claude/claude_desktop_config.json`  
   - Windows: `%APPDATA%\Claude\claude_desktop_config.json`

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
   - (Hidden but callable) `datum_skeleton_crd` with `apiVersion` + `kind`

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

Since the server is pure Go, you can also embed it in-process, but the standalone binary keeps upgrades and distribution trivial.

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

- <strong>Claude: “failed to start server”</strong>  
  Check the absolute path in `claude_desktop_config.json` and executable bit:  
  `chmod +x /absolute/path/to/datum-mcp`.

- <strong>`list_crds` is empty</strong>  
  No internet or GitHub blocked. Try your control-plane:
  ```bash
  export DATUM_OPENAPI_BASE="https://<cp>/api"
  export DATUM_ACCESS_TOKEN="<token>"
  ```
  Restart `datum-mcp`.

- <strong>`prune_crd` returns HTTP 422</strong>  
  That’s expected when unsupported fields were removed. Fix the YAML and retry.

- <strong>Corporate proxy</strong>  
  Set `HTTPS_PROXY` / `HTTP_PROXY` before launching `datum-mcp`.

- <strong>Go toolchain mismatch</strong>  
  If `go mod tidy` complains about version, set `go 1.20` in `go.mod` or upgrade Go.

---

## License

See [LICENSE](./LICENSE).
