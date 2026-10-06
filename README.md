<p align="center">
  <img width="500" height="99" alt="Datum Logo" src="https://github.com/user-attachments/assets/c57d4f38-da4e-466b-9e77-dd862d72578d" />
  <h1 align="center">Datum MCP Server</h1>
  <p align="center">
    Empower agents to help you manage your network infrastructure
  </p>
</p>

An MCP server for Datum Cloud. Gives an agent tools to manage organizations/projects, networking (domains, HTTP
proxies/routes, gateways, traffic protection policies, DNS), compute workloads, IP address management, Galactic VPC,
IAM/billing/services, audit/activity search, and a generic escape hatch for any other Datum-managed resource. Auth is
OAuth 2.1 (PKCE) with system-keychain token storage. Speaks MCP over stdio or streamable HTTP.

See **[docs/TOOLS.md](docs/TOOLS.md)** for the full tool-by-tool reference, toolsets, and prompts.

## Install the binary

```bash
curl -fsSL https://github.com/datum-cloud/datum-mcp/releases/latest/download/install.sh | sh
```

Or download a binary directly from the [latest release](https://github.com/datum-cloud/datum-mcp/releases/latest)
(macOS/Linux/Windows, amd64/arm64), rename it to `datum-mcp` (`datum-mcp.exe` on Windows), and put it on your `PATH`.

Build from source instead: `go build ./cmd/datum-mcp`

## Register with your MCP client

**Claude Code:**
```bash
claude mcp add --scope user datum-mcp -- datum-mcp
```
(`--scope user` registers it for all projects; drop it to register for the current project only.)

**Claude Desktop** — add to your MCP config:
```json
{
  "datum-mcp": {
    "command": "datum-mcp",
    "args": []
  }
}
```

**Cursor:**

[![Install MCP Server](https://cursor.com/deeplink/mcp-install-light.svg)](https://cursor.com/en-US/install-mcp?name=datum-mcp&config=eyJ0eXBlIjoic3RkaW8iLCJlbnYiOnt9LCJjb21tYW5kIjoiL3Vzci9sb2NhbC9iaW4vZGF0dW0tbWNwICJ9)

**Windows** — point your client's config at the full install path, e.g. `command: "<path>\\datum-mcp.exe"`.

## Auth
On first use, the server opens a browser for OAuth (PKCE) and stores credentials in the system keychain; later calls
reuse/refresh the token automatically. For CI or headless use, see "Running fully non-interactively" in
[docs/TOOLS.md](docs/TOOLS.md#environment-variables).

## Recommended workflow
1. `context` → check what's already set up
2. `organizations` → list, then set active org
3. `projects` → list for the org, then set active project
4. Use any resource tool for CRUD, or `apis` to inspect CRD schemas

(Or just use the `onboard-to-project` prompt for steps 1-3.)
