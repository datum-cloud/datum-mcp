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
- `DATUM_MCP_DISABLE_TOOLSETS` (comma-separated, e.g. `billing,iam`; see "Toolsets" below)

### Running fully non-interactively (CI, an agent-hosted deployment, etc.)
Set **both** `DATUM_TOKEN` (a valid bearer token, obtained however you obtain one outside this process) and
`DATUM_API_HOSTNAME` (e.g. `api.datum.net`). Both are required together: `DATUM_TOKEN` alone used to silently fall
through to requiring a prior interactive login just to learn the API hostname, even though the token itself was never
going to come from that login. With both set, no keychain access and no browser is ever needed. `DATUM_USER_ID` is
also required for the `organizations` tool's `list`/`set` actions (which need a user ID to query memberships), since
there's no stored-credentials subject to fall back on.

## Register with your MCP client
The binary speaks MCP over stdio or streamable http. Register it (e.g., in Claude Desktop) as a command transport pointing to the built executable.

## Run modes
- **Stdio** (default, and what every mainstream MCP client - Claude Desktop, Claude Code, Cursor - expects): the client
  spawns `datum-mcp` itself as a subprocess and talks JSON-RPC over its stdin/stdout. This is the `command`-style config
  shown under Installation above.
```bash
datum-mcp
```
- **HTTP** (for a server you run yourself as a persistent process, e.g. on a shared host): `datum-mcp` listens and
  serves MCP over streamable HTTP; your client connects to it **by URL**, the same way a browser connects to a web
  server - the client does not spawn this command. Do not combine `--mode http` with a client config shape that spawns
  a command (an HTTP-type client config normally takes a `url`, not a `command`); that mismatch is what issue #19 on
  this repo turned out to be. Exits gracefully on SIGINT/SIGTERM.
```bash
datum-mcp --mode http --host localhost --port 9000
# then point your client's MCP config at http://localhost:9000 (however that
# client's config format expresses "connect to this URL", not "run this command")
```
  This transport has no authentication or Origin check of its own - every tool call runs with whatever Datum Cloud
  credentials this process has, so anyone who can reach `host:port` can act as you against Datum Cloud. `--host`
  refuses to bind anywhere but a loopback address (`localhost`/`127.0.0.1`/`::1`) unless you also pass
  `--allow-non-loopback`; only do that if you have your own access control in front of it (a reverse proxy, a
  container network boundary, etc.).

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

- resource (generic escape hatch)
  - **Actions**: `list` | `get` | `create` | `update` | `delete`, same shape and semantics as every other resource tool —
    except `iam.miloapis.com`, `billing.miloapis.com`, and `services.miloapis.com`, which are `list`/`get` only here,
    same as their dedicated tools, regardless of toolset configuration: this generic tool is never a wider door into
    those groups than the dedicated tool is.
  - **Input**: adds `group` (required, e.g. `"compute.datumapis.com"`), `kind` (required, e.g. `"Workload"`), and
    `namespace` (required if the kind is namespaced — check with `apis`) to the usual `project`/`id`/`body`/pagination/
    `dryRun`/`resourceVersion` fields.
  - **Behavior**: for any resource kind that doesn't have a dedicated tool. Only `*.datumapis.com`/`*.miloapis.com`
    groups and the Gateway API groups are reachable here; everything else the control plane also exposes (core
    Kubernetes machinery, RBAC, admission webhooks, etc.) is refused. Use `apis` (`action: "list"`) first to find a
    kind's group and whether it's namespaced. Can itself be disabled via `DATUM_MCP_DISABLE_TOOLSETS=resource`.

### IPAM (toolset `ipam`)
Mirrors `datumctl ipam`'s own class/pool/claim/allocation grouping. All four share the usual CRUD tool shape
(`list`/`get`/`create`/`update`/`delete`, `project`/`id`/`body`/pagination/`dryRun`/`resourceVersion`), restricted per
kind below.

- `ipclasses` — **Actions**: `list` | `get`. Cluster-scoped, operator-authored. The kinds of address space a claim can
  name; check a class's pools (in its status) before claiming from it.
- `ippools` — **Actions**: full. Cluster-scoped. Root pools declare a CIDR; child pools carve a sub-prefix from a
  parent. `delete` releases a pool.
- `ipclaims` — **Actions**: full. Namespaced (`default`). A claim names a class and a scope — never a pool, CIDR, or
  location; the server resolves those and reports them in `status.poolRef`/`status.allocatedCIDR`. `delete` releases
  the claim.
- `ipallocations` — **Actions**: `list` | `get` | `delete`. Namespaced (`default`). Created by the system when a claim
  is satisfied, never directly. `delete` releases a held allocation back to its pool — e.g. one left behind by a claim
  released under reclaim policy Retain.

### Compute (toolset `compute`)
- `workloads` — **Actions**: full. Namespaced (`default`). The spec is deeply nested (placements, template, runtime,
  sandbox, containers) — inspect it first with `apis` (`group: "compute.datumapis.com"`, `version: "v1alpha"`,
  `kind: "Workload"`, `detail: "structure"`) rather than guessing the shape.
