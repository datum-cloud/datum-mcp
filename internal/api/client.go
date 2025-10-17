package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// resolveGVKs resolves the preferred object and list GVKs for a given Group/Kind
// using the shared RESTMapper. It returns an error if mapping cannot be resolved.
func resolveGVKs(group, kind string) (schema.GroupVersionKind, schema.GroupVersionKind, error) {
	if sharedMapper == nil {
		return schema.GroupVersionKind{}, schema.GroupVersionKind{}, fmt.Errorf("rest mapper not initialized")
	}
	m, err := sharedMapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind})
	if err != nil || m == nil {
		return schema.GroupVersionKind{}, schema.GroupVersionKind{}, fmt.Errorf("failed to resolve GVK for %s/%s: %w", group, kind, err)
	}
	objGVK := m.GroupVersionKind
	listGVK := schema.GroupVersionKind{Group: objGVK.Group, Version: objGVK.Version, Kind: objGVK.Kind + "List"}
	return objGVK, listGVK, nil
}

// Generic unstructured helpers using the shared RESTMapper
func FetchObject(ctx context.Context, cli ctrlclient.Client, group, kind, namespace, name string) (*unstructured.Unstructured, error) {
	objGVK, _, err := resolveGVKs(group, kind)
	if err != nil {
		return nil, err
	}
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(objGVK)
	if err := cli.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: name}, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func FetchList(ctx context.Context, cli ctrlclient.Client, group, kind, namespace string) (*unstructured.UnstructuredList, error) {
	_, listGVK, err := resolveGVKs(group, kind)
	if err != nil {
		return nil, err
	}
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(listGVK)
	if namespace != "" {
		if err := cli.List(ctx, &list, ctrlclient.InNamespace(namespace)); err != nil {
			return nil, err
		}
	} else {
		if err := cli.List(ctx, &list); err != nil {
			return nil, err
		}
	}
	return &list, nil
}

