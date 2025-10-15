package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	netv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	iamv1alpha1 "go.miloapis.com/milo/pkg/apis/iam/v1alpha1"
	resmanv1alpha1 "go.miloapis.com/milo/pkg/apis/resourcemanager/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
)

func registerSchemes(s *runtime.Scheme) error {
	if err := resmanv1alpha1.AddToScheme(s); err != nil {
		return err
	}
	if err := iamv1alpha1.AddToScheme(s); err != nil {
		return err
	}
	if err := netv1alpha.AddToScheme(s); err != nil {
		return err
	}
	return nil
}

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
			return &prefixRoundTripper{base: basePrefix, next: rt}
		},
	}
	scheme := runtime.NewScheme()
	if err := registerSchemes(scheme); err != nil {
		return nil, err
	}
	c, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	return c, nil
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
