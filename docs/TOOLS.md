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
- `DATUM_PROJECT` (active project for every project-scoped tool; wins over one saved by `projects set`)
- `DATUM_MCP_ENABLE_TOOLSETS` (comma-separated allowlist, e.g. `dns`; unset means every toolset; see "Toolsets" below)
- `DATUM_MCP_DISABLE_TOOLSETS` (comma-separated, e.g. `billing,iam`; see "Toolsets" below)

### Running fully non-interactively (CI, an agent-hosted deployment, etc.)
Set **both** `DATUM_TOKEN` and `DATUM_API_HOSTNAME` (e.g. `api.datum.net`). Both are required together — `DATUM_TOKEN`
alone still needs the API hostname from somewhere, and without stored credentials from a prior interactive login to
derive it from, startup fails fast with an error telling you to also set `DATUM_API_HOSTNAME` (no browser is ever
opened on its account). With both env vars set, no keychain access and no browser is ever needed. `DATUM_USER_ID` is
also required for `organizations` `list`/`set` (which need a user ID to query memberships), since there's no
stored-credentials subject to fall back on.

### Running as a service account
A service account is confined to its own project and can't list organizations or projects, so the usual
`organizations` -> `projects` -> resource flow doesn't apply. It doesn't need to: with a service account's token in
`DATUM_TOKEN`, the server asks the API who it is (a `SelfSubjectReview`) and uses that service account's own project
whenever no other project is set. No `DATUM_ORG`, `DATUM_USER_ID` or `projects set` is needed; `context` and
`projects` `get` report `"service account"` as the project's source. Set `DATUM_PROJECT` (or pass `project` on a
call) to override it.

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
  `list`/`create` need an org; `set` verifies against the org's projects when it can, else by fetching the project
  directly, so it works without an org. `get` returns the active project and its `source`.
- **domains** — `list` | `get` | `create` | `update` | `delete`. Namespace `default`. Project resolution: `project`
  input, else `DATUM_PROJECT`, else active project (from `projects set`), else a service account's own project.
- **networkservices** (toolset `network`) — same shape as `domains`. The backend an `httpproxies` rule routes to
  (`spec.rules[].backends[].networkService`) — a Workload never auto-creates one, including on redeploy, so
  create/verify it before creating the HTTPProxy. `spec.networkInterfaces.selector.matchLabels` typically targets
  `compute.datumapis.com/workload-name: <workload-name>` to pick up that Workload's instances; `spec.ports` are the
  named ports to expose (must match a container port on the Workload).