- `instances` — **Actions**: `list` | `get`. Namespaced (`default`). Read-only: instances come from a Workload's
  rollout, not direct creation. To change them, create/update/delete the owning workload instead.

### Galactic VPC (toolset `vpc`)
- `networks`, `subnets`, `connectors` — **Actions**: full. Namespaced (`default`). The long tail of VPC resources
  (subnet claims, connector advertisements/classes, network policies/interfaces/contexts/bindings, etc.) doesn't have
  a dedicated tool — reach them via the generic `resource` tool.

### Activity (toolset `activity`)
- `activity` — **Actions**: `create` (primary) | `get` | `list` | `delete`. Cluster-scoped. Query audit logs, Kubernetes
  events, or the combined human-readable activity feed — `create` submits a query and the results come back in the
  same response's `status.results`; nothing is persisted the way `domains`/`dnszones`/etc. are.
  - **Input**: adds `queryType` (required: `audit` | `events` | `feed`) to the usual fields; the query parameters go in
    `body.spec`, which differs per `queryType`:
    - `audit` (`AuditLogQuery`): `startTime`\*, `endTime`\* (relative like `"now-7d"` or RFC3339), `filter` (CEL),
      `limit`, `continue`.
    - `events` (`EventQuery`, up to 60 days vs. the native 24h Events list): `startTime`\*, `endTime`\*, `namespace`,
      `fieldSelector` (standard Kubernetes field-selector syntax, e.g. `"type=Warning"`), `limit`, `continue`.
    - `feed` (`ActivityQuery`; also covers `datumctl activity history` — add a `spec.resource.*` filter to scope to one
      resource): `startTime`\*, `endTime`\*, `filter` (CEL; fields: `spec.changeSource`,
      `spec.actor.name`/`type`/`uid`, `spec.resource.apiGroup`/`kind`/`name`/`namespace`/`uid`, `spec.summary`,
      `spec.origin.type`), `search`, `limit`, `continue`.
  - Example: `{"queryType": "audit", "action": "create", "body": {"metadata": {"name": "recent-deletions"}, "spec": {"startTime": "now-7d", "endTime": "now", "filter": "verb == 'delete'", "limit": 100}}}`

### Search (toolset `search`)
- `search` — **Actions**: `create` (primary) | `get` | `list` | `update` | `delete`. Cluster-scoped. Same
  create-a-query-get-synchronous-results pattern as `activity`. `create` body: `spec.query` (required),
  `spec.limit`, `spec.targetResources` (optional `[{group,kind,version}]` to scope to specific kinds) — results come
  back in `status.results`.

### IAM (toolset `iam`) — read-only
Write access (granting roles/permissions) is deferred pending a safety design; these cover the most common need —
auditing who has access, not managing it.
- `roles` — **Actions**: `list` | `get`. Namespaced (`default`). Datum IAM roles (`iam.miloapis.com`), not plain
  Kubernetes RBAC Roles — the control plane exposes both under the same Kind name.
- `policybindings` — **Actions**: `list` | `get`. Namespaced (`default`). Binds a Role to subjects
  (User/Group/ServiceAccount) over a `resourceSelector`.

### Services (toolset `services`)
- `services` — **Actions**: `list` | `get`. Cluster-scoped. The platform's service catalog.
- `serviceentitlements` — **Actions**: `list` | `get` | `create`. Cluster-scoped. `create` is how a project requests
  access to a service — what `datumctl services enable` does: `body.spec.serviceRef.name` (required, a name from
  `services`), `body.spec.requestMessage` (optional, for services that require provider approval). No `update`/`delete`
  — removing a project's access to a service isn't something to expose generically here. `ServiceConsumer` (the
  provider-side, approval-only object) is deliberately not exposed — its schema says providers never create these
  directly.

### Billing (toolset `billing`) — read-only
Reporting, not configuration.
- `billingaccounts` — **Actions**: `list` | `get`. Namespaced (`default`).
- `invoices` — **Actions**: `list` | `get`. Namespaced (`default`).

## Toolsets
Some curated resource tools are grouped into optional toolsets you can turn off with `DATUM_MCP_DISABLE_TOOLSETS`
(comma-separated, case-insensitive) if you want a leaner tool list for a given agent — e.g.
`DATUM_MCP_DISABLE_TOOLSETS=billing,iam,resource`. The original tool set (organizations/projects/users/domains/
httpproxies/httproutes/gateways/trafficprotectionpolicies/dnszones/dnsrecordsets/dnszoneclasses/apis/context) is always
on and can't be disabled this way. `resource` is always registered by default but, unlike the others, can be disabled
(`DATUM_MCP_DISABLE_TOOLSETS=resource`) if you want to fully close the generic escape hatch rather than rely on its
per-group restrictions.

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

