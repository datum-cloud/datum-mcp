<p align="center">
 
  <img width="500" height="99" alt="Datum Logo" src="https://github.com/user-attachments/assets/c57d4f38-da4e-466b-9e77-dd862d72578d" />
  
  <h1 align="center">Datum MCP Server</h1>
  
  <p align="center">
    Empower agents to help you manage your network infrastructure
  </p>
</p>

# datum-mcp

An MCP server for Datum Cloud with OAuth 2.1 (PKCE) auth, macOS Keychain token storage, and tools for listing/operating on organizations, projects, domains, HTTP proxies, HTTP routes, gateways, traffic protection policies, DNS zones/records, and CRD schemas.

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

MCP client config in Claude desktop and Claude:

Mode: stdio
```json
{
  "datum-mcp": {
    "command": "datum-mcp",
    "args": []
  }
}
```

Cursor config in stdio mode (macOS/Linux):

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
The binary speaks MCP over stdio or streamable http. Register it (e.g., in Claude Desktop) as a command transport pointing to the built executable.

## Run modes
- Stdio (http coming soon):
```bash
datum-mcp
```

## Tools
All tools accept JSON inputs and return both structured content and a pretty-printed text block for UIs that show text only.

### Response and error format
- **Successful list responses** are returned as `{ "items": [...], "count": N, "continue": "..." }`, not a raw Kubernetes
  list. `continue` is `""` when there are no more pages.
- **Successful get/create/update responses** return the resource with internal-only Kubernetes metadata stripped
  (`managedFields`, `generation`, `creationTimestamp`, `selfLink`). `uid` and `resourceVersion` are preserved: `uid` is
  how an agent references the object elsewhere, and `resourceVersion` is the concurrency token to round-trip into a
  later update/delete (see below). `spec`, `status`, `name`, `namespace`, `labels`, and `annotations` are preserved in full.
- **Update** deep-merges `body.spec` into the existing spec field-by-field: a null field deletes it, nested objects
  merge recursively, and arrays/scalars replace wholesale. Fields omitted from `body.spec` are left untouched.
- **Errors** are returned as structured JSON, not a plain-text message: `{ "error": "...", "suggested_action": { "tool": "...", "action": "...", "args": {...} } }`.
  `suggested_action` is populated whenever there's a clear recovery step (e.g. no active project/organization set, or a
  resourceVersion conflict).