func CreateObject(ctx context.Context, cli ctrlclient.Client, group, kind, namespace string, in any) (*unstructured.Unstructured, error) {
	objGVK, _, err := resolveGVKs(group, kind)
	if err != nil {
		return nil, err
	}
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(objGVK)
	if err := assignJSON(&obj.Object, in); err != nil {
		return nil, err
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	if err := cli.Create(ctx, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func UpdateObjectSpec(ctx context.Context, cli ctrlclient.Client, group, kind, namespace, name string, in any) (*unstructured.Unstructured, error) {
	objGVK, _, err := resolveGVKs(group, kind)
	if err != nil {
		return nil, err
	}
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(objGVK)
	if err := cli.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: name}, &obj); err != nil {
		return nil, err
	}
	var patch unstructured.Unstructured
	if err := assignJSON(&patch.Object, in); err != nil {
		return nil, err
	}
	if spec, found, _ := unstructured.NestedFieldNoCopy(patch.Object, "spec"); found {
		_ = unstructured.SetNestedField(obj.Object, spec, "spec")
	}
	if err := cli.Update(ctx, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func DeleteObject(ctx context.Context, cli ctrlclient.Client, group, kind, namespace, name string) error {
	objGVK, _, err := resolveGVKs(group, kind)
	if err != nil {
		return err
	}
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(objGVK)
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	obj.SetName(name)
	return cli.Delete(ctx, &obj)
}

// Discovery: CRD schema via OpenAPI v3 direct path: /openapi/v3/apis/<group>/<version>[/<kind>]
func GetResourceDefinition(ctx context.Context, project, group, version, kind string, out any) error {
	httpClient, host, err := NewProjectHTTPClient(ctx, project)
	if err != nil {
		return err
	}
	// Resolve via OpenAPI v3 index hashed URL, then GET component; we'll filter by kind later
	idxReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, host+"/openapi/v3", nil)
	idxResp, err := httpClient.Do(idxReq)
	if err != nil {
		return err
	}
	defer idxResp.Body.Close()
	if idxResp.StatusCode != http.StatusOK {
		return fmt.Errorf("openapi index status %d", idxResp.StatusCode)
	}
	var idx any
	if err := json.NewDecoder(idxResp.Body).Decode(&idx); err != nil {
		return err
	}
	target := "apis/" + strings.Trim(group, ".") + "/" + strings.Trim(version, ".")
	rel := ""
	if m, ok := idx.(map[string]any); ok {
		if r, ok := getIndexComponentRel(m, target); ok {
			rel = r
		}
	}
	if rel == "" {
		return fmt.Errorf("openapi v3 component for %s/%s not found under project", group, version)
	}
	compURL := host + rel
	doc, err := httpGetJSON(ctx, httpClient, compURL)
	if err != nil {
		return err
	}
	// If kind provided, filter the component to that kind's schema using x-kubernetes-group-version-kind
	if k := strings.TrimSpace(kind); k != "" {
		if m, ok := doc.(map[string]any); ok {
			if comps, ok := m["components"].(map[string]any); ok {
				if schemas, ok := comps["schemas"].(map[string]any); ok {
					for _, v := range schemas {
						sm, _ := v.(map[string]any)
						xgvk, _ := sm["x-kubernetes-group-version-kind"].([]any)
						for _, e := range xgvk {
							em, _ := e.(map[string]any)
							gStr, _ := em["group"].(string)
							vStr, _ := em["version"].(string)
							kStr, _ := em["kind"].(string)
							if strings.EqualFold(gStr, strings.Trim(group, ".")) && strings.EqualFold(vStr, strings.Trim(version, ".")) && strings.EqualFold(kStr, k) {
								return assignJSON(out, sm)
							}
						}
					}
				}
			}
		}
	}
	return assignJSON(out, doc)
}

// List CRDs under the project control-plane
func ListResourceDefinitions(ctx context.Context, project string, out any) error {
	httpClient, host, err := NewProjectHTTPClient(ctx, project)
	if err != nil {
		return err
	}
	// Step 1: Read project-prefixed OpenAPI v3 index and derive groups/versions from keys
	idxReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, host+"/openapi/v3", nil)
	idxResp, err := httpClient.Do(idxReq)
	if err != nil {
		return err
	}
	defer idxResp.Body.Close()
	if idxResp.StatusCode != http.StatusOK {
		return fmt.Errorf("openapi index status %d", idxResp.StatusCode)
	}
	var idx any
	if err := json.NewDecoder(idxResp.Body).Decode(&idx); err != nil {
		return err
	}
	groupToVersions := map[string]map[string]struct{}{}
	// If index has top-level "paths", iterate those; else iterate flat map keys
	if m, ok := idx.(map[string]any); ok {
		if pathsRaw, ok := m["paths"]; ok {
			if paths, ok := pathsRaw.(map[string]any); ok {
				for key := range paths {
					k := strings.TrimPrefix(key, "/")
					if !strings.HasPrefix(k, "apis/") {
						continue
					}
					parts := strings.Split(k, "/")
					if len(parts) < 3 {
						continue
					}
					g := parts[1]
					v := parts[2]
					if _, ok := groupToVersions[g]; !ok {
						groupToVersions[g] = map[string]struct{}{}
					}
					groupToVersions[g][v] = struct{}{}
				}
			}
		} else {
			for key := range m {
				k := strings.TrimPrefix(key, "/")
				if !strings.HasPrefix(k, "apis/") {
					continue
				}
				parts := strings.Split(k, "/")
				if len(parts) < 3 {
					continue
				}
				g := parts[1]
				v := parts[2]
				if _, ok := groupToVersions[g]; !ok {
					groupToVersions[g] = map[string]struct{}{}
				}
				groupToVersions[g][v] = struct{}{}
			}
		}
	}

	// Step 2: For each group/version, GET /apis/<group>/<version> and collect resources
	groupsOut := make([]map[string]any, 0, len(groupToVersions))
	for g, vset := range groupToVersions {
		versions := make([]map[string]any, 0, len(vset))
		for v := range vset {
			resources := make([]map[string]any, 0)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, host+"/apis/"+g+"/"+v, nil)
			if resp, err := httpClient.Do(req); err == nil && resp != nil && resp.StatusCode == http.StatusOK {
				var arl map[string]any
				if err := json.NewDecoder(resp.Body).Decode(&arl); err == nil {
					resArr, _ := arl["resources"].([]any)
					resources = make([]map[string]any, 0, len(resArr))
					for _, r := range resArr {
						rm, _ := r.(map[string]any)
						name, _ := rm["name"].(string)
						kind, _ := rm["kind"].(string)
						ns, _ := rm["namespaced"].(bool)
						if name == "" || kind == "" || strings.Contains(name, "/") {
							continue
						}
						resources = append(resources, map[string]any{"name": name, "kind": kind, "namespaced": ns})
					}
				}
				resp.Body.Close()
			} else if resp != nil {
				resp.Body.Close()
			}
			versions = append(versions, map[string]any{"version": v, "resources": resources})
		}
		groupsOut = append(groupsOut, map[string]any{"group": g, "versions": versions})
	}
	return assignJSON(out, map[string]any{"groups": groupsOut})
}

