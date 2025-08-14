package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultRepo = "datum-cloud/network-services-operator"
	defaultRef  = "main"
	defaultDir  = "config/crd/bases"
)

// Cache loads CRD/OpenAPI schemas and builds lookup tables used by service tools:
//   - fullSchema[(api,kind)]
//   - kind2api[kind] -> []apiVersions
//   - topAllowed[(api,kind)] -> top-level properties
//   - allowed[(api,kind)] -> collected spec.* paths
type Cache struct {
	mu   sync.RWMutex
	http *http.Client

	// Config
	GitHubRepo string
	GitHubRef  string
	GitHubDir  string

	// Optional control-plane OpenAPI base; if set, we try it first.
	OpenAPIBase string
	BearerToken string

	// Data
	allowed    map[string]map[string]struct{} // key(api|kind) -> set(spec.*)
	topAllowed map[string]map[string]struct{} // key(api|kind) -> set(top-level props)
	kind2api   map[string][]string            // kind -> [apiVersion]
	fullSchema map[string]map[string]any      // key(api|kind) -> OpenAPI fragment
}

func New() *Cache {
	return &Cache{
		http:       &http.Client{Timeout: 30 * time.Second},
		GitHubRepo: defaultRepo,
		GitHubRef:  defaultRef,
		GitHubDir:  defaultDir,

		allowed:    make(map[string]map[string]struct{}),
		topAllowed: make(map[string]map[string]struct{}),
		kind2api:   make(map[string][]string),
		fullSchema: make(map[string]map[string]any),
	}
}

func joinKey(api, kind string) string { return api + "|" + kind }

func (c *Cache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowed = make(map[string]map[string]struct{})
	c.topAllowed = make(map[string]map[string]struct{})
	c.kind2api = make(map[string][]string)
	c.fullSchema = make(map[string]map[string]any)
}

func (c *Cache) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.Reset()

	// Optional control-plane OpenAPI first (non-fatal if it fails).
	if c.OpenAPIBase != "" {
		_ = c.fetchAndIngestOpenAPI(ctx, strings.TrimRight(c.OpenAPIBase, "/")+"/openapi/v3")
	}

	// GitHub CRD directory listing
	listURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", c.GitHubRepo, c.GitHubDir, c.GitHubRef)
	var entries []map[string]any
	if err := c.getJSON(ctx, listURL, &entries); err != nil {
		return fmt.Errorf("fetch GitHub dir: %w", err)
	}
	for _, it := range entries {
		if it["type"] != "file" {
			continue
		}
		name, _ := it["name"].(string)
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		downloadURL, _ := it["download_url"].(string)
		if downloadURL == "" {
			path, _ := it["path"].(string)
			downloadURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", c.GitHubRepo, c.GitHubRef, strings.TrimLeft(path, "/"))
		}
		raw, err := c.getBytes(ctx, downloadURL)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", downloadURL, err)
		}
		if err := c.ingestDocs(raw); err != nil {
			return fmt.Errorf("ingest %s: %w", downloadURL, err)
		}
	}
	return nil
}

func (c *Cache) fetchAndIngestOpenAPI(ctx context.Context, url string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "datum-mcp/2.2 (+go)")
	req.Header.Set("Accept", "application/json, */*")
	if c.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	var spec map[string]any
	// OpenAPI may be JSON or YAML
	if json.Unmarshal(body, &spec) != nil {
		if err := yaml.Unmarshal(body, &spec); err != nil {
			return err
		}
	}
	return c.ingestOpenAPI(spec)
}

func (c *Cache) getJSON(ctx context.Context, url string, into any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "datum-mcp/2.2 (+go)")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(into)
}

func (c *Cache) getBytes(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "datum-mcp/2.2 (+go)")
	req.Header.Set("Accept", "*/*")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}

