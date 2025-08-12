# Datum MCP

Deterministic helper service that lets LLM agents discover, skeleton‑generate, prune, and validate **Datum Cloud CRDs** (HTTPProxy, Network*, …).  
Designed first and foremost for **Claude Desktop** via the Model‑Context‑Protocol (MCP).  
A lightweight FastAPI server exists, but the primary workflow is inside Claude.

---

## Requirements

| Tool             | Reason |
|------------------|--------|
| **Python 3.11+** | runtime |
| **kubeconform**  | strict schema validation |
| **git**          | one‑time fetch of Datum CRD YAMLs |

Python packages (installed via `requirements.txt`):

```text
fastapi
uvicorn[standard]
pydantic>=2
pyyaml>=6
```

---

## Quick‑start

```bash
# ─── system deps (macOS example) ───────────────────────────────
brew install python@3.11 kubeconform git

# ─── clone + venv ──────────────────────────────────────────────
git clone https://github.com/your-fork/datum_mcp_py.git
cd datum_mcp_py
python3.11 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# ─── copy Datum CRD schemas once ───────────────────────────────
git clone https://github.com/datum-cloud/network-services-operator.git
cp network-services-operator/config/crd/bases/*.yaml schema/

# ─── (optional) sanity‑check the CLI locally ───────────────────
python cli.py   # prints “STDIO mode ready”
                # then press Ctrl‑C to quit
```

> **Claude Desktop will start `cli.py` automatically** (see next section).  
> The FastAPI `--port` mode is available for ad‑hoc testing but isn’t required for day‑to‑day use.

---

## Using inside Claude Desktop

Add the `datum_mcp` entry to your `~/.claude/desktop.json`:

```jsonc
{
  "mcpServers": {
    "datum_mcp": {
      "command": "/Users/you/datum-mcp/.venv/bin/python",
      "args": ["/Users/you/datum-mcp/cli.py"]
    }
  }
}
```

1. Restart Claude Desktop.  
2. In any chat you can now invoke tools like `datum_list_crds`, `datum_skeleton_crd`, `datum_prune_crd`, … from the ⌥/**Tools** menu or via natural language (“Generate a skeleton HTTPProxy for …”).

---

## What’s in the box?

| Path / Tool | Purpose |
|-------------|---------|
| `server.py` | FastAPI backend (list / skeleton / prune / validate). Uses **kubeconform** for strict schema checking. |
| `cli.py`    | JSON‑RPC bridge that exposes the same functions as deterministic MCP tools. |
| `schema/`   | Datum CRD OpenAPI YAMLs (copied from the operator repo). |
| `examples/` | Few‑shot user/assistant pairs (handy for front‑end prompting). |

---

## License

Apache 2.0
