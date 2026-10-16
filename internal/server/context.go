package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/datum-cloud/datum-mcp/internal/api"
	"github.com/datum-cloud/datum-mcp/internal/auth"
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
	Authenticated          bool             `json:"authenticated"`
	ActiveOrganization     string           `json:"active_organization,omitempty"`
	ActiveProject          string           `json:"active_project,omitempty"`
	ProjectSource          string           `json:"project_source,omitempty"`
	Organizations          []OrgSummary     `json:"organizations,omitempty"`
	OrganizationsTruncated bool             `json:"organizations_truncated,omitempty"`
	Projects               []ProjectSummary `json:"projects,omitempty"`
	ProjectsTruncated      bool             `json:"projects_truncated,omitempty"`
	// Warnings carries lookups that failed without stopping the rest of the
	// snapshot, e.g. an identity that can't list organizations.
	Warnings []string `json:"warnings,omitempty"`
	NextStep string   `json:"next_step,omitempty"`
}

func toolContext(ctx context.Context, _ *mcp.CallToolRequest, _ ContextInput) (*mcp.CallToolResult, any, error) {
	if _, ok := auth.CheckAuth(ctx); !ok {
		return okResult(ContextResult{
			Authenticated: false,
			NextStep:      "Call any tool (e.g. organizations action=list) to start the login flow.",
		})
	}

	res := ContextResult{Authenticated: true}
	res.ActiveProject, res.ProjectSource = activeProjectOrEmpty(ctx)
	if res.ProjectSource == projectSourceServiceAccount {
		// A service account is confined to its own project and can't list
		// organizations or projects, so there's nothing more to discover.
		return okResult(res)
	}
	res.ActiveOrganization = activeOrgOrEmpty()

	orgsListed := false
	if err := listContextOrgs(ctx, &res); err != nil {
		res.Warnings = append(res.Warnings, "couldn't list organizations: "+err.Error())
	} else {
		orgsListed = true
	}

	projectsListed := false
	if res.ActiveOrganization != "" {
		if err := listContextProjects(ctx, &res); err != nil {
			res.Warnings = append(res.Warnings, "couldn't list projects: "+err.Error())
		} else {
			projectsListed = true
		}
	}

	switch {
	case res.ActiveProject != "":
		// Nothing left to set up.
	case res.ActiveOrganization == "" && orgsListed:
		res.NextStep = "Call 'organizations' with action=set and one of the names from 'organizations' above."
		if res.OrganizationsTruncated {
			res.NextStep += " 'organizations' above is capped at 500; if yours isn't listed, call 'organizations' with action=list and follow 'continue' to see the rest."
		}
	case res.ActiveOrganization != "" && projectsListed:
		res.NextStep = "Call 'projects' with action=set and body.name set to one of the names from 'projects' above."
		if res.ProjectsTruncated {
			res.NextStep += " 'projects' above is capped at 500; if yours isn't listed, call 'projects' with action=list and follow 'continue' to see the rest."
		}
	default:
		res.NextStep = "Organizations or projects couldn't be listed (see 'warnings'); " + explicitProjectHint + "."
	}
	return okResult(res)
}

func listContextOrgs(ctx context.Context, res *ContextResult) error {
	userID, err := currentUserID()
	if err != nil {
		return err
	}
	ucli, err := api.NewUserControlPlaneClient(ctx, userID)
	if err != nil {
		return err
	}
	memList, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "", api.ListOptions{Limit: api.MaxListLimit})
	if err != nil {
		return err
	}
	res.OrganizationsTruncated = memList.GetContinue() != ""
	for _, it := range memList.Items {
		name, _, _ := unstructured.NestedString(it.Object, "spec", "organizationRef", "name")
		if name == "" {
			continue
		}
		displayName, _, _ := unstructured.NestedString(it.Object, "status", "organization", "displayName")
		res.Organizations = append(res.Organizations, OrgSummary{Name: name, DisplayName: displayName})
	}
	return nil
}

func listContextProjects(ctx context.Context, res *ContextResult) error {
	orgCli, err := api.NewOrgControlPlaneClient(ctx, res.ActiveOrganization)
	if err != nil {
		return err
	}
	projList, err := api.FetchList(ctx, orgCli, "resourcemanager.miloapis.com", "Project", "", api.ListOptions{Limit: api.MaxListLimit})
	if err != nil {
		return err
	}
	res.ProjectsTruncated = projList.GetContinue() != ""
	for _, it := range projList.Items {
		res.Projects = append(res.Projects, ProjectSummary{Name: it.GetName()})
	}
	return nil
}
