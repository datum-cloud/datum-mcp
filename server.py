# server.py
"""
Datum MCP backend – deterministic helpers for:
  • discovering available types via OpenAPI v3 (control plane) or GitHub CRDs,
  • emitting a minimal YAML skeleton,
  • listing allowed field paths (prefers spec.*),
  • pruning unknown/disallowed fields,
  • validating (local schema-only; no cluster dry-run).

Env:
  DATUM_PROJECT       – project id (enables control-plane /openapi/v3 route)
  DATUM_OPENAPI_BASE  – override full openapi base path if needed
  DATUM_KUBE_CONTEXT  – kubeconfig context name (optional)
"""
from __future__ import annotations

import os
import re
from pathlib import Path
from typing import Dict, List, Tuple

import yaml
from fastapi import FastAPI, HTTPException
from kubernetes.client.exceptions import ApiException  # harmless import even if unused
from pydantic import BaseModel

from discovery import DiscoveryCache

# ───── constants / config ──────────────────────────────────────────────

ROOT_DIR = Path(__file__).parent.resolve()

# Allow-lists for metadata; customize if you want to preserve specific keys
ALLOWED_META_ANNOTATIONS: set[str] = set()
ALLOWED_META_LABELS: set[str] = set()

# ───── discovery (built at import) ─────────────────────────────────────

_DISC = DiscoveryCache(
    project=os.getenv("DATUM_PROJECT"),
    kube_context=os.getenv("DATUM_KUBE_CONTEXT"),
)

_ALLOWED = _DISC.allowed          # spec.* paths per (api, kind)
_TOP_ALLOWED = _DISC.top_allowed  # top-level fields per (api, kind)
_K2A = _DISC.kind2api             # kind -> [apiVersions]
_FULL_SCHEMA = _DISC.full_schema  # full object schema per (api, kind)

_IDX = re.compile(r"\[\d+]")  # strip list indices like [7] from dotted paths


# ───── utils ───────────────────────────────────────────────────────────

def _prune(doc: str) -> tuple[str, List[str], List[str]]:
    """
    • Remove any spec.* field not in the schema allow-list (when spec exists).
    • Strip metadata.annotations/labels except allow-listed keys.
    • Drop unknown top-level keys (based on discovered top-level props).
    Returns: (clean YAML, removed_spec_paths, removed_meta_or_top_paths)
    """
    try:
        data = yaml.safe_load(doc) or {}
    except Exception as e:
        raise HTTPException(400, f"Invalid YAML: {e}")

    api, kind = data.get("apiVersion"), data.get("kind")
    if (api, kind) not in _FULL_SCHEMA:
        raise HTTPException(400, f"{api}/{kind} is not known to the control plane")

    removed_spec: List[str] = []
    removed_meta_or_top: List[str] = []

    # ----- prune spec.* if schema has spec subtree ---------------------
    if (api, kind) in _ALLOWED:
        allowed = _ALLOWED[(api, kind)]

        def walk(node, dotted=""):
            if isinstance(node, dict):
                for k in list(node.keys()):
                    here = f"{dotted}.{k}" if dotted else k
                    clean = _IDX.sub("", here)
                    if clean.startswith("spec.") and clean not in allowed:
                        removed_spec.append(clean)
                        node.pop(k)
                    else:
                        walk(node[k], here)
            elif isinstance(node, list):
                for i, x in enumerate(node):
                    walk(x, f"{dotted}[{i}]")

        walk(data)

    # ----- prune metadata annotations/labels ---------------------------
    meta = data.get("metadata", {})
    if isinstance(meta, dict):
        ann = meta.get("annotations", {})
        if isinstance(ann, dict):
            for k in list(ann.keys()):
                if k not in ALLOWED_META_ANNOTATIONS:
                    removed_meta_or_top.append(f"metadata.annotations.{k}")
                    ann.pop(k)
            if not ann:
                meta.pop("annotations", None)

        lab = meta.get("labels", {})
        if isinstance(lab, dict):
            for k in list(lab.keys()):
                if k not in ALLOWED_META_LABELS:
                    removed_meta_or_top.append(f"metadata.labels.{k}")
                    lab.pop(k)
            if not lab:
                meta.pop("labels", None)

        if not meta:
            data.pop("metadata", None)

    # ----- drop stray top-level keys using discovered props ------------
    allowed_top = _TOP_ALLOWED.get((api, kind), set())
    always = {"apiVersion", "kind", "metadata"}
    for top in list(data.keys()):
        if top not in (allowed_top | always):
            removed_meta_or_top.append(top)
            data.pop(top)

    cleaned = yaml.safe_dump(data, sort_keys=False)
    return cleaned, removed_spec, removed_meta_or_top


