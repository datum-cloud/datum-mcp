package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// serviceAccountDomainSuffix is the domain the API puts on a service
// account's username: "<sa>@<project>.identity.miloapis.com".
const serviceAccountDomainSuffix = ".identity.miloapis.com"

// Identity is who the API authenticates this server's token as.
type Identity struct {
	// Username is a person's email, or a service account's
	// "<sa>@<project>.identity.miloapis.com".
	Username string
	// UID is the user ID the user control plane is addressed by.
	UID string
}

// WhoAmI returns who the API authenticates this server's token as, via a
// SelfSubjectReview. It's the API's own answer rather than anything read out
// of the token, so it's right for any token the API accepts.
func WhoAmI(ctx context.Context) (Identity, error) {
	cli, host, err := newPrefixedHTTPClient(ctx, "")
	if err != nil {
		return Identity{}, err
	}
	return whoAmI(ctx, cli, host)
}

func whoAmI(ctx context.Context, cli *http.Client, host string) (Identity, error) {
	body := []byte(`{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/apis/authentication.k8s.io/v1/selfsubjectreviews", bytes.NewReader(body))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Status struct {
			UserInfo struct {
				Username string `json:"username"`
				UID      string `json:"uid"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	if err := doJSON(cli, req, &out); err != nil {
		return Identity{}, err
	}
	if out.Status.UserInfo.Username == "" {
		return Identity{}, fmt.Errorf("SelfSubjectReview returned no username")
	}
	return Identity{Username: out.Status.UserInfo.Username, UID: out.Status.UserInfo.UID}, nil
}

// ServiceAccountProject returns the project a service account belongs to,
// parsed from its username, and false for any username that isn't a service
// account's (a person's email, say).
func ServiceAccountProject(username string) (string, bool) {
	at := strings.LastIndex(username, "@")
	if at <= 0 {
		return "", false
	}
	domain := username[at+1:]
	if !strings.HasSuffix(domain, serviceAccountDomainSuffix) {
		return "", false
	}
	p := strings.TrimSuffix(domain, serviceAccountDomainSuffix)
	// Project names are single DNS labels.
	if p == "" || strings.Contains(p, ".") {
		return "", false
	}
	return p, true
}

// GetProject checks that a project exists by fetching it directly, without
// needing the org-scoped list permission a service account doesn't have.
// Failures are *apierrors.StatusError, so apierrors.IsNotFound/IsForbidden
// work on them.
func GetProject(ctx context.Context, name string) error {
	cli, host, err := newPrefixedHTTPClient(ctx, "")
	if err != nil {
		return err
	}
	return getProject(ctx, cli, host, name)
}

func getProject(ctx context.Context, cli *http.Client, host, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/apis/resourcemanager.miloapis.com/v1alpha1/projects/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	return doJSON(cli, req, nil)
}

// doJSON sends req and decodes a 2xx response into out (when non-nil). Any
// other status comes back as an *apierrors.StatusError built from the
// response's own Status body, so callers can classify it the same way as an
// error from a controller-runtime client.
func doJSON(cli *http.Client, req *http.Request, out any) error {
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var st metav1.Status
		if json.Unmarshal(b, &st) != nil || st.Kind != "Status" {
			st = metav1.Status{Status: metav1.StatusFailure, Code: int32(resp.StatusCode),
				Message: fmt.Sprintf("%s %s: %s", req.Method, req.URL.Path, resp.Status)}
		}
		// apierrors.IsNotFound/IsForbidden fall back to the code when the
		// body carries no reason.
		if st.Code == 0 {
			st.Code = int32(resp.StatusCode)
		}
		return &apierrors.StatusError{ErrStatus: st}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}
