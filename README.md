<p align="center">
  <img width="60" src="assets/logo.png" alt="Datum logo">
</p>

<h1 align="center">Datum MCP Server</h1>

<p align="center">
  Tiny MCP bridge that lets agents use <code>kubectl</code> to discover CRDs and server-side validate manifests.
</p>

---

Datum provides an open-source network cloud platform for building and operating network-sensitive apps across managed global infra and your own clouds.  
This repo ships the <strong>Datum MCP server</strong> — a small Go binary that exposes deterministic tools to Model Context Protocol (MCP) clients (e.g., Claude Desktop) and to simple CLI/HTTP tests. The server talks to <strong>your current Kubernetes context via kubectl</strong> (no GitHub/OpenAPI cache).

- Website: https://www.datum.net

---

## What it does (kubectl-backed)

At startup the server relies on your <strong>local kubectl</strong> and kubeconfig:

- Lists CRDs from the active cluster: <code>kubectl get crd -o json</code>
- Fetches/describes a specific CRD: <code>kubectl get|describe crd &lt;name&gt;</code>
- Validates YAML via server-side dry-run: <code>kubectl apply --dry-run=server --validate=true -f -</code>

### Tools exposed

- <strong><code>datum_list_crds</code></strong> – return installed CRDs with <code>{name, group, kind, versions[]}</code>.
- <strong><code>datum_get_crd</code></strong> – get or describe a CRD by <strong>resource name</strong> (e.g. <code>httpproxies.networking.datumapis.com</code>).<br>
  Args: <code>{ "name": "...", "mode": "yaml|json|describe" }</code> (default <code>yaml</code>).
- <strong><code>datum_validate_yaml</code></strong> – server-side validation (no apply).<br>
  Args: <code>{ "yaml": "&lt;manifest text&gt;" }</code>

<blockquote>
MCP traffic uses <strong>STDIO</strong>. An optional local <strong>HTTP</strong> port is available for easy <code>curl</code> debugging.
</blockquote>

---

## Requirements

- Go <strong>1.20+</strong>
- <strong>kubectl</strong> on your <code>$PATH</code>
- A working <strong>kubeconfig</strong> & context (or pass <code>--kube-context</code>)
- RBAC that allows:
  - <code>get</code> on <code>customresourcedefinitions.apiextensions.k8s.io</code>
  - server-side dry-run of the resources you validate
- Optional: Claude Desktop (for MCP usage), <code>curl</code> for HTTP tests

---

## Install

```bash
git clone https://github.com/datum-cloud/datum-mcp.git
cd datum-mcp
go mod tidy
go build -o datum-mcp ./cmd/mcp

# (optional) put on PATH
sudo mv ./datum-mcp /usr/local/bin
```

---

## Run

Flags:

- <code>--port &lt;n&gt;</code> – also serve a local HTTP debug API on <code>127.0.0.1:&lt;n&gt;</code>
- <code>--kube-context &lt;name&gt;</code> – choose a kube context (otherwise uses current)
- <code>--namespace &lt;ns&gt;</code> – default namespace for validation (if YAML omits it)
- <code>--kubectl &lt;path&gt;</code> – path to kubectl (default <code>kubectl</code>)

Examples:

```bash
# Use current context
datum-mcp --port 8080

# Or pick an explicit context and default namespace
datum-mcp --port 8080 --kube-context kind-datum --namespace default
```

You should see:
```
[datum-mcp] STDIO mode ready
```

---

## HTTP debug (curl) quickstart

> MCP clients don’t use these endpoints; they’re just for manual testing.

<strong>List CRDs</strong>
```bash
curl -s http://127.0.0.1:8080/datum/list_crds | jq
```

<strong>Get a CRD (YAML / JSON / describe)</strong>
```bash
curl -s -X POST http://127.0.0.1:8080/datum/get_crd   -H 'Content-Type: application/json'   -d '{"name":"httpproxies.networking.datumapis.com","mode":"yaml"}' | head
```

<strong>Validate a manifest (server dry-run)</strong>
```bash
curl -s -X POST http://127.0.0.1:8080/datum/validate_yaml   -H 'Content-Type: application/json'   -d @- <<'JSON'
{"yaml":"apiVersion: v1
kind: ConfigMap
metadata:
  name: demo
  namespace: default
data:
  k: v
"}
JSON
# → {"valid":true,"output":"configmap/demo created (server dry run)"}
```

Multi-doc YAML (<code>---</code>) is supported.

---

## Use with Claude Desktop (MCP over STDIO)

Point Claude at the binary (not the HTTP port).

<strong>Config file</strong> (macOS):  
<code>~/Library/Application Support/Claude/claude_desktop_config.json</code>

Add or edit:

```json
{
  "mcpServers": {
    "datum_mcp": {
      "command": "/absolute/path/to/datum-mcp",
      "args": ["--kube-context","kind-datum","--namespace","default"]
    }
  }
}
```

Restart Claude Desktop. In a chat, enable the tools and ask it to call:

- <code>datum_list_crds</code>
- <code>datum_get_crd</code> with <code>{ "name": "...", "mode": "describe" }</code>
- <code>datum_validate_yaml</code> with your manifest

---

## Project layout

```
cmd/
  mcp/           # main.go (STDIO MCP; optional HTTP)
internal/
  kube/          # kubectl wrapper (list/get/validate)
  mcp/           # service (tool impl), stdio/http adapters
assets/
  logo.png
```

Build:

```bash
go build -o datum-mcp ./cmd/mcp
```

---

## Troubleshooting

- <strong>“context was not found for specified context”</strong>  
  You passed a non-existent <code>--kube-context</code>.  
  Check: <code>kubectl config get-contexts</code> or drop the flag to use the current context.

- <strong><code>kubectl: command not found</code></strong>  
  Install kubectl and ensure it’s on <code>$PATH</code>.

- <strong>RBAC/permission errors</strong> (e.g., when validating)  
  Dry-run still enforces authz. Check:  
  <code>kubectl auth can-i get crd</code> and permissions for the resources you validate.

- <strong>Validation says unknown field</strong>  
  That’s coming from the API server (good!). Fix the manifest or select the right CRD/version.

---

## License

See <a href="./LICENSE">LICENSE</a>.
