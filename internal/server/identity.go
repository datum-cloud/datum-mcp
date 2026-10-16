package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/datum-cloud/datum-mcp/internal/api"
	"github.com/datum-cloud/datum-mcp/internal/auth"
	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/project"
)

// Where the active project came from, reported by 'context' and 'projects'
// get so an agent can tell an explicit choice from an inferred one.
const (
	projectSourceEnv            = "DATUM_PROJECT"
	projectSourceStored         = "projects set"
	projectSourceServiceAccount = "service account"
)

// whoAmI is api.WhoAmI, swappable in tests.
var whoAmI = api.WhoAmI

// The last token serviceAccountProject looked up, and its answer. One entry is
// enough: a server runs as one identity, and only a keyring login's hourly
// refresh changes the token.
var (
	saProjectMu    sync.Mutex
	saProjectToken string
	saProjectName  string
)

// serviceAccountProject returns the project this server's token belongs to
// when the token is a service account's, and "" otherwise. A service account
// can't list organizations or projects, so without this it could never find
// the one project it's allowed into. It never starts a login: with no token
// already available it just returns "".
func serviceAccountProject(ctx context.Context) string {
	tok, ok := auth.CheckAuth(ctx)
	if !ok {
		return ""
	}
	saProjectMu.Lock()
	defer saProjectMu.Unlock()
	if tok == saProjectToken {
		return saProjectName
	}
	user, err := whoAmI(ctx)
	if err != nil {
		// Not cached, so a transient failure doesn't stick.
		return ""
	}
	p, _ := api.ServiceAccountProject(user)
	saProjectToken, saProjectName = tok, p
	return p
}

// activeProjectOrEmpty returns the project tools use when no 'project' is
// passed, and where it came from: DATUM_PROJECT, else the one saved by
// 'projects' set, else a service account's own project. "" when none apply.
func activeProjectOrEmpty(ctx context.Context) (name, source string) {
	if v := os.Getenv("DATUM_PROJECT"); v != "" {
		return v, projectSourceEnv
	}
	if p, _ := project.GetActive(); p != "" {
		return p, projectSourceStored
	}
	if p := serviceAccountProject(ctx); p != "" {
		return p, projectSourceServiceAccount
	}
	return "", ""
}

// currentUserID is the user whose organization memberships 'organizations'
// and 'context' list: DATUM_USER_ID, else the stored login's subject.
func currentUserID() (string, error) {
	if v := os.Getenv("DATUM_USER_ID"); v != "" {
		return v, nil
	}
	id, err := authutil.GetSubject()
	if err != nil {
		return "", fmt.Errorf("failed to determine user ID: %w", err)
	}
	return id, nil
}

// explicitProjectHint is the way forward for an identity that can't discover
// organizations or projects (a service account, typically): name the project
// directly.
const explicitProjectHint = "if you know the project id, pass 'project' on each call, set DATUM_PROJECT, or call 'projects' " +
	"with action=set and body.name"

// discoveryErr is errResult for a failed organization or project lookup. A
// service account can't make either, and pointing it back at another listing
// call only sends it round in a loop, so when that's what went wrong this
// says so and points at naming the project directly instead.
func discoveryErr(ctx context.Context, err error) (*mcp.CallToolResult, any, error) {
	if p := serviceAccountProject(ctx); p != "" {
		return errResult(fmt.Errorf("%w; this token is a service account for project %q, which tools use by default, "+
			"so no organization or project needs to be set", err, p), &SuggestedAction{Tool: "projects", Action: "get"})
	}
	if apierrors.IsForbidden(err) {
		return errResult(fmt.Errorf("%w; this identity can't list organizations or projects; %s", err, explicitProjectHint),
			&SuggestedAction{Tool: "projects", Action: "set"})
	}
	return errResult(err, nil)
}

// verifyProject checks that name is a project this identity can use before
// 'projects' set saves it. Checking against the org's project list needs an
// organization and permission to list its projects, which a service account
// has neither of, so it falls back to fetching the project itself.
func verifyProject(ctx context.Context, orgOverride, name string) (*SuggestedAction, error) {
	if sa := serviceAccountProject(ctx); sa != "" && strings.EqualFold(sa, name) {
		return nil, nil
	}
	if orgName, err := resolveOrgName(orgOverride); err == nil {
		cli, err := api.NewOrgControlPlaneClient(ctx, orgName)
		if err != nil {
			return nil, err
		}
		// Membership verification always checks the full set, independent
		// of any list-only pagination the caller passed in, and must not
		// miss a project just because the org has more than one page.
		items, err := api.FetchAllItems(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err == nil {
			for _, it := range items {
				if strings.EqualFold(it.GetName(), name) {
					return nil, nil
				}
			}
			return suggestListProjects(), fmt.Errorf("project %q not found in org %q", name, orgName)
		}
		if !apierrors.IsForbidden(err) {
			return nil, err
		}
	}
	err := api.GetProject(ctx, name)
	switch {
	case err == nil:
		return nil, nil
	case apierrors.IsNotFound(err):
		return nil, fmt.Errorf("project %q not found", name)
	default:
		return nil, fmt.Errorf("couldn't confirm access to project %q: %w", name, err)
	}
}
