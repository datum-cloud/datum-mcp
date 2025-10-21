<p align="center">
  <img width="60px" src="assets/logo.png">
  
  <h1 align="center">Datum MCP Server</h1>
  
  <p align="center">
    Empower agents to help you manage your network infrastructure
  </p>
</p>

# datum-mcp

A MCP server for Datum Cloud with OAuth 2.1 (PKCE) auth, macOS Keychain token storage, and tools for listing/operating on organizations, projects, domains, HTTP proxies, and CRD schemas.

## Installation

- Quick install (auto-detects your platform and installs to a user-writable PATH):
```bash
curl -fsSL https://github.com/datum-cloud/datum-mcp/releases/latest/download/install.sh | sh
```

- Manual download:
  - Download the appropriate binary from the [latest release](https://github.com/datum-cloud/datum-mcp/releases/latest)
    - macOS: `datum-mcp_darwin_arm64`, `datum-mcp_darwin_amd64`
    - Linux: `datum-mcp_linux_amd64`, `datum-mcp_linux_arm64`
    - Windows: `datum-mcp_windows_amd64.exe` (and optionally `windows_arm64`)
  - Rename to `datum-mcp` (or `datum-mcp.exe` on Windows) and place it somewhere on your PATH.

Generic MCP Config:

Add this to your MCP client config to run the server via stdio:

```json
{
  "datum-mcp": {
    "command": "datum-mcp",
    "args": []
  }
}
```

(Optionally) Add to Cursor (macOS/Linux):

[![Install MCP Server](https://cursor.com/deeplink/mcp-install-light.svg)](https://cursor.com/en-US/install-mcp?name=datum-mcp&config=eyJ0eXBlIjoic3RkaW8iLCJlbnYiOnt9LCJjb21tYW5kIjoiL3Vzci9sb2NhbC9iaW4vZGF0dW0tbWNwICJ9)


Windows:

On Windows, point your MCP config to the full path where you installed the binary:

```json
{
  "datum-mcp": {
    "command": "<path prefix here>/datum-mcp.exe",
    "args": []
  }
}
```

## Build

```bash
go build ./cmd/datum-mcp
```

## Auth flow
- On first use, the server opens a browser for OAuth (PKCE), then stores credentials (including refresh token) in the system keychain.
- Subsequent calls reuse/refresh the token from keychain automatically.
- We log to stderr; JSON-RPC uses stdout.

## Environment variables (Optional)
- `DATUM_AUTH_HOSTNAME` (default `auth.datum.net`)
- `DATUM_API_HOSTNAME` (derived from auth host if unset)
- `DATUM_CLIENT_ID` (inferred for *.datum.net and *.staging.env.datum.net)
- `DATUM_TOKEN` (override bearer token; skips login)
- `DATUM_VERBOSE` (`true` to print verbose auth logs)
- `DATUM_USER_ID` (override user subject; otherwise from stored credentials)
- `DATUM_ORG` (active organization for project listing)

## Register with your MCP client
The binary speaks MCP over stdio. Register it (e.g., in Claude Desktop) as a command transport pointing to the built executable.

## Run modes
- Stdio (default):
```bash
datum-mcp
```

- HTTP server:
```bash
datum-mcp --mode http --host localhost --port 9000
```

Flags:
- `--mode` one of `stdio` (default) or `http`
- `--host` interface to bind in HTTP mode (default `localhost`)
- `--port` port to bind in HTTP mode (default `9000`)

## Tools
All tools accept JSON inputs and return both structured content and a pretty-printed text block for UIs that show text only.

- organizationmemberships
  - **Actions**: `list` | `get` | `set`
  - **Input**:
    - List memberships for current user: `{ "action": "list" }`
    - Get active organization: `{ "action": "get" }`
    - Set active organization (verifies membership): `{ "action": "set", "body": { "name": "<org-id>" } }`
  - **User resolution**: `DATUM_USER_ID` env, else subject from stored credentials.

- users
  - **Actions**: `list`
  - **Input**:
    - List users (org memberships) under an organization: `{ "action": "list", "org": "<org-id>" }`
  - Lists org-scoped memberships in namespace `organization-<org>` using the org control-plane client.

- projects
  - **Actions**: `list` | `get` | `set` | `create`
  - **Input**:
    - List: `{ "action": "list", "org": "<org-id>" }` (or set `DATUM_ORG` and omit `org`)
    - Get active: `{ "action": "get" }`
    - Set active (verifies existence in org): `{ "action": "set", "body": { "name": "<project-id>" }, "org": "<optional>" }`
    - Create: `{ "action": "create", "org": "<org-id>", "body": { "metadata": { "name": "<project-id>" }, "spec": { ... } } }`
  - **Org resolution**: `org` input, else `DATUM_ORG` env, else stored active org.

- domains
  - **Actions**: `list` | `get` | `create` | `update` | `delete`
  - **Input**:
    - List: `{ "action": "list", "project": "<optional>" }`
    - Get: `{ "action": "get", "id": "<name>", "project": "<optional>" }`
    - Create: `{ "action": "create", "body": { ... }, "project": "<optional>" }`
    - Update: `{ "action": "update", "id": "<name>", "body": { ... }, "project": "<optional>" }`
    - Delete: `{ "action": "delete", "id": "<name>", "project": "<optional>" }`
  - **Project resolution**: `project` input, else active project (from `projects set`).
  - **Namespace**: list/get/create/update run in namespace `default`.

- httpproxies
  - Same shape and behavior as `domains` (namespaced list/get/create/update; delete by name).

- apis (CRDs list/describe via upstream OpenAPI/`kubectl explain` logic)
  - **Actions**: `list` | `get`
  - **Input**:
    - List groups/versions and resources: `{ "action": "list", "project": "<optional>" }`
    - Get a schema for a specific kind: `{ "action": "get", "group": "<group>", "version": "<version>", "kind": "<Kind>", "project": "<optional>" }`
  - **Behavior**:
    - `list` reads the project control-plane OpenAPI v3 index and returns groups, versions, and resources with `name`, `kind`, and `namespaced`.
    - `get` fetches the OpenAPI v3 document for the given group/version and returns the full upstream-rendered schema for the requested kind (no custom trimming).

## Recommended workflow
1. `organizations` → list orgs
2. `organizations` → set active org
3. `projects` → list for an org
4. `projects` → set active project
5. Use `domains` / `httpproxies` for CRUD, or `apis` to inspect CRD schemas

