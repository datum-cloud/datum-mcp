# discovery.py
from __future__ import annotations

import json
import logging
from typing import Dict, Tuple, List, Set
from urllib.request import Request, urlopen
from urllib.error import URLError, HTTPError

import os
import ssl
import yaml

# Use certifi's CA bundle so HTTPS works even if system certs aren't wired up
try:
    import certifi  # pip install certifi
    _SSL_CTX = ssl.create_default_context(cafile=certifi.where())
except Exception:
    _SSL_CTX = ssl.create_default_context()

# ──────────────────────────────────────────────────────────────────────────────
# Hardcoded GitHub location of Datum CRD YAMLs (no env needed)
# ──────────────────────────────────────────────────────────────────────────────
GITHUB_REPO = "datum-cloud/network-services-operator"
GITHUB_REF  = "main"
GITHUB_DIR  = "config/crd/bases"


class DiscoveryCache:
    """
    Load CRD schemas from GitHub and populate lookup tables used by the MCP:
      - full_schema[(apiVersion, kind)] -> full object schema (OpenAPI fragment)
      - kind2api[kind]                  -> [apiVersions]
      - top_allowed[(apiVersion, kind)] -> top-level fields from schema 'properties'
      - allowed[(apiVersion, kind)]     -> collected 'spec.*' paths (if spec present)

    Notes:
      • Discovery is GitHub-only here (no /openapi/v3 calls).
      • We still TRY to init a Kubernetes DynamicClient so server.validate() can work.
        If kube is absent, self.dyn remains None and validate() should handle that.
    """

    def __init__(self, project: str | None = None, kube_context: str | None = None):
        self.log = logging.getLogger("datum-mcp.discovery")
        if not self.log.handlers:
            logging.basicConfig(level=logging.INFO, format="[datum-mcp] %(message)s")

        # Optional Kubernetes client for validate(); keep None if not available
        self.api = None
        self.dyn = None
        try:
            from kubernetes import config as kcfg, client as kclient
            from kubernetes.dynamic import DynamicClient
            try:
                if "KUBERNETES_SERVICE_HOST" in os.environ:
                    kcfg.load_incluster_config()
                else:
                    kcfg.load_kube_config(context=kube_context)
                self.api = kclient.ApiClient()
                self.dyn = DynamicClient(self.api)
            except Exception as e:
                self.log.info("kube client not initialized (validate() will be offline): %s", e)
        except Exception:
            self.log.info("kubernetes python client not installed; validate() will be offline")

        # Lookup tables
        self.allowed: Dict[Tuple[str, str], Set[str]] = {}
        self.top_allowed: Dict[Tuple[str, str], Set[str]] = {}
        self.kind2api: Dict[str, List[str]] = {}
        self.full_schema: Dict[Tuple[str, str], dict] = {}

        self.refresh()

    # ───────────────────────── HTTP helpers ─────────────────────────────

    def _fetch_json(self, url: str) -> dict:
        req = Request(url, headers={"User-Agent": "datum-mcp/2.2 (+python)", "Accept": "application/json"})
        try:
            with urlopen(req, timeout=30, context=_SSL_CTX) as r:
                return json.loads(r.read().decode("utf-8"))
        except HTTPError as e:
            raise RuntimeError(f"HTTP {e.code} fetching {url}: {e.reason}") from e
        except URLError as e:
            raise RuntimeError(f"Failed to fetch {url}: {e}") from e
        except Exception as e:
            raise RuntimeError(f"Invalid JSON from {url}: {e}") from e

    def _fetch_bytes(self, url: str) -> bytes:
        req = Request(url, headers={"User-Agent": "datum-mcp/2.2 (+python)", "Accept": "*/*"})
        try:
            with urlopen(req, timeout=30, context=_SSL_CTX) as r:
                return r.read()
        except HTTPError as e:
            raise RuntimeError(f"HTTP {e.code} fetching {url}: {e.reason}") from e
        except URLError as e:
            raise RuntimeError(f"Failed to fetch {url}: {e}") from e

    def _fetch_json_or_yaml_docs(self, url: str) -> List[dict]:
        raw = self._fetch_bytes(url)
        text = raw.decode("utf-8", "replace").lstrip()

        if text[:1] in ("{", "["):
            try:
                return [json.loads(text)]
            except Exception:
                pass

        docs: List[dict] = []
        for d in yaml.safe_load_all(text):
            if isinstance(d, dict):
                docs.append(d)
        if not docs:
            raise RuntimeError(f"Unsupported or empty document at {url}")
        return docs

    # ───────────────────────── Discovery ────────────────────────────────

    def refresh(self):
        """Reload schemas from the hardcoded GitHub directory."""
        self.allowed.clear()
        self.top_allowed.clear()
        self.kind2api.clear()
        self.full_schema.clear()

        dir_url = f"https://api.github.com/repos/{GITHUB_REPO}/contents/{GITHUB_DIR}?ref={GITHUB_REF}"
        entries = self._fetch_json(dir_url)
        if not isinstance(entries, list):
            raise RuntimeError(f"GitHub contents listing did not return a list: {dir_url}")

        for it in entries:
            if it.get("type") != "file":
                continue
            name = it.get("name", "")
            if not (name.endswith(".yaml") or name.endswith(".yml")):
                continue

            download_url = it.get("download_url") or f"https://raw.githubusercontent.com/{GITHUB_REPO}/{GITHUB_REF}/{it.get('path','').lstrip('/')}"
            for doc in self._fetch_json_or_yaml_docs(download_url):
                if doc.get("kind") == "CustomResourceDefinition" and str(doc.get("apiVersion", "")).startswith("apiextensions.k8s.io/"):
                    self._ingest_crd(doc)
                elif "components" in doc and "schemas" in (doc.get("components") or {}):
                    self._ingest_openapi(doc)

        self.log.info("discovery: loaded %d schemas from GitHub %s/%s/%s",
                      len(self.full_schema), GITHUB_REPO, GITHUB_REF, GITHUB_DIR)

    # ───────────────────────── Ingestion ────────────────────────────────

    def _ingest_openapi(self, spec: dict):
        schemas = (spec.get("components", {}) or {}).get("schemas", {}) or {}
        for _, schema in schemas.items():
            gvks = schema.get("x-kubernetes-group-version-kind") or []
            if not gvks:
                continue
            for gvk in gvks:
                group = gvk.get("group", "")
                version = gvk.get("version")
                kind = gvk.get("kind")
                if not version or not kind or str(kind).endswith("List"):
                    continue
                api = f"{group}/{version}" if group else version
                self._register_schema(api, kind, schema)

    def _ingest_crd(self, crd: dict):
        spec = crd.get("spec") or {}
        group = spec.get("group")
        names = spec.get("names") or {}
        kind = names.get("kind")
        versions = spec.get("versions") or []
        if not group or not kind or not versions:
            return

        for v in versions:
            if not v.get("served", True):
                continue
            ver = v.get("name")
            openapi = ((v.get("schema") or {}).get("openAPIV3Schema") or {})
            if not ver or not isinstance(openapi, dict):
                continue

            if "type" not in openapi:
                openapi["type"] = "object"
            if "properties" not in openapi:
                openapi["properties"] = {}

            api = f"{group}/{ver}"
            self._register_schema(api, kind, openapi)

    def _register_schema(self, api: str, kind: str, schema: dict):
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
        if node.get("x-kubernetes-preserve-unknown-fields") is True:
            out.add(base + ".*")

        for key in ("allOf", "anyOf", "oneOf"):
            if key in node and isinstance(node[key], list):
                for sub in node[key]:
                    self._collect_paths(sub or {}, base, out)

        t = node.get("type")
        if t == "object":
            props = node.get("properties", {}) or {}
            for k, sub in props.items():
                here = f"{base}.{k}" if base else k
                out.add(here)
                self._collect_paths(sub or {}, here, out)
            addl = node.get("additionalProperties")
            if isinstance(addl, dict):
                out.add(base + ".*")
                self._collect_paths(addl, base, out)
            elif addl is True:
                out.add(base + ".*")
        elif t == "array":
            self._collect_paths((node.get("items") or {}), base, out)