// assignJSON marshals v and unmarshals into out (pointer), accommodating *any receivers.
func assignJSON(out any, v any) error {
	if out == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	switch p := out.(type) {
	case *any:
		var m any
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		*p = m
		return nil
	default:
		return json.Unmarshal(b, out)
	}
}

// trimToStructure returns a reduced view of an OpenAPI schema focusing on
// structural shape: types, properties, items, required, and additionalProperties.
func trimToStructure(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any)
		// Allowed top-level structural keys
		if tv, ok := t["type"]; ok {
			out["type"] = tv
		}
		if rp, ok := t["required"]; ok {
			out["required"] = rp
		}
		if ap, ok := t["additionalProperties"]; ok {
			if mp, ok := ap.(map[string]any); ok {
				out["additionalProperties"] = trimToStructure(mp)
			} else {
				out["additionalProperties"] = ap
			}
		}
		if props, ok := t["properties"].(map[string]any); ok {
			trimmedProps := make(map[string]any, len(props))
			for name, pv := range props {
				trimmedProps[name] = trimToStructure(pv)
			}
			out["properties"] = trimmedProps
		}
		if items, ok := t["items"]; ok {
			out["items"] = trimToStructure(items)
		}
		// Keep composition keywords minimally
		for _, k := range []string{"oneOf", "anyOf", "allOf"} {
			if arr, ok := t[k].([]any); ok {
				trimmed := make([]any, 0, len(arr))
				for _, e := range arr {
					trimmed = append(trimmed, trimToStructure(e))
				}
				out[k] = trimmed
			}
		}
		return out
	case []any:
		trimmed := make([]any, 0, len(t))
		for _, e := range t {
			trimmed = append(trimmed, trimToStructure(e))
		}
		return trimmed
	default:
		return t
	}
}

// httpGetJSON issues a GET and decodes JSON into an any.
func httpGetJSON(ctx context.Context, client *http.Client, url string) (any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %d", url, resp.StatusCode)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// getIndexComponentRel returns the relative URL to a group/version component
// key (e.g., "apis/<group>/<version>") from the OpenAPI v3 index. It supports
// both index.paths and flat keys, and keys with or without a leading slash.
func getIndexComponentRel(index map[string]any, key string) (string, bool) {
	// Expect OpenAPI v3 index shape with a top-level "paths" object, whose
	// entries contain a serverRelativeURL (preferred) or url.
	paths, ok := index["paths"].(map[string]any)
	if !ok {
		return "", false
	}
	for _, k := range []string{key, "/" + key} {
		if ent, ok := paths[k].(map[string]any); ok {
			if s, _ := ent["serverRelativeURL"].(string); s != "" {
				return s, true
			}
			if s, _ := ent["url"].(string); s != "" {
				return s, true
			}
		}
	}
	return "", false
}
