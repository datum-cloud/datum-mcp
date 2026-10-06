# Tool reference

Full reference for every tool, toolset, and prompt this server exposes. See the [README](../README.md) for
installation and quick start.

## Run modes
- **Stdio** (default): the client spawns `datum-mcp` as a subprocess and talks JSON-RPC over stdin/stdout. What
  Claude Desktop, Claude Code, and Cursor all expect.
  ```bash
  datum-mcp
  ```
- **HTTP**: `datum-mcp` listens and serves MCP over streamable HTTP; your client connects **by URL** instead of
  spawning the process. Exits gracefully on SIGINT/SIGTERM.
  ```bash
  datum-mcp --mode http --host localhost --port 9000
  ```
  This transport has no authentication or Origin check of its own — every tool call runs with whatever Datum Cloud
  credentials this process has. `--host` refuses to bind anywhere but loopback unless you also pass
  `--allow-non-loopback`; only do that behind your own access control (reverse proxy, container network boundary, etc).

## Environment variables
- `DATUM_AUTH_HOSTNAME` (default `auth.datum.net`)
- `DATUM_API_HOSTNAME` (derived from auth host if unset)
- `DATUM_CLIENT_ID` (inferred for *.datum.net and *.staging.env.datum.net)
- `DATUM_TOKEN` (override bearer token; skips login)
- `DATUM_VERBOSE` (`true` to print verbose auth logs)
- `DATUM_USER_ID` (override user subject; otherwise from stored credentials)
- `DATUM_ORG` (active organization for project listing)
- `DATUM_MCP_DISABLE_TOOLSETS` (comma-separated, e.g. `billing,iam`; see "Toolsets" below)

### Running fully non-interactively (CI, an agent-hosted deployment, etc.)
Set **both** `DATUM_TOKEN` and `DATUM_API_HOSTNAME` (e.g. `api.datum.net`). Both are required together — `DATUM_TOKEN`
alone still falls through to requiring an interactive login just to learn the API hostname. With both set, no
keychain access and no browser is ever needed. `DATUM_USER_ID` is also required for `organizations` `list`/`set`
(which need a user ID to query memberships), since there's no stored-credentials subject to fall back on.