def _make_skeleton(api: str, kind: str) -> str:
    schema = _FULL_SCHEMA[(api, kind)]
    props = schema.get("properties", {}) or {}

    def build(node: dict):
        t = node.get("type")
        if t == "object":
            out = {}
            req = set(node.get("required", []) or [])
            for k, sub in (node.get("properties") or {}).items():
                if k in req:
                    out[k] = build(sub or {})
            return out
        if t == "array":
            return [build(node.get("items") or {})]
        return None  # primitive → neutral placeholder (null)

    body: Dict[str, object] = {"apiVersion": api, "kind": kind}

    # metadata (often requires name; sometimes namespace)
    meta_schema = props.get("metadata")
    if isinstance(meta_schema, dict):
        needed = set(meta_schema.get("required", []) or [])
        meta = {}
        if "name" in needed:
            meta["name"] = ""
        if "namespace" in needed:
            meta["namespace"] = ""
        if meta:
            body["metadata"] = meta

    # prefer spec subtree when present
    if "spec" in props:
        body["spec"] = build(props["spec"]) or {}
    else:
        # build minimal required top-level fields besides apiVersion/kind/metadata
        req = set(schema.get("required", []) or []) - {"apiVersion", "kind", "metadata"}
        for k in sorted(req):
            if k in props:
                body[k] = build(props[k])

    return yaml.safe_dump(body, sort_keys=False)


# ───── FastAPI types & app ─────────────────────────────────────────────

app = FastAPI()


class ListCRDsResp(BaseModel):
    crds: List[Tuple[str, str]]


class SkeletonReq(BaseModel):
    apiVersion: str
    kind: str


class SkeletonResp(BaseModel):
    yaml: str


class PruneReq(BaseModel):
    yaml: str


class PruneResp(BaseModel):
    yaml: str
    removed: List[str]


class ValReq(BaseModel):
    yaml: str


class ListSupReq(BaseModel):
    apiVersion: str
    kind: str


@app.get("/datum/list_crds", response_model=ListCRDsResp)
def list_crds():
    return {"crds": sorted(_FULL_SCHEMA.keys())}


@app.post("/datum/skeleton_crd", response_model=SkeletonResp)
def skeleton(req: SkeletonReq):
    key = (req.apiVersion, req.kind)
    if key not in _FULL_SCHEMA:
        raise HTTPException(400, "Unknown apiVersion/kind")
    return {"yaml": _make_skeleton(*key)}


@app.post("/datum/list_supported")
def list_supported(req: ListSupReq):
    key = (req.apiVersion, req.kind)
    if key not in _FULL_SCHEMA:
        raise HTTPException(400, "Unknown apiVersion/kind")
    # prefer spec.* paths; otherwise expose top-level fields excluding boilerplate
    if key in _ALLOWED:
        return {"paths": sorted(_ALLOWED[key])}
    tl = _TOP_ALLOWED.get(key, set()) - {"apiVersion", "kind", "metadata"}
    return {"paths": sorted(tl)}


@app.post("/datum/prune_crd", response_model=PruneResp)
def prune(req: PruneReq):
    cleaned, bad_spec, bad_meta_or_top = _prune(req.yaml)
    removed = bad_spec + bad_meta_or_top
    if removed:  # strict mode: surface removals as a 422
        raise HTTPException(
            422,
            "Unsupported fields stripped:\n- " + "\n- ".join(removed),
        )
    return {"yaml": cleaned, "removed": []}


@app.post("/datum/validate_crd")
def validate(req: ValReq):
    """
    Local-only validation:
      • YAML parse errors → invalid
      • Unknown apiVersion/kind → invalid
      • Any fields that would be pruned → invalid (report which)
      • Otherwise → valid (note: no cluster dry-run performed)
    """
    # Parse YAML
    try:
        _ = yaml.safe_load(req.yaml) or {}
    except Exception as e:
        return {"valid": False, "details": f"Invalid YAML: {e}"}

    # Use _prune in detect-only mode; convert HTTPException to a normal response
    try:
        _, bad_spec, bad_meta_or_top = _prune(req.yaml)
    except HTTPException as he:
        # e.g., unknown api/kind or invalid YAML caught inside _prune
        detail = getattr(he, "detail", str(he))
        return {"valid": False, "details": str(detail)}

    removed = bad_spec + bad_meta_or_top
    if removed:
        return {
            "valid": False,
            "details": "Unsupported fields (local schema): " + ", ".join(removed),
        }

    return {
        "valid": True,
        "details": "Local schema check passed (no cluster dry-run).",
    }


@app.post("/datum/refresh_discovery")
def refresh_discovery():
    try:
        _DISC.allowed.clear()
        _DISC.top_allowed.clear()
        _DISC.kind2api.clear()
        _DISC.full_schema.clear()
        _DISC.refresh()
        return {"ok": True, "count": len(_FULL_SCHEMA)}
    except Exception as e:
        raise HTTPException(500, f"refresh failed: {e}")


def run_http(port: int = 7777):
    import uvicorn
    uvicorn.run(
        "server:app",
        host="127.0.0.1",
        port=port,
        log_level="warning",
        access_log=False,
    )
