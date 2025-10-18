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

	"github.com/datum-cloud/datum-mcp/internal/authutil"
)

var (
	sharedMapper   meta.RESTMapper
	sharedMapperMu sync.Mutex
)

func newPrefixedClient(ctx context.Context, basePrefix string, bearer string) (ctrlclient.Client, error) {
	apiHost, err := authutil.GetAPIHostname()
	if err != nil {
		return nil, err
	}

	cfg := &rest.Config{
		Host:        "https://" + strings.TrimRight(apiHost, "/"),
		BearerToken: bearer,
		// WrapTransport to prefix base path
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			if rt == nil {
				rt = http.DefaultTransport
			}
			// Inject auth so first request triggers EnsureAuth (opens browser if needed),
			// then apply the project/org/user control-plane path prefix.
			authed := &authRoundTripper{next: rt}
			return &prefixRoundTripper{base: basePrefix, next: authed}
		},
	}
	mapper, err := getOrCreateMapper(cfg)
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme, Mapper: mapper})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func getOrCreateMapper(cfg *rest.Config) (meta.RESTMapper, error) {
	sharedMapperMu.Lock()
	defer sharedMapperMu.Unlock()
	if sharedMapper != nil {
		return sharedMapper, nil
	}
	// Build a DeferredDiscoveryRESTMapper backed by on-disk cached discovery, per host
	cacheBaseDir := defaultCacheBaseDir()
	hostDir := safeHostComponent(cfg.Host)
	discoveryCacheDir := filepath.Join(cacheBaseDir, hostDir, "discovery")
	httpCacheDir := filepath.Join(cacheBaseDir, hostDir, "http")
	dc, err := disk.NewCachedDiscoveryClientForConfig(cfg, discoveryCacheDir, httpCacheDir, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	sharedMapper = restmapper.NewDeferredDiscoveryRESTMapper(dc)
	return sharedMapper, nil
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

func bearerFromKeychain(ctx context.Context) (string, error) {
	ts, err := authutil.GetTokenSource(ctx)
	if err != nil {
		return "", err
	}
	t, err := ts.Token()
	if err != nil {
		return "", err
	}
	if t == nil || t.AccessToken == "" {
		return "", fmt.Errorf("empty access token")
	}
	return t.AccessToken, nil
}

func NewUserControlPlaneClient(ctx context.Context, userID string) (ctrlclient.Client, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}
	bearer, err := bearerFromKeychain(ctx)
	if err != nil {
		return nil, err
	}
	base := "/apis/iam.miloapis.com/v1alpha1/users/" + userID + "/control-plane"
	return newPrefixedClient(ctx, base, bearer)
}

func NewOrgControlPlaneClient(ctx context.Context, org string) (ctrlclient.Client, error) {
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	bearer, err := bearerFromKeychain(ctx)
	if err != nil {
		return nil, err
	}
	base := "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/" + org + "/control-plane"
	return newPrefixedClient(ctx, base, bearer)
}

func NewProjectControlPlaneClient(ctx context.Context, project string) (ctrlclient.Client, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	bearer, err := bearerFromKeychain(ctx)
	if err != nil {
		return nil, err
	}
	base := "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane"
	return newPrefixedClient(ctx, base, bearer)
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
			authed := &authRoundTripper{next: rt}
			base := "/apis/resourcemanager.miloapis.com/v1alpha1/projects/" + project + "/control-plane"
			return &prefixRoundTripper{base: base, next: authed}
		},
	}
	tr, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, "", err
	}
	return &http.Client{Transport: tr}, cfg.Host, nil
}