func (c *Cache) ingestDocs(raw []byte) error {
	// Detect JSON vs YAML (possibly multi-doc)
	var first byte
	for _, b := range raw {
		if b <= ' ' {
			continue
		}
		first = b
		break
	}
	if first == '{' || first == '[' {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil {
			return c.ingestOpenAPI(m)
		}
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if doc == nil {
			continue
		}
		if k, _ := doc["kind"].(string); k == "CustomResourceDefinition" {
			if apiv, _ := doc["apiVersion"].(string); strings.HasPrefix(apiv, "apiextensions.k8s.io/") {
				if err := c.ingestCRD(doc); err != nil {
					return err
				}
				continue
			}
		}
		if comp, ok := doc["components"].(map[string]any); ok {
			if _, ok := comp["schemas"].(map[string]any); ok {
				if err := c.ingestOpenAPI(doc); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *Cache) ingestOpenAPI(spec map[string]any) error {
	comps, _ := spec["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	for _, v := range schemas {
		s, _ := v.(map[string]any)
		if s == nil {
			continue
		}
		gvks, ok := s["x-kubernetes-group-version-kind"].([]any)
		if !ok || len(gvks) == 0 {
			continue
		}
		for _, gvk := range gvks {
			gm, _ := gvk.(map[string]any)
			group, _ := gm["group"].(string)
			version, _ := gm["version"].(string)
			kind, _ := gm["kind"].(string)
			if version == "" || kind == "" || strings.HasSuffix(kind, "List") {
				continue
			}
			api := version
			if group != "" {
				api = group + "/" + version
			}
			c.registerSchema(api, kind, s)
		}
	}
	return nil
}

func (c *Cache) ingestCRD(crd map[string]any) error {
	spec, _ := crd["spec"].(map[string]any)
	group, _ := spec["group"].(string)
	names, _ := spec["names"].(map[string]any)
	kind, _ := names["kind"].(string)
	versions, _ := spec["versions"].([]any)
	if group == "" || kind == "" || len(versions) == 0 {
		return nil
	}
	for _, v := range versions {
		vm, _ := v.(map[string]any)
		if vm == nil {
			continue
		}
		if served, ok := vm["served"].(bool); ok && !served {
			continue
		}
		ver, _ := vm["name"].(string)
		schema := map[string]any{}
		if scm, ok := vm["schema"].(map[string]any); ok {
			if oap, ok := scm["openAPIV3Schema"].(map[string]any); ok {
				schema = oap
			}
		}
		if ver == "" || schema == nil {
			continue
		}
		if _, ok := schema["type"]; !ok {
			schema["type"] = "object"
		}
		if _, ok := schema["properties"]; !ok {
			schema["properties"] = map[string]any{}
		}
		api := group + "/" + ver
		c.registerSchema(api, kind, schema)
	}
	return nil
}

func (c *Cache) registerSchema(api, kind string, schema map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keyAK := joinKey(api, kind)
	c.fullSchema[keyAK] = schema

	// kind -> apiVersions (unique append)
	found := false
	for _, a := range c.kind2api[kind] {
		if a == api {
			found = true
			break
		}
	}
	if !found {
		c.kind2api[kind] = append(c.kind2api[kind], api)
	}

	props, _ := schema["properties"].(map[string]any)

	// top-level fields
	tset := make(map[string]struct{})
	for prop := range props {
		tset[prop] = struct{}{}
	}
	c.topAllowed[keyAK] = tset

	// spec.* collection if present
	if sp, ok := props["spec"].(map[string]any); ok {
		aset := make(map[string]struct{})
		c.collectPaths(sp, "spec", aset)
		c.allowed[keyAK] = aset
	}
}

func (c *Cache) collectPaths(node map[string]any, base string, out map[string]struct{}) {
	if node == nil {
		return
	}
	// wildcard allowances
	if b, ok := node["x-kubernetes-preserve-unknown-fields"].(bool); ok && b {
		out[base+".*"] = struct{}{}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := node[key].([]any); ok {
			for _, it := range arr {
				if m, ok := it.(map[string]any); ok {
					c.collectPaths(m, base, out)
				}
			}
		}
	}
	typ, _ := node["type"].(string)
	switch typ {
	case "object":
		props, _ := node["properties"].(map[string]any)
		for k, sub := range props {
			here := base
			if here != "" {
				here += "."
			}
			here += k
			out[here] = struct{}{}
			if sm, ok := sub.(map[string]any); ok {
				c.collectPaths(sm, here, out)
			}
		}
		switch ap := node["additionalProperties"].(type) {
		case map[string]any:
			out[base+".*"] = struct{}{}
			c.collectPaths(ap, base, out)
		case bool:
			if ap {
				out[base+".*"] = struct{}{}
			}
		}
	case "array":
		if it, ok := node["items"].(map[string]any); ok {
			c.collectPaths(it, base, out)
		}
	}
}

func (c *Cache) ListCRDs() [][2]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([][2]string, 0, len(c.fullSchema))
	for k := range c.fullSchema {
		parts := strings.SplitN(k, "|", 2)
		out = append(out, [2]string{parts[0], parts[1]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] == out[j][0] {
			return out[i][1] < out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	return out
}

func (c *Cache) FullCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.fullSchema)
}

func (c *Cache) Has(api, kind string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.fullSchema[joinKey(api, kind)]
	return ok
}

func (c *Cache) GetSchema(api, kind string) map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneMap(c.fullSchema[joinKey(api, kind)])
}

func (c *Cache) TopAllowed(api, kind string) map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSet(c.topAllowed[joinKey(api, kind)])
}

func (c *Cache) AllowedSpec(api, kind string) map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSet(c.allowed[joinKey(api, kind)])
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Skeleton builds a minimal YAML for api/kind using required fields.
func (c *Cache) Skeleton(api, kind string) (string, error) {
	schema := c.GetSchema(api, kind)
	if schema == nil {
		return "", fmt.Errorf("unknown apiVersion/kind")
	}
	props, _ := schema["properties"].(map[string]any)

	// IMPORTANT: recursive closure must be declared then assigned
	var build func(map[string]any) any
	build = func(node map[string]any) any {
		if node == nil {
			return nil
		}
		t, _ := node["type"].(string)
		switch t {
		case "object":
			out := map[string]any{}
			reqset := listToSet(node["required"])
			if pr, ok := node["properties"].(map[string]any); ok {
				for k, sub := range pr {
					if _, needed := reqset[k]; needed {
						if sm, ok := sub.(map[string]any); ok {
							out[k] = build(sm)
						} else {
							out[k] = nil
						}
					}
				}
			}
			return out
		case "array":
			if it, ok := node["items"].(map[string]any); ok {
				return []any{build(it)}
			}
			return []any{nil}
		default:
			return nil
		}
	}

	body := map[string]any{
		"apiVersion": api,
		"kind":       kind,
	}

	if ms, ok := props["metadata"].(map[string]any); ok {
		req := listToSet(ms["required"])
		meta := map[string]any{}
		if _, ok := req["name"]; ok {
			meta["name"] = ""
		}
		if _, ok := req["namespace"]; ok {
			meta["namespace"] = ""
		}
		if len(meta) > 0 {
			body["metadata"] = meta
		}
	}

	if sp, ok := props["spec"].(map[string]any); ok {
		if x := build(sp); x != nil {
			if m, ok := x.(map[string]any); ok && len(m) == 0 {
				body["spec"] = map[string]any{}
			} else {
				body["spec"] = x
			}
		} else {
			body["spec"] = map[string]any{}
		}
	} else {
		reqTop := listToSet(schema["required"])
		delete(reqTop, "apiVersion")
		delete(reqTop, "kind")
		delete(reqTop, "metadata")
		for k := range reqTop {
			if pr, ok := props[k].(map[string]any); ok {
				body[k] = build(pr)
			} else {
				body[k] = nil
			}
		}
	}

	y, err := yaml.Marshal(body)
	if err != nil {
		return "", err
	}
	return string(y), nil
}

func listToSet(v any) map[string]struct{} {
	out := map[string]struct{}{}
	switch vv := v.(type) {
	case []any:
		for _, x := range vv {
			if s, _ := x.(string); s != "" {
				out[s] = struct{}{}
			}
		}
	}
	return out
}

var idxRe = regexp.MustCompile(`\[\d+]`)

func StripIndices(path string) string {
	return idxRe.ReplaceAllString(path, "")
}

// IsAllowed checks exact match or wildcard "base.*" prefixes.
func IsAllowed(allowed map[string]struct{}, clean string) bool {
	if allowed == nil {
		return false
	}
	if _, ok := allowed[clean]; ok {
		return true
	}
	for k := range allowed {
		if strings.HasSuffix(k, ".*") {
			base := strings.TrimSuffix(k, ".*")
			if clean == base || strings.HasPrefix(clean, base+".") {
				return true
			}
		}
	}
	return false
}