## Response and error format
- **List** responses: `{ "items": [...], "count": N, "continue": "..." }` (`continue` is `""` when there's no next page).
- **Get/create/update** responses: the resource with internal-only Kubernetes metadata stripped (`managedFields`,
  `generation`, `creationTimestamp`, `selfLink`). `uid` and `resourceVersion` are preserved.
- **Update** deep-merges `body.spec`: null deletes a field, nested objects merge recursively, arrays/scalars replace
  wholesale. Fields omitted from `body.spec` are untouched.
- **Errors**: `{ "error": "...", "suggested_action": { "tool": "...", "action": "...", "args": {...} } }`.
  `suggested_action` is populated whenever there's a clear recovery step.

## List pagination and filtering
Every `list` action accepts:
- `limit` — max items per page (default 100, clamped to 500; follow `continue` instead of a "list everything" mode).
- `continue` — the token from a previous `list` response.
- `labelSelector` — kubectl-style, e.g. `"team=edge,env!=prod"`.
- `fieldSelector` — kubectl-style, e.g. `"metadata.name=foo"`; only fields the resource registers as selectable work server-side.

## Dry-run and optimistic concurrency
(CRD-backed resource tools, and `projects` create)
- `dryRun: true` on `create`/`update`/`delete` validates and runs admission server-side without persisting; response
  is tagged `"dryRun": true`.
- `resourceVersion: "<value from a prior get>"` on `update`/`delete` enables optimistic concurrency: a conflicting
  change since that version fails with a conflict error instead of silently overwriting. Omit it for last-write-wins.

## Tools

- **organizations** — `list` | `get` | `set`. User resolution: `DATUM_USER_ID` env, else subject from stored credentials.
- **users** — `list`. Org-scoped memberships: `{ "action": "list", "org": "<org-id>" }`.
- **projects** — `list` | `get` | `set` | `create`. Org resolution: `org` input, else `DATUM_ORG`, else stored active org.
- **domains** — `list` | `get` | `create` | `update` | `delete`. Namespace `default`. Project resolution: `project`
  input, else active project (from `projects set`).
- **networkservices** — same shape as `domains`. The backend an `httpproxies` rule routes to
  (`spec.rules[].backends[].networkService`) — a Workload never auto-creates one, including on redeploy, so
  create/verify it before creating the HTTPProxy. `spec.networkInterfaces.selector.matchLabels` typically targets
  `compute.datumapis.com/workload-name: <workload-name>` to pick up that Workload's instances; `spec.ports` are the
  named ports to expose (must match a container port on the Workload).
- **httpproxies**, **httproutes**, **gateways**, **trafficprotectionpolicies** — same shape/behavior as `domains`.
  `httproutes` targets Gateway API HTTPRoute, `gateways` targets Gateway API Gateway, `trafficprotectionpolicies`
  targets either and is group/kind `networking.datumapis.com`/`TrafficProtectionPolicy`. For `httpproxies`, a custom
  hostname goes in `spec.hostnames` — **not** a hand-created `DNSRecordSet`. The Gateway controller auto-manages the
  correct DNS record for every `spec.hostnames` entry (visible in a `get` response's `status.hostnameStatuses[].dnsRecords`);
  a manually-created record for the same name conflicts with it and silently blocks certificate issuance.
- **dnszones** — full CRUD, namespace `default`, group/kind `dns.networking.miloapis.com`/`DNSZone`.
- **dnsrecordsets** — full CRUD, namespace `default`, group/kind `dns.networking.miloapis.com`/`DNSRecordSet`.
- **dnszoneclasses** — `list` | `get`, cluster-scoped, group/kind `dns.networking.miloapis.com`/`DNSZoneClass`.
- **apis** — `list` | `get`. CRD discovery via upstream OpenAPI/`kubectl explain` logic. `list` returns
  groups/versions/resources; `get` returns the schema for a kind (`detail: "structure"` for a condensed shape).
- **context** — no actions, call with `{}`. Stateless snapshot: `authenticated`, `active_organization`,
  `active_project`, reachable orgs/projects, and `next_step` (the exact next tool call, `null` once a project is
  active). Orgs/projects are capped at 500 each — `*_truncated` flags say when to page via the dedicated `list` instead.
- **resource** (generic escape hatch) — `list` | `get` | `create` | `update` | `delete` for any kind without a
  dedicated tool, under `*.datumapis.com`/`*.miloapis.com` or the Gateway API groups (everything else, e.g. core
  Kubernetes machinery, RBAC, is refused). Adds `group`, `kind` (required), `namespace` (required if the kind is
  namespaced). `iam.miloapis.com`/`billing.miloapis.com`/`services.miloapis.com` stay `list`/`get` only here too —
  this is never a wider door than the dedicated tool. Disable via `DATUM_MCP_DISABLE_TOOLSETS=resource`.

### IPAM (toolset `ipam`)
Mirrors `datumctl ipam`'s class/pool/claim/allocation grouping.
- `ipclasses` — `list` | `get`. Cluster-scoped, operator-authored.
- `ippools` — full CRUD. Cluster-scoped. Root pools declare a CIDR; child pools carve a sub-prefix from a parent.
- `ipclaims` — full CRUD. Namespaced. Names a class and scope, never a pool/CIDR directly — resolved server-side.
- `ipallocations` — `list` | `get` | `delete`. Namespaced. Created by the system when a claim is satisfied.

### Compute (toolset `compute`)
- `workloads` — full CRUD. Namespaced. Deeply nested spec — inspect via `apis` with `detail: "structure"` first.
- `instances` — `list` | `get` only. Namespaced. Comes from a Workload's rollout; change via the owning workload.

### Galactic VPC (toolset `vpc`)
- `networks`, `subnets`, `connectors` — full CRUD, namespaced. Other VPC resources (subnet claims, connector
  advertisements/classes, network policies/etc.) go through the generic `resource` tool.

### Activity (toolset `activity`)
- `activity` — `create` (primary) | `get` | `list` | `delete`. Cluster-scoped. `create` submits a query; results come
  back synchronously in `status.results`. Add `queryType`: `audit` | `events` | `feed`, each with its own `body.spec`
  shape (time range, CEL filter, etc. — see source for exact fields per type).

### Search (toolset `search`)
- `search` — `create` (primary) | `get` | `list` | `update` | `delete`. Same synchronous query pattern as `activity`.
  `body`: `spec.query` (required), `spec.limit`, `spec.targetResources` (optional kind scoping).

### IAM (toolset `iam`) — read-only
- `roles` — `list` | `get`. Namespaced. Datum IAM roles, not plain Kubernetes RBAC.
- `policybindings` — `list` | `get`. Namespaced. Binds a Role to subjects over a `resourceSelector`.

### Services (toolset `services`)
- `services` — `list` | `get`. Cluster-scoped service catalog.
- `serviceentitlements` — `list` | `get` | `create`. `create` requests access to a service (what
  `datumctl services enable` does). No update/delete.

### Billing (toolset `billing`) — read-only
- `billingaccounts`, `invoices` — `list` | `get`. Namespaced.

## Toolsets
Some tools are grouped into optional toolsets, disabled via `DATUM_MCP_DISABLE_TOOLSETS` (comma-separated,
case-insensitive), e.g. `DATUM_MCP_DISABLE_TOOLSETS=billing,iam,resource`. The core set (organizations, projects,
users, domains, httpproxies, httproutes, gateways, trafficprotectionpolicies, dnszones, dnsrecordsets,
dnszoneclasses, apis, context) can't be disabled this way.

## Prompts
- `deploy-http-proxy` (`backend_service`, `backend_port`, optional `path_prefix`) — expose a backend via an
  `HTTPProxy`, auto-provisioning its `Gateway`/`HTTPRoute`.
- `configure-dns` (`domain_name`) — create a managed `DNSZone` with its default NS `DNSRecordSet`.
- `onboard-to-project` (no args) — loop on `context` until an active organization and project are set.

## Recommended workflow
1. `context` → check what's already set up (skip to 5 if `next_step` is `null`)
2. `organizations` → list, then set active org
3. `projects` → list for the org, then set active project
4. `context` → confirm `next_step` is `null` (or use the `onboard-to-project` prompt for steps 1-4)
5. Use any resource tool for CRUD, or `apis` to inspect CRD schemas
