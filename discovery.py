# discovery.py
from __future__ import annotations

import json
import os
from typing import Dict, Tuple, List, Set

from kubernetes import config, client
from kubernetes.dynamic import DynamicClient


class DiscoveryCache:
    """
    Pull OpenAPI v3 specs from the control plane and build lookup tables:
      - full_schema[(apiVersion, kind)] -> full object schema
      - kind2api[kind]                  -> [apiVersions]
      - top_allowed[(apiVersion, kind)] -> top-level fields (props)
      - allowed[(apiVersion, kind)]     -> spec.* field paths (when spec exists)
    """

    def __init__(self, project: str | None = None, kube_context: str | None = None):
        # kube config
        if "KUBERNETES_SERVICE_HOST" in os.environ:
            config.load_incluster_config()
        else:
            config.load_kube_config(context=kube_context)

        self.api = client.ApiClient()
        self.dyn = DynamicClient(self.api)

        self.project = project or os.getenv("DATUM_PROJECT", "")
        base = os.getenv("DATUM_OPENAPI_BASE")
        if base:
            self.base = base.rstrip("/")
        elif self.project:
            self.base = f"/apis/resourcemanager.miloapis.com/v1alpha1/projects/{self.project}/control-plane/openapi/v3"
        else:
            # fall back to cluster's main OpenAPI index
            self.base = "/openapi/v3"

        self.allowed: Dict[Tuple[str, str], Set[str]] = {}
        self.top_allowed: Dict[Tuple[str, str], Set[str]] = {}
        self.kind2api: Dict[str, List[str]] = {}
        self.full_schema: Dict[Tuple[str, str], dict] = {}

        self.refresh()

    # ---- HTTP helpers -------------------------------------------------

    def _get_json(self, path: str) -> dict:
        """
        GET an apiserver-relative path (like kubectl --raw).
        """
        resp = self.api.call_api(path, "GET", _preload_content=False)[0]
        data = resp.data.decode("utf-8") if hasattr(resp, "data") else resp.read()
        return json.loads(data)

    # ---- discovery ----------------------------------------------------

    def refresh(self):
        """
        Refresh internal tables from the OpenAPI v3 index.
        """
        idx = self._get_json(self.base)
        paths = idx.get("paths") or {}
        for _, entry in paths.items():
            rel = entry.get("serverRelativeURL")
            if not rel:
                continue
            rel = rel if rel.startswith("/") else f"{self.base}/{rel}"
            spec = self._get_json(rel)
            self._ingest_spec(spec)

    def _ingest_spec(self, spec: dict):
        schemas = (spec.get("components", {}) or {}).get("schemas", {}) or {}
        for _, schema in schemas.items():
            gvks = schema.get("x-kubernetes-group-version-kind") or []
            if not gvks:
                continue
            for gvk in gvks:
                group = gvk.get("group", "")
                version = gvk.get("version")
                kind = gvk.get("kind")
                if not version or not kind or kind.endswith("List"):
                    continue
                api = f"{group}/{version}" if group else version

                self.full_schema[(api, kind)] = schema
                self.kind2api.setdefault(kind, [])
                if api not in self.kind2api[kind]:
                    self.kind2api[kind].append(api)

                props = schema.get("properties", {}) or {}
                self.top_allowed[(api, kind)] = set(props.keys())

                if "spec" in props:
                    s: Set[str] = set()
                    self._collect_paths(props["spec"], base="spec", out=s)
                    self.allowed[(api, kind)] = s

    def _collect_paths(self, node: dict, base: str, out: Set[str]):
        """
        Walk a JSONSchema fragment and collect nested field paths.
        Handles object/array, and merges allOf/anyOf/oneOf conservatively.
        """
        for key in ("allOf", "anyOf", "oneOf"):
            if key in node and isinstance(node[key], list):
                for sub in node[key]:
                    self._collect_paths(sub or {}, base, out)

        t = node.get("type")
        if t == "object":
            props = node.get("properties", {}) or {}
            req = set(node.get("required", []) or [])
            # record all visible props
            for k, sub in props.items():
                here = f"{base}.{k}" if base else k
                out.add(here)
                self._collect_paths(sub or {}, here, out)
            addl = node.get("additionalProperties")
            if isinstance(addl, dict):
                # map[string]T — we can’t know keys; mark wildcard
                out.add(base + ".*")
                self._collect_paths(addl, base, out)
        elif t == "array":
            self._collect_paths((node.get("items") or {}), base, out)
