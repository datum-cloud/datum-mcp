package server

import (
	"context"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/datum-cloud/datum-mcp/internal/api"
	"github.com/datum-cloud/datum-mcp/internal/auth"
	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/project"
)

const contextToolDescription = "Discover what's already set up in this session: whether you're authenticated, the active organization/" +
	"project (if any), and which organizations/projects are reachable. A stateless snapshot, not session memory — call it any time " +
	"instead of guessing. 'next_step' tells you exactly what to call next; it's null once an active project is set. " +
	"Doesn't trigger the login flow; use any other tool to do that if authenticated is false."

// ContextInput takes no fields; it's a pure discovery call.
type ContextInput struct{}

type OrgSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

type ProjectSummary struct {
	Name string `json:"name"`
}

type ContextResult struct {
	Authenticated      bool             `json:"authenticated"`
	ActiveOrganization string           `json:"active_organization,omitempty"`
	ActiveProject      string           `json:"active_project,omitempty"`
	Organizations      []OrgSummary     `json:"organizations,omitempty"`
	Projects           []ProjectSummary `json:"projects,omitempty"`
	NextStep           string           `json:"next_step,omitempty"`
}

func toolContext(ctx context.Context, _ *mcp.CallToolRequest, _ ContextInput) (*mcp.CallToolResult, any, error) {
	if _, ok := auth.CheckAuth(ctx); !ok {
		return okResult(ContextResult{
			Authenticated: false,
			NextStep:      "Call any tool (e.g. organizations action=list) to start the login flow.",
		})
	}

	res := ContextResult{Authenticated: true}

	userID := os.Getenv("DATUM_USER_ID")
	if userID == "" {
		var err error
		userID, err = authutil.GetSubject()
		if err != nil {
			return errResult(err, nil)
		}
	}
	ucli, err := api.NewUserControlPlaneClient(ctx, userID)
	if err != nil {
		return errResult(err, nil)
	}
	memList, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "", api.ListOptions{Limit: api.MaxListLimit})
	if err != nil {
		return errResult(err, nil)
	}
	for _, it := range memList.Items {
		name, _, _ := unstructured.NestedString(it.Object, "spec", "organizationRef", "name")
		if name == "" {
			continue
		}
		displayName, _, _ := unstructured.NestedString(it.Object, "status", "organization", "displayName")
		res.Organizations = append(res.Organizations, OrgSummary{Name: name, DisplayName: displayName})
	}

	res.ActiveOrganization = activeOrgOrEmpty()
	if res.ActiveOrganization == "" {
		res.NextStep = "Call 'organizations' with action=set and one of the names from 'organizations' above."
		return okResult(res)
	}

	res.ActiveProject, _ = project.GetActive()

	orgCli, err := api.NewOrgControlPlaneClient(ctx, res.ActiveOrganization)
	if err != nil {
		return errResult(err, nil)
	}
	projList, err := api.FetchList(ctx, orgCli, "resourcemanager.miloapis.com", "Project", "", api.ListOptions{Limit: api.MaxListLimit})
	if err != nil {
		return errResult(err, nil)
	}
	for _, it := range projList.Items {
		res.Projects = append(res.Projects, ProjectSummary{Name: it.GetName()})
	}

	if res.ActiveProject == "" {
		res.NextStep = "Call 'projects' with action=set and body.name set to one of the names from 'projects' above."
	}
	return okResult(res)
}
