"""
Datum MCP backend – deterministic helpers for
  • discovering CRDs,
  • emitting a minimal skeleton,
  • listing allowed spec.* paths,
  • pruning unknown keys or disallowed metadata,
  • validating YAML with kubeconform, and
  • serving few-shot examples for the frontend LLM.
"""

from __future__ import annotations
import re, subprocess, tempfile
from pathlib import Path
from typing import Dict, List, Tuple

import yaml
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

# ───── configuration ────────────────────────────────────────────────────
ROOT_DIR   = Path(__file__).parent.resolve()
SCHEMA_DIR = ROOT_DIR / "schema"
EX_DIR     = ROOT_DIR / "examples"

# Allow-lists for metadata -------------------------------
ALLOWED_META_ANNOTATIONS: set[str] = set()
ALLOWED_META_LABELS:      set[str] = set()

# ───── build lookup tables once ─────────────────────────────────────────
def _build_maps() -> Tuple[
    Dict[Tuple[str, str], set[str]],
    Dict[str, List[str]],
    Dict[Tuple[str, str], dict]
]:
    allowed, kind2api, raw = {}, {}, {}
    for crd_file in SCHEMA_DIR.glob("*.yaml"):
        doc   = yaml.safe_load(crd_file.read_text())
        group = doc["spec"]["group"]
        kind  = doc["spec"]["names"]["kind"]

        for ver in doc["spec"]["versions"]:
            api  = f"{group}/{ver['name']}"
            kind2api.setdefault(kind, []).append(api)

            spec_schema = ver["schema"]["openAPIV3Schema"]["properties"]["spec"]
            raw[(api, kind)] = spec_schema

            paths: set[str] = set()
            def walk(node, base):
                for k, val in node.get("properties", {}).items():
                    here = f"{base}.{k}" if base else k
                    paths.add(here)
                    walk(val, here)
                if "items" in node:
                    walk(node["items"], base)
            walk(spec_schema, "spec")
            allowed[(api, kind)] = paths
    return allowed, kind2api, raw

_ALLOWED, _K2A, _RAW_SCHEMA = _build_maps()
_IDX = re.compile(r"\[\d+]")             # strip list indices like [7]

# ───── load few-shot examples ───────────────────────────────────────────
def _load_examples() -> List[dict]:
    out: List[dict] = []
    for p in sorted(EX_DIR.glob("*.txt")):
        try:
            user_txt, yaml_block = map(str.strip, p.read_text().split("---", 1))
        except ValueError:
            continue                    # malformed file
        out.append({"user": user_txt, "assistant": yaml_block})
    return out

_EXAMPLES = _load_examples()

# ───── helpers ──────────────────────────────────────────────────────────
def _prune(doc: str) -> Tuple[str, List[str], List[str]]:
    """
    • Remove any spec.* field not in the schema allow-list.
    • Delete *all* metadata.annotations / metadata.labels except allow-listed ones.
    • Drop unknown top-level keys.
    Returns: (clean YAML, removed_spec_paths, removed_meta_paths)
    """
    data = yaml.safe_load(doc)
    api, kind = data.get("apiVersion"), data.get("kind")
    if (api, kind) not in _ALLOWED:
        raise HTTPException(400, f"{api}/{kind} is not an allowed CRD")

    # ------- prune spec.* ----------------------------------------------
    allowed = _ALLOWED[(api, kind)]
    removed_spec: List[str] = []

    def walk(node, dotted=""):
        if isinstance(node, dict):
            for k in list(node):
                here  = f"{dotted}.{k}" if dotted else k
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

    # ------- prune metadata --------------------------------------------
    removed_meta: List[str] = []
    meta = data.get("metadata", {})
    if isinstance(meta, dict):
        # annotations
        ann = meta.get("annotations", {})
        if isinstance(ann, dict):
            for k in list(ann):
                if k not in ALLOWED_META_ANNOTATIONS:
                    removed_meta.append(f"metadata.annotations.{k}")
                    ann.pop(k)
            if not ann:
                meta.pop("annotations", None)
        # labels
        lab = meta.get("labels", {})
        if isinstance(lab, dict):
            for k in list(lab):
                if k not in ALLOWED_META_LABELS:
                    removed_meta.append(f"metadata.labels.{k}")
                    lab.pop(k)
            if not lab:
                meta.pop("labels", None)
        if not meta:                    # strip empty metadata map
            data.pop("metadata", None)

    # ------- drop stray top-level keys ---------------------------------
    for top in list(data):
        if top not in {"apiVersion", "kind", "metadata", "spec"}:
            removed_meta.append(top)
            data.pop(top)

    cleaned = yaml.safe_dump(data, sort_keys=False)
    return cleaned, removed_spec, removed_meta


