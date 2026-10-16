package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestServiceAccountProject(t *testing.T) {
	for _, tc := range []struct {
		username string
		want     string
		ok       bool
	}{
		{"robot@acme-prod.identity.miloapis.com", "acme-prod", true},
		{"ci-deployer@p-1234.identity.miloapis.com", "p-1234", true},
		{"someone@example.com", "", false},
		{"someone@datum.net", "", false},
		{"robot@.identity.miloapis.com", "", false},
		{"robot@a.b.identity.miloapis.com", "", false},
		{"@acme-prod.identity.miloapis.com", "", false},
		{"robot@acme-prod.identity.miloapis.com.evil.example", "", false},
		{"", "", false},
	} {
		got, ok := ServiceAccountProject(tc.username)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ServiceAccountProject(%q) = %q, %v; want %q, %v", tc.username, got, ok, tc.want, tc.ok)
		}
	}
}

func TestWhoAmIReadsSelfSubjectReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/apis/authentication.k8s.io/v1/selfsubjectreviews" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if b, _ := io.ReadAll(r.Body); !strings.Contains(string(b), `"kind":"SelfSubjectReview"`) {
			t.Errorf("expected a SelfSubjectReview body, got %s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"kind":"SelfSubjectReview","status":{"userInfo":{"username":"robot@acme-prod.identity.miloapis.com"}}}`)
	}))
	defer srv.Close()

	got, err := whoAmI(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "robot@acme-prod.identity.miloapis.com" {
		t.Errorf("expected the reviewed username, got %q", got)
	}
}

func TestGetProjectClassifiesFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/resourcemanager.miloapis.com/v1alpha1/projects/real":
			_, _ = io.WriteString(w, `{"kind":"Project","metadata":{"name":"real"}}`)
		case "/apis/resourcemanager.miloapis.com/v1alpha1/projects/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","status":"Failure","reason":"NotFound","code":404,"message":"projects \"missing\" not found"}`)
		default:
			// No Status body, as from a proxy in front of the API.
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	if err := getProject(ctx, srv.Client(), srv.URL, "real"); err != nil {
		t.Errorf("expected an existing project to verify, got %v", err)
	}
	if err := getProject(ctx, srv.Client(), srv.URL, "missing"); !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound, got %v", err)
	}
	if err := getProject(ctx, srv.Client(), srv.URL, "other"); !apierrors.IsForbidden(err) {
		t.Errorf("expected a bare 403 to classify as Forbidden, got %v", err)
	}
}