- **httpproxies**, **httproutes**, **gateways**, **trafficprotectionpolicies** (toolset `alb`) — same shape/behavior as `domains`.
  `httproutes` targets Gateway API HTTPRoute, `gateways` targets Gateway API Gateway, `trafficprotectionpolicies`
  targets either and is group/kind `networking.datumapis.com`/`TrafficProtectionPolicy`. For `httpproxies`, a custom
  hostname goes in `spec.hostnames` — **not** a hand-created `DNSRecordSet`. The Gateway controller auto-manages the
  correct DNS record for every `spec.hostnames` entry (visible in a `get` response's `status.hostnameStatuses[].dnsRecords`);
  a manually-created record for the same name conflicts with it and silently blocks certificate issuance.
- **dnszones** (toolset `dns`) — full CRUD, namespace `default`, group/kind `dns.networking.miloapis.com`/`DNSZone`.
- **dnsrecordsets** (toolset `dns`) — full CRUD, namespace `default`, group/kind `dns.networking.miloapis.com`/`DNSRecordSet`.
- **dnszoneclasses** (toolset `dns`) — `list` | `get`, cluster-scoped, group/kind `dns.networking.miloapis.com`/`DNSZoneClass`.
- **apis** — `list` | `get`. CRD discovery via upstream OpenAPI/`kubectl explain` logic. `list` returns
  groups/versions/resources; `get` returns the schema for a kind (`detail: "structure"` for a condensed shape).
- **context** — no actions, call with `{}`. Stateless snapshot: `authenticated`, `active_organization`,
  `active_project` and its `project_source`, reachable orgs/projects, and `next_step` (the exact next tool call,
  `null` once a project is active). Orgs/projects are capped at 500 each — `*_truncated` flags say when to page via the
  dedicated `list` instead. A lookup that fails (e.g. an identity that can't list organizations) goes in `warnings`
  rather than failing the call.
- **resource** (generic escape hatch) — `list` | `get` | `create` | `update` | `delete` for any kind without a
  dedicated tool, under `*.datumapis.com`/`*.miloapis.com` or the Gateway API groups (everything else, e.g. core
  Kubernetes machinery, RBAC, is refused). Adds `group`, `kind` (required), `namespace` (required if the kind is
  namespaced). `iam.miloapis.com`/`billing.miloapis.com`/`services.miloapis.com` stay `list`/`get` only here too —
  this is never a wider door than the dedicated tool. Disable via `DATUM_MCP_DISABLE_TOOLSETS=resource`.

### Application Load Balancers (toolset `alb`) — read-only
An Application Load Balancer is an `HTTPProxy` plus what's attached to it (hostnames and their domains, origins,
traffic protection, basic auth). These tools are networking's own, mounted from
[network-services-operator](https://github.com/datum-cloud/network-services-operator) (`pkg/albagent`): the same
diagnosis, reason catalog and product decoding the in-product assistant and `datumctl alb` use. Every one reads the
**active project** and takes no `project` argument.
- `alb_guide` — call with no `skill` first: how a load balancer is put together, how to read its status without
  getting it wrong, and the skill list. Then `skill: "<name>"` loads one procedure (`alb-create`, `alb-not-serving`,
  `hostname-not-working`, `certificate-not-issued`, `dns-delegation`, `domain-verification`, `backend-not-reachable`,
  `edge-propagation`, `traffic-protection-triage`, `access-log-triage`). Each document arrives with a preamble mapping
  the assistant's plan/apply onto this server: `dryRun: true`, confirm with the user, then the same call for real.
- `alb_list` — every load balancer, worst first, with its root cause if something is wrong.
- `alb_get` — one load balancer as the product: hostnames and their progress, routes and origins, Force HTTPS,
  protection, basic-auth usernames (never passwords).
- `alb_diagnose` — why one isn't working, walked down to the hostname/service/record that's actually the cause, with
  who has to act and which skill to load. A clean result comes back `unverified`, not healthy: nothing reports whether
  the edge can serve it yet.
- `alb_reason_explain` — what a condition reason means; pass the condition type too (`Pending` alone has three meanings).
- `alb_traffic_summary` — requests that actually arrived, from access logs. Needs observability access in the project;
  reports "unavailable" rather than failing without it. No traffic is not a fault.

Changes still go through the write tools (`httpproxies`, `trafficprotectionpolicies`, `domains`, `resource` for
`SecurityPolicy`/`BackendTrafficPolicy`). Basic-auth passwords never go through this server: use
`datumctl alb auth set <name> --user <user> --password-stdin`.

### IPAM (toolset `ipam`)
Mirrors `datumctl ipam`'s class/pool/claim/allocation grouping.
- `ipclasses` — `list` | `get`. Cluster-scoped, operator-authored.
- `ippools` — full CRUD. Cluster-scoped. Root pools declare a CIDR; child pools carve a sub-prefix from a parent.
- `ipclaims` — full CRUD. Namespaced. Names a class and scope, never a pool/CIDR directly — resolved server-side.
- `ipallocations` — `list` | `get` | `delete`. Namespaced. Created by the system when a claim is satisfied.

### Networks (toolset `network`) — read-only
Networks and network services, as networking's own tools see them, mounted from
[network-services-operator](https://github.com/datum-cloud/network-services-operator) (`pkg/networkagent`): the same
diagnosis and reason catalog the in-product assistant and `datumctl network` use. Every one reads the **active
project** and takes no `project` argument.
- `network_guide` — call with no `skill` first: the network model, how to read status, and the skill list
  (`network-create`, `network-service-create`, `network-not-ready`, `network-location-triage`,
  `network-interface-triage`, `network-service-not-serving`, `network-deletion-stuck`).
- `network_list` / `network_get` / `network_diagnose` — every network worst first, one network in full, and why one (or
  a workload's use of it) isn't working.
- `network_service_list` / `network_service_diagnose` — the same for the NetworkServices a load balancer routes to.
- `network_reason_explain` — what a reason means on a given kind, and who acts.
- `network_render` — a `Network` or `NetworkService` manifest admission will accept. Writes nothing: create it with
  `networks`/`networkservices` (`dryRun: true` first).

### Compute (toolset `compute`)
- `workloads` — full CRUD. Namespaced. Deeply nested spec — inspect via `apis` with `detail: "structure"` first.
  Instances come from a Workload's rollout: read them with `compute_instances_list` (or `resource` with
  `group: compute.datumapis.com`, `kind: Instance`, `namespace: default` for one by name), and change them through the
  owning workload.

Compute's own read-only agent tools, mounted from [compute](https://github.com/datum-cloud/compute)
(`pkg/computeagent`) and reading the **active project**:
- `compute_guide` — call with no `skill` first: the compute model, how to read its conditions, and the skill list
  (`workload-create`, `workload-not-available`, `instance-not-ready`, `placement-triage`, `quota-triage`,
  `referenced-data-triage`, `stalled-transient`).
- `compute_workloads_list` / `compute_workloads_get` / `compute_instances_list` — workloads worst first with their root
  cause, one workload's full condition tree, and its instances.
- `compute_workload_diagnose` — why a workload isn't available, walked down to the deployment or instance at fault.
- `compute_reason_explain` — what a condition reason means.
- `compute_instance_types_list` / `compute_workload_render` — the sizes a workload may ask for, and a Workload manifest
  built from inputs. Neither writes: create it with `workloads` (`dryRun: true` first).

### DNS (toolset `dns`) — read-only
DNS's own tools, mounted from [dns-operator](https://github.com/datum-cloud/dns-operator) (`pkg/dnsagent`) and reading
the **active project**. Disabling `dns` also removes the `dnszones`/`dnsrecordsets`/`dnszoneclasses` tools.
- `dns_guide` — call with no `skill` first: the DNS model, how to read conditions and record ownership, and the skill
  list (`record-create`, `zone-not-resolving`, `record-not-programmed`, `conflicting-record`, `record-not-owner`,
  `delegation-check`, `domain-verification`, `managed-record-refused`, `zone-import`).
- `dns_zones_list` / `dns_zones_get` / `dns_zone_diagnose` / `dns_delegation_check` — zones with their derived
  status, one zone in full, why one isn't resolving, and whether its delegation is in place.
- `dns_records_list` / `dns_records_get` / `dns_record_diagnose` — the same for record sets, per owner name.
- `dns_zone_discovery_get` — what a domain resolves to today, for importing a zone managed elsewhere.
- `dns_record_render` — a DNSRecordSet manifest built from inputs. Writes nothing: create it with `dnsrecordsets`
  (`dryRun: true` first).

### Galactic VPC (toolset `vpc`)
- `networks`, `subnets`, `connectors` — full CRUD, namespaced. Other VPC resources (subnet claims, connector
  advertisements/classes, network policies/etc.) go through the generic `resource` tool.

### Activity (toolset `activity`)
- `activity` — `create` (primary) | `get` | `list` | `delete`. Cluster-scoped. `create` submits a query; results come
  back synchronously in `status.results`. Add `queryType`: `audit` | `events` | `feed`, each with its own `body.spec`
  shape (time range, CEL filter, etc. — see source for exact fields per type).

Activity's own read-only query tools, mounted from [activity](https://github.com/datum-cloud/activity)
(`pkg/mcp/tools`) and reading the **active project**: exactly the set the in-product assistant gets
(`httpserver.ExposedTools`), so raw audit logs and activity policies stay out.
- `activity_guide` — call with no `skill` first: how changes are recorded, which tool answers which question, and the
  skill list (`what-changed`, `who-changed-resource`, `failed-operation-triage`, `event-investigation`).
- `query_activities`, `get_activity_facets`, `get_activity_timeline`, `summarize_recent_activity`,
  `compare_activity_periods` — the human-readable activity feed: search it, break it down, and summarise or compare
  windows.
- `get_resource_history`, `get_user_activity_summary` — one resource's changes, one person's changes.
- `find_failed_operations` — requests that failed, as a fixed summarised set of fields.
- `query_events`, `get_event_facets` — Kubernetes events, up to 60 days back.

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
Every tool outside the core set (organizations, projects, users, domains, apis, context) belongs to an optional
toolset: `activity`, `alb`, `billing`, `compute`, `dns`, `iam`, `ipam`, `network`, `resource`, `search`, `services`,
`vpc`. A service's write tools share its toolset with its agent tools, so one name covers the whole service (e.g.
`dns` is `dnszones`/`dnsrecordsets`/`dnszoneclasses` plus every `dns_*` tool and `dns-*` prompt).

Both variables are comma-separated and case-insensitive:
- `DATUM_MCP_ENABLE_TOOLSETS` registers only the toolsets it names, plus core. Use it to keep the tool list small
  when this server runs alongside others: `DATUM_MCP_ENABLE_TOOLSETS=dns` is 19 tools instead of 74, and
  `DATUM_MCP_ENABLE_TOOLSETS=core` is the 6 core tools alone. Unset means every toolset.
- `DATUM_MCP_DISABLE_TOOLSETS` then removes toolsets from that, e.g. `DATUM_MCP_DISABLE_TOOLSETS=billing,iam,resource`.

Core can't be disabled. An unknown toolset name is ignored with a warning on stderr. The `deploy-http-proxy` prompt is
offered only when `alb` and `network` are both enabled, and `configure-dns` only when `dns` is.

## Prompts
- `deploy-http-proxy` (`backend_service`, `backend_port` — a port name, optional `path_prefix`) — expose a backend via an
  `HTTPProxy`, auto-provisioning its `Gateway`/`HTTPRoute`.
- `configure-dns` (`domain_name`) — create a managed `DNSZone` with its default NS `DNSRecordSet`.
- `onboard-to-project` (no args) — loop on `context` until an active organization and project are set.
- `alb-create`, `alb-not-serving`, `alb-hostname-not-working`, `alb-certificate-not-issued`, `alb-dns-delegation`,
  `alb-domain-verification`, `alb-backend-not-reachable`, `alb-edge-propagation`, `alb-traffic-protection-triage`,
  `alb-access-log-triage` (optional `load_balancer`) — one per networking skill (toolset `alb`); the same text
  `alb_guide` returns for that skill.
- `network-*` (optional `name`), `compute-*` (optional `workload`), `dns-*` (optional `zone`) and `activity-*`
  (optional `resource`) — likewise one per skill of the `network`, `compute`, `dns` and `activity` toolsets, named
  `<toolset>-<skill>` unless the skill already starts with it (e.g. `network-create`, `compute-workload-create`,
  `dns-record-create`, `activity-what-changed`).

## Recommended workflow
1. `context` → check what's already set up (skip to 5 if `next_step` is `null`)
2. `organizations` → list, then set active org
3. `projects` → list for the org, then set active project
4. `context` → confirm `next_step` is `null` (or use the `onboard-to-project` prompt for steps 1-4)
5. Use any resource tool for CRUD, or `apis` to inspect CRD schemas