def _validate(yaml_doc: str) -> None:
    with tempfile.NamedTemporaryFile("w", delete=False) as fh:
        fh.write(yaml_doc)
        fh.flush()
        subprocess.check_output(
            ["kubeconform", "-strict",
             f"-schema-location=file://{SCHEMA_DIR}", fh.name],
            text=True, stderr=subprocess.STDOUT,
        )


def _make_skeleton(api: str, kind: str) -> str:
    spec_schema = _RAW_SCHEMA[(api, kind)]

    def build(node):
        if node.get("type") == "object":
            out = {}
            for k, sub in node.get("properties", {}).items():
                if k in node.get("required", []):
                    out[k] = build(sub)
            return out
        if node.get("type") == "array":
            return [build(node["items"])]
        return None

    skel = {
        "apiVersion": api,
        "kind": kind,
        "spec": build(spec_schema) or {},
    }
    return yaml.safe_dump(skel, sort_keys=False)

# ───── FastAPI app ──────────────────────────────────────────────────────
app = FastAPI()

# —— pydantic models
class ListCRDsResp(BaseModel): crds: List[Tuple[str, str]]
class SkeletonReq(BaseModel): apiVersion: str; kind: str
class SkeletonResp(BaseModel): yaml: str
class PruneReq(BaseModel): yaml: str
class PruneResp(BaseModel): yaml: str; removed: List[str]
class ValReq(BaseModel): yaml: str
class ListSupReq(BaseModel): apiVersion: str; kind: str
class ExamplesResp(BaseModel): examples: List[dict]

# —— endpoints
@app.get("/datum/list_crds", response_model=ListCRDsResp)
def list_crds():
    return {"crds": sorted(_ALLOWED.keys())}

@app.post("/datum/skeleton_crd", response_model=SkeletonResp)
def skeleton(req: SkeletonReq):
    key = (req.apiVersion, req.kind)
    if key not in _RAW_SCHEMA:
        raise HTTPException(400, "Unknown apiVersion/kind")
    return {"yaml": _make_skeleton(*key)}

@app.post("/datum/list_supported")
def list_supported(req: ListSupReq):
    key = (req.apiVersion, req.kind)
    if key not in _ALLOWED:
        raise HTTPException(400, "Unknown apiVersion/kind")
    return {"paths": sorted(_ALLOWED[key])}

@app.post("/datum/prune_crd", response_model=PruneResp)
def prune(req: PruneReq):
    cleaned, bad_spec, bad_meta = _prune(req.yaml)
    removed = bad_spec + bad_meta
    if removed:                         # strict mode
        raise HTTPException(422,
            "Unsupported fields stripped:\n- " + "\n- ".join(removed))
    return {"yaml": cleaned, "removed": []}

@app.post("/datum/validate_crd")
def validate(req: ValReq):
    try:
        _validate(req.yaml)
        return {"valid": True, "details": ""}
    except subprocess.CalledProcessError as e:
        return {"valid": False, "details": e.output}

@app.get("/datum/list_examples", response_model=ExamplesResp)
def list_examples():
    return {"examples": _EXAMPLES}

def run_http(port: int = 7777):
    import uvicorn
    uvicorn.run("server:app", host="127.0.0.1", port=port,
                log_level="warning", access_log=False)