### List pagination and filtering
Every tool's `list` action accepts:
- `limit` — max items per page (default 100, clamped to 500; there's no "list everything" mode, follow `continue` instead).
- `continue` — the `continue` token from a previous `list` response, to fetch the next page.
- `labelSelector` — kubectl-style, e.g. `"team=edge,env!=prod"`.
- `fieldSelector` — kubectl-style, e.g. `"metadata.name=foo"`; only fields the resource registers as selectable are supported server-side.

### Dry-run and optimistic concurrency (CRD-backed resource tools, and `projects` create)
- `dryRun: true` on `create`/`update`/`delete` validates and runs admission server-side without persisting the change;
  the response is tagged `"dryRun": true`.
- `resourceVersion: "<value from a prior get>"` on `update`/`delete` enables optimistic concurrency: if the resource
  changed since that `resourceVersion` was read, the call fails with a conflict error (`suggested_action` points back
  at `get`) instead of silently overwriting the newer state. Omit it to keep the previous last-write-wins behavior.

- organizations
  - **Actions**: `list` | `get` | `set`
  - **Input**:
    - List memberships for current user: `{ "action": "list" }`
    - Get active organization: `{ "action": "get" }`
    - Set active organization (verifies membership): `{ "action": "set", "name": "<org-id>" }`
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

- httproutes
  - Same shape and behavior as `domains` (namespaced list/get/create/update; delete by name).
  - Targets Gateway API HTTPRoute resources.

- gateways
  - Same shape and behavior as `domains` (namespaced list/get/create/update; delete by name).
  - Targets Gateway API Gateway resources.

- trafficprotectionpolicies
  - Same shape and behavior as `domains` (namespaced list/get/create/update; delete by name).
  - Policies are intended to target either `Gateway` or `HTTPRoute` resources.
  - Group/kind: `networking.datumapis.com` / `TrafficProtectionPolicy`.

- dnszones
  - **Actions**: `list` | `get` | `create` | `update` | `delete`
  - **Input**:
    - List: `{ "action": "list", "project": "<optional>" }`
    - Get: `{ "action": "get", "id": "<name>", "project": "<optional>" }`
    - Create: `{ "action": "create", "body": { ... }, "project": "<optional>" }`
    - Update: `{ "action": "update", "id": "<name>", "body": { ... }, "project": "<optional>" }`
    - Delete: `{ "action": "delete", "id": "<name>", "project": "<optional>" }`
  - **Project resolution**: `project` input, else active project (from `projects set`).
  - **Namespace**: operates in namespace `default`.
  - **Group/kind**: `dns.networking.miloapis.com` / `DNSZone`.

- dnsrecordsets
  - **Actions**: `list` | `get` | `create` | `update` | `delete`
  - **Input**:
    - List: `{ "action": "list", "project": "<optional>" }`
    - Get: `{ "action": "get", "id": "<name>", "project": "<optional>" }`
    - Create: `{ "action": "create", "body": { ... }, "project": "<optional>" }`
    - Update: `{ "action": "update", "id": "<name>", "body": { ... }, "project": "<optional>" }`
    - Delete: `{ "action": "delete", "id": "<name>", "project": "<optional>" }`
  - **Project resolution**: `project` input, else active project (from `projects set`).
  - **Namespace**: operates in namespace `default`.
  - **Group/kind**: `dns.networking.miloapis.com` / `DNSRecordSet`.

- dnszoneclasses
  - **Actions**: `list` | `get`
  - **Input**:
    - List: `{ "action": "list", "project": "<optional>" }`
    - Get: `{ "action": "get", "id": "<name>", "project": "<optional>" }`
  - **Scope**: cluster-scoped (no namespace).
  - **Group/kind**: `dns.networking.miloapis.com` / `DNSZoneClass`.

- apis (CRDs list/describe via upstream OpenAPI/`kubectl explain` logic)
  - **Actions**: `list` | `get`
  - **Input**:
    - List groups/versions and resources: `{ "action": "list", "project": "<optional>" }`
    - Get a schema for a specific kind: `{ "action": "get", "group": "<group>", "version": "<version>", "kind": "<Kind>", "project": "<optional>", "detail": "<optional>" }`
  - **Behavior**:
    - `list` reads the project control-plane OpenAPI v3 index and returns groups, versions, and resources with `name`, `kind`, and `namespaced`.
    - `get` fetches the OpenAPI v3 document for the given group/version and returns the full upstream-rendered schema for the requested kind.
    - `detail: "structure"` returns a condensed shape (types/properties/required/items only) instead of the full schema, to save context. Default is the full schema.

- context
  - **Actions**: none — call with `{}`.
  - **Behavior**: a stateless discovery snapshot, not session memory. Returns `authenticated`, `active_organization`,
    `active_project`, the organizations/projects you can reach, and a `next_step` with the exact next tool call to make
    (`null` once an active project is set). Doesn't trigger the OAuth login flow itself — call any other tool to do that
    if `authenticated` is `false`. The organizations/projects shown are capped at 500 each; `organizations_truncated` /
    `projects_truncated` are `true` when there are more, and `next_step` says to use the paginated `organizations`/
    `projects` list action and follow `continue` instead. `organizations` action=`set` and `projects` action=`set`
    always verify membership against the complete set regardless of size, independent of this cap.

## Prompts
In addition to tools, the server exposes MCP prompts for common multi-step workflows. Clients that support `prompts/list`
and `prompts/get` can surface these directly:

- `deploy-http-proxy` (`backend_service`, `backend_port`, optional `path_prefix`) — expose a backend `NetworkService` via
  an `HTTPProxy`, which auto-provisions its own `Gateway` and `HTTPRoute`.
- `configure-dns` (`domain_name`) — create a managed `DNSZone`, which auto-provisions its default NS `DNSRecordSet`, plus
  how to add further records.
- `onboard-to-project` (no arguments) — loop on the `context` tool until an active organization and project are set.

## Recommended workflow
1. `context` → check what's already set up (or jump straight to step 5 if `next_step` is already `null`)
2. `organizations` → list orgs, then set active org
3. `projects` → list for an org, then set active project
4. `context` → confirm `next_step` is `null` (or just use the `onboard-to-project` prompt for steps 1-4)
5. Use `domains` / `httpproxies` / `httproutes` / `gateways` / `trafficprotectionpolicies` / `dnszones` / `dnsrecordsets` / `dnszoneclasses` for CRUD/list/get, or `apis` to inspect CRD schemas

