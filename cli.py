# cli.py
"""
Datum MCP JSON-RPC bridge exposing deterministic tools.

Tools:
  • datum_list_crds
  • datum_skeleton_crd
  • datum_list_supported
  • datum_prune_crd
  • datum_validate_crd
  • datum_list_examples
  • datum_refresh_discovery
"""
from __future__ import annotations

import argparse
import asyncio
import json
import logging
import sys
from typing import Any, Callable

# ——— import backend ————————————————
try:
    import server
except ImportError:
    class _Stub:
        def list_crds(self):     return {"crds": [("g/v", "Kind")]}
        def list_examples(self): return {"examples": []}
        class SkeletonReq:
            def __init__(self, apiVersion: str, kind: str):
                self.apiVersion, self.kind = apiVersion, kind
        class ListSupReq:
            def __init__(self, apiVersion: str, kind: str):
                self.apiVersion, self.kind = apiVersion, kind
        class PruneReq:
            def __init__(self, yaml: str): self.yaml = yaml
        class ValReq:
            def __init__(self, yaml: str): self.yaml = yaml
        def skeleton(self, _):        return {"yaml": "# stub"}
        def list_supported(self, _):  return {"paths": []}
        def prune(self, _):           return {"yaml": "# stub", "removed": []}
        def validate(self, _):        return {"valid": True, "details": ""}
        def refresh_discovery(self):  return {"ok": True, "count": 0}
    server = _Stub()  # type: ignore

# ——— tool catalogue — advertised to the LLM ————————————————
TOOLS = [
    {"name": "datum_list_crds",
     "description": "List all apiVersion/kind pairs known to the control plane.",
     "inputSchema": {"type": "object", "properties": {}, "required": []}},
    {"name": "datum_skeleton_crd",
     "description": "Return minimal YAML skeleton for apiVersion+kind.",
     "inputSchema": {"type": "object", "properties": {
         "apiVersion": {"type": "string"},
         "kind": {"type": "string"}
     }, "required": ["apiVersion", "kind"]}},
    {"name": "datum_list_supported",
     "description": "List legal field paths (prefers spec.* when present; otherwise top-level fields).",
     "inputSchema": {"type": "object", "properties": {
         "apiVersion": {"type": "string"},
         "kind": {"type": "string"}
     }, "required": ["apiVersion", "kind"]}},
    {"name": "datum_prune_crd",
     "description": "Strip unsupported fields (422 if any were removed).",
     "inputSchema": {"type": "object", "properties": {
         "yaml": {"type": "string"}
     }, "required": ["yaml"]}},
    {"name": "datum_validate_crd",
     "description": "Validate with Kubernetes server dry-run (Strict field validation).",
     "inputSchema": {"type": "object", "properties": {
         "yaml": {"type": "string"}
     }, "required": ["yaml"]}},
    {"name": "datum_list_examples",
     "description": "Return few-shot examples.",
     "inputSchema": {"type": "object", "properties": {}, "required": []}},
    {"name": "datum_refresh_discovery",
     "description": "Refresh the OpenAPI discovery cache.",
     "inputSchema": {"type": "object", "properties": {}, "required": []}},
]

HANDLERS: dict[str, tuple[Callable, Any | None]] = {
    "datum_list_crds":         (server.list_crds,          None),
    "datum_skeleton_crd":      (server.skeleton,           server.SkeletonReq),
    "datum_list_supported":    (server.list_supported,     server.ListSupReq),
    "datum_prune_crd":         (server.prune,              server.PruneReq),
    "datum_validate_crd":      (server.validate,           server.ValReq),
    "datum_list_examples":     (server.list_examples,      None),
    "datum_refresh_discovery": (server.refresh_discovery,  None),
}

IGNORED = {
    "resources/list", "prompts/list",
    "notifications/cancelled", "notifications/initialized",
}

# ——— logging & CLI args ————————————————————————————————
logging.basicConfig(level=logging.INFO,
                    format="[datum-mcp] %(message)s", stream=sys.stderr)
log = logging.getLogger("datum-mcp")

ap = argparse.ArgumentParser()
ap.add_argument("--port", type=int, help="Run FastAPI HTTP server on this port")
args = ap.parse_args()
if args.port:
    server.run_http(args.port)

# ——— helper I/O ————————————————————————————————————————
async def _read() -> str | None:
    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, sys.stdin.readline) or None

async def _send(obj: dict):
    # ensure_ascii=False keeps unicode nice in terminals
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()

def _jsonify(o: Any) -> Any:
    """
    Recursively convert objects into JSON-serializable types.
    - pydantic BaseModel -> dict
    - bytes/bytearray    -> utf-8 string (replace errors)
    - dict/list/tuple    -> recurse
    - everything else    -> as-is (let json.dumps handle primitives)
    """
    # pydantic models → dict
    try:
        from pydantic import BaseModel  # type: ignore
        if isinstance(o, BaseModel):
            return o.model_dump()
    except Exception:
        pass

    # bytes → text
    if isinstance(o, (bytes, bytearray)):
        try:
            return o.decode("utf-8", "replace")
        except Exception:
            # ensure we never bubble bytes upward
            return str(o)

    # containers → recurse
    if isinstance(o, dict):
        return {k: _jsonify(v) for k, v in o.items()}
    if isinstance(o, (list, tuple)):
        return [_jsonify(x) for x in o]

    # exceptions → string (extra safety)
    if isinstance(o, BaseException):
        return str(o)

    return o

# ——— main loop ————————————————————————————————————————
async def main():
    log.info("STDIO mode ready")
    while True:
        raw = await _read()
        if not raw:
            break
        try:
            req = json.loads(raw)
        except json.JSONDecodeError:
            continue

        mid = req.get("id")
        meth = req.get("method")
        p = req.get("params", {}) or {}

        try:
            # handshake --------------------------------------------------
            if meth == "initialize":
                await _send({"jsonrpc": "2.0", "id": mid,
                             "result": {"protocolVersion": "2025-06-18",
                                        "serverInfo": {"name": "datum-mcp", "version": "2.1.0"},
                                        "capabilities": {}}})
                await _send({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})
                continue

            # noisy notifications ---------------------------------------
            if meth in IGNORED:
                if mid is not None:
                    await _send({"jsonrpc": "2.0", "id": mid, "result": {meth.split('/')[0]: []}})
                continue

            # tool discovery --------------------------------------------
            if meth == "tools/list":
                await _send({"jsonrpc": "2.0", "id": mid, "result": {"tools": TOOLS}})
                continue

            # tool execution --------------------------------------------
            if meth == "tools/call":
                name = p["name"]; argv = p.get("arguments", {}) or {}
                fn, Ty = HANDLERS[name]
                if asyncio.iscoroutinefunction(fn):
                    res = (await fn(Ty(**argv)) if Ty else await fn())
                else:
                    res = (fn(Ty(**argv)) if Ty else fn())

                payload = _jsonify(res)
                # as extra defense, if something still slips through, stringify on failure
                try:
                    text = json.dumps(payload, indent=2, ensure_ascii=False)
                except TypeError:
                    text = json.dumps(str(payload), ensure_ascii=False)

                await _send({"jsonrpc": "2.0", "id": mid,
                             "result": {"content": [{"type": "text", "text": text}]}})
                continue

            # unknown ---------------------------------------------------
            if mid is not None:
                await _send({"jsonrpc": "2.0", "id": mid,
                             "error": {"code": -32601, "message": f"Unknown method {meth}"}})
        except Exception as exc:
            if mid is not None:
                await _send({"jsonrpc": "2.0", "id": mid,
                             "error": {"code": -32603, "message": str(exc)}})

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        pass
