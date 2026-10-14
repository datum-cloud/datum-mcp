package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"net/url"
	"path/filepath"
	"sync"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/datum-cloud/datum-mcp/internal/auth"
	"github.com/datum-cloud/datum-mcp/internal/authutil"
)

// mappers holds one RESTMapper per control plane, keyed by host and path
// prefix. Each user, org and project control plane serves its own set of kinds,
// so a mapper discovered against one can't answer for another: sharing one
// across them made whichever was discovered first decide what every later call
// could see.
var (
	mappers   = map[string]meta.RESTMapper{}
	mappersMu sync.Mutex
)

// requestTimeout bounds how long a single attempt waits for a response to
// start (see withResponseHeaderTimeout), not the whole round trip including
// body read. client-go's own retry-on-429/5xx (up to 10 attempts, honoring
// Retry-After - see NewRequest's default in k8s.io/client-go/rest) applies
// around this per-attempt bound, not instead of it.
const requestTimeout = 30 * time.Second

func newPrefixedClient(ctx context.Context, basePrefix string) (ctrlclient.Client, error) {
	return newPrefixedClientWithScheme(ctx, basePrefix, runtime.NewScheme())
}

// newPrefixedClientWithScheme is newPrefixedClient for callers that decode
// into typed objects rather than unstructured ones, and so need their types
// registered.
func newPrefixedClientWithScheme(ctx context.Context, basePrefix string, scheme *runtime.Scheme) (ctrlclient.Client, error) {
	cfg, err := newPrefixedConfig(ctx, basePrefix)
	if err != nil {
		return nil, err
	}
	mapper, err := getOrCreateMapper(cfg, basePrefix)
	if err != nil {
		return nil, err
	}
	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// newPrefixedConfig returns a rest.Config addressing one control plane: every
// request carries the current token and has basePrefix prepended to its path.
func newPrefixedConfig(ctx context.Context, basePrefix string) (*rest.Config, error) {
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return nil, err
	}
	apiHost, err := authutil.GetAPIHostname()
	if err != nil {
		return nil, err
	}

	return &rest.Config{
		Host: "https://" + strings.TrimRight(apiHost, "/"),
		// WrapTransport to prefix base path
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			if rt == nil {
				rt = http.DefaultTransport
			}
			rt = withResponseHeaderTimeout(rt, requestTimeout)
			// Inject auth so first request triggers EnsureAuth (opens browser if needed),
			// then apply the project/org/user control-plane path prefix.
			authed := &authRoundTripper{next: rt}
			return &prefixRoundTripper{base: basePrefix, next: authed}
		},
	}, nil
}

func getOrCreateMapper(cfg *rest.Config, basePrefix string) (meta.RESTMapper, error) {
	mappersMu.Lock()
	defer mappersMu.Unlock()
	key := cfg.Host + basePrefix
	if m, ok := mappers[key]; ok {
		return m, nil
	}
	// Build a DeferredDiscoveryRESTMapper backed by on-disk cached discovery,
	// per host and control plane.
	cacheBaseDir := defaultCacheBaseDir()
	hostDir := safeHostComponent(cfg.Host)
	planeDir := safePathComponent(basePrefix)
	discoveryCacheDir := filepath.Join(cacheBaseDir, hostDir, planeDir, "discovery")
	httpCacheDir := filepath.Join(cacheBaseDir, hostDir, planeDir, "http")
	dc, err := disk.NewCachedDiscoveryClientForConfig(cfg, discoveryCacheDir, httpCacheDir, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	m := restmapper.NewDeferredDiscoveryRESTMapper(dc)
	mappers[key] = m
	return m, nil
}

// safePathComponent turns a control-plane path prefix into one directory name,
// e.g. "/apis/resourcemanager.miloapis.com/v1alpha1/projects/p/control-plane" ->
// "apis_resourcemanager.miloapis.com_v1alpha1_projects_p_control-plane". Every
// separator becomes "_", so a project name a caller passed can't climb out of
// the cache directory.
func safePathComponent(prefix string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, strings.Trim(prefix, "/"))
	if out == "" || strings.Trim(out, ".") == "" {
		return "root"
	}
	return out
}

func defaultCacheBaseDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "datum-mcp")
	}
	// Fallback to current directory if user cache dir is not available
	return ".kube-cache"
}

// safeHostComponent converts a full URL host into a filesystem-friendly segment
// e.g., "https://api.example.com" or "api.example.com" -> "api.example.com"
func safeHostComponent(host string) string {
	if host == "" {
		return "unknown-host"
	}
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		return u.Host
	}
	// host might already be just the hostname
	return strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
}

func NewUserControlPlaneClient(ctx context.Context, userID string) (ctrlclient.Client, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}
	base := "/apis/iam.miloapis.com/v1alpha1/users/" + userID + "/control-plane"
	return newPrefixedClient(ctx, base)
}

func NewOrgControlPlaneClient(ctx context.Context, org string) (ctrlclient.Client, error) {
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	base := "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/" + org + "/control-plane"
	return newPrefixedClient(ctx, base)
}

func NewProjectControlPlaneClient(ctx context.Context, project string) (ctrlclient.Client, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	return newPrefixedClient(ctx, projectControlPlanePrefix(project))
}

// NewProjectTypedClient is NewProjectControlPlaneClient decoding into the
// typed objects scheme registers, for code that reads typed objects rather
// than unstructured ones (e.g. the alb toolset).
func NewProjectTypedClient(ctx context.Context, project string, scheme *runtime.Scheme) (ctrlclient.Client, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	return newPrefixedClientWithScheme(ctx, projectControlPlanePrefix(project), scheme)
}

// NewProjectRESTConfig returns the rest.Config a project control-plane client
// is built from, for code that issues its own requests against that control
// plane's other APIs (e.g. the project logs API).
func NewProjectRESTConfig(ctx context.Context, project string) (*rest.Config, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	return newPrefixedConfig(ctx, projectControlPlanePrefix(project))
}

func projectControlPlanePrefix(project string) string {
	return "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane"
}

// NewProjectHTTPClient returns an HTTP client whose transport injects Authorization and the
// project control-plane base path prefix. Use with absolute URLs like "https://host" + path.
func NewProjectHTTPClient(ctx context.Context, project string) (*http.Client, string, error) {
	if project == "" {
		return nil, "", fmt.Errorf("project is required")
	}
	apiHost, err := authutil.GetAPIHostname()
	if err != nil {
		return nil, "", err
	}
	cfg := &rest.Config{ // host only, transport does auth+prefix
		Host: "https://" + strings.TrimRight(apiHost, "/"),
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			if rt == nil {
				rt = http.DefaultTransport
			}
			rt = withResponseHeaderTimeout(rt, requestTimeout)
			// Unlike the ctrlclient.Client path, this raw *http.Client never
			// goes through rest.Request, so it doesn't get client-go's own
			// retry-on-429/5xx for free; add it here.
			retried := &retryRoundTripper{next: rt}
			authed := &authRoundTripper{next: retried}
			return &prefixRoundTripper{base: projectControlPlanePrefix(project), next: authed}
		},
	}
	tr, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, "", err
	}
	// No Client.Timeout: that would cover the whole round trip including
	// body read (see withResponseHeaderTimeout). The ResponseHeaderTimeout
	// set above already bounds a stalled/dead connection per attempt.
	return &http.Client{Transport: tr}, cfg.Host, nil
}
