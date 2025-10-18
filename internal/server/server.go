package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/datum-cloud/datum-mcp/internal/api"
	"github.com/datum-cloud/datum-mcp/internal/auth"
	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/org"
	"github.com/datum-cloud/datum-mcp/internal/project"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ProjectSetInput struct {
	Name string `json:"name" jsonschema:"project name to set active"`
}

type Action string

const (
	ActionList   Action = "list"
	ActionGet    Action = "get"
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

type RoutedInput struct {
	// Optional per-request project override; if empty, uses active project.
	Project string `json:"project,omitempty"`
	// Action: one of list|get|create|update|delete
	Action Action `json:"action"`
	// ID required for get/update/delete
	ID string `json:"id,omitempty"`
	// Body is the request payload for create/update
	Body map[string]any `json:"body,omitempty"`
}

type APIInfoInput struct {
	Project string `json:"project,omitempty"`
	Name    string `json:"name,omitempty"`
	Group   string `json:"group,omitempty"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Action  string `json:"action,omitempty"`
}

type OrgInput struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
}

type ProjectsInput struct {
	Action string         `json:"action"`
	Org    string         `json:"org,omitempty"`
	Body   map[string]any `json:"body,omitempty"`
}

type OrgMembershipsInput struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
}

type UsersInput struct {
	Action string `json:"action"`
	Org    string `json:"org,omitempty"`
}

func resolveProjectName(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	p, _ := project.GetActive()
	if p == "" {
		return "", fmt.Errorf("no active project set; pass 'project' in request or call projects action set")
	}
	return p, nil
}

func resolveOrgName(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("DATUM_ORG"); v != "" {
		return v, nil
	}
	o, _ := org.GetActive()
	if o == "" {
		return "", fmt.Errorf("no active organization set; set DATUM_ORG env, pass 'org', or call organizationmemberships tool to choose")
	}
	return o, nil
}

// Organization memberships tool: list
// - {"action":"list"}
func toolOrganizationMemberships(ctx context.Context, _ *mcp.CallToolRequest, in OrgMembershipsInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "list"
	}
	// Ensure auth once and initialize user control-plane client once
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	userID := os.Getenv("DATUM_USER_ID")
	if userID == "" {
		var err error
		userID, err = authutil.GetSubject()
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("failed to determine user ID: %w", err)
		}
	}
	ucli, err := api.NewUserControlPlaneClient(ctx, userID)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, list, nil
	case "set":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: name is required")
		}
		memList, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		allowed := false
		for _, it := range memList.Items {
			orgName, _, _ := unstructured.NestedString(it.Object, "spec", "organizationRef", "name")
			if strings.EqualFold(orgName, name) {
				allowed = true
				break
			}
		}
		if !allowed {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("you are not a member of organization %q", name)
		}
		if err := org.SetActive(name); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "active organization set"}}}, map[string]string{"organization": name}, nil
	case "get":
		if v := os.Getenv("DATUM_ORG"); v != "" {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: v}}}, map[string]string{"organization": v}, nil
		}
		o, _ := org.GetActive()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: o}}}, map[string]string{"organization": o}, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported organizationmemberships action: %s", in.Action)
	}
}

// Projects tool: list/set/get (list requires organization)
// - {"action":"list","org":"org-1"}
// - {"action":"set","body":{"name":"project-1"}}
// - {"action":"get"}
func toolProjects(ctx context.Context, _ *mcp.CallToolRequest, in ProjectsInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	orgName, err := resolveOrgName(in.Org)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	cli, err := api.NewOrgControlPlaneClient(ctx, orgName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, list, nil
	case "create":
		if in.Body == nil {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: body is required for create")
		}
		obj, err := api.CreateObject(ctx, cli, "resourcemanager.miloapis.com", "Project", "", in.Body)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case "set":
		name, _ := in.Body["name"].(string)
		if name == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: body.name is required")
		}
		plist, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		found := false
		for _, it := range plist.Items {
			if strings.EqualFold(it.GetName(), name) {
				found = true
				break
			}
		}
		if !found {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("project %q not found in org %q", name, orgName)
		}
		if err := project.SetActive(name); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "active project set"}}}, map[string]string{"project": name}, nil
	case "get":
		p, _ := project.GetActive()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: p}}}, map[string]string{"project": p}, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported projects action: %s", in.Action)
	}
}

// Users tool: list users in an organization (requires active org or 'org')
// - {"action":"list","org":"org-1"}
func toolUsers(ctx context.Context, _ *mcp.CallToolRequest, in UsersInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "list"
	}
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	orgName, err := resolveOrgName(in.Org)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	cli, err := api.NewOrgControlPlaneClient(ctx, orgName)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "OrganizationMembership", "organization-"+orgName)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, list, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported users action: %s", in.Action)
	}
}

// Domains tool supports: list|get|create|update|delete
// Examples:
// - {"action":"list","project":"my-proj"}
// - {"action":"get","id":"domain-1"}
// - {"action":"create","body":{...}}
// - {"action":"update","id":"domain-1","body":{...}}
// - {"action":"delete","id":"domain-1"}
func toolDomains(ctx context.Context, _ *mcp.CallToolRequest, in RoutedInput) (*mcp.CallToolResult, any, error) {
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	p, err := resolveProjectName(in.Project)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	cli, err := api.NewProjectControlPlaneClient(ctx, p)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	switch strings.ToLower(string(in.Action)) {
	case string(ActionList):
		list, err := api.FetchList(ctx, cli, "networking.datumapis.com", "Domain", "default")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, list, nil
	case string(ActionGet):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		obj, err := api.FetchObject(ctx, cli, "networking.datumapis.com", "Domain", "default", in.ID)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionCreate):
		obj, err := api.CreateObject(ctx, cli, "networking.datumapis.com", "Domain", "default", in.Body)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionUpdate):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		obj, err := api.UpdateObjectSpec(ctx, cli, "networking.datumapis.com", "Domain", "default", in.ID, in.Body)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionDelete):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		if err := api.DeleteObject(ctx, cli, "networking.datumapis.com", "Domain", "default", in.ID); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deleted"}}}, map[string]string{"deleted": in.ID}, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported domains action: %s", in.Action)
	}
}

// HTTP Proxies tool supports: list|get|create|update|delete
// Examples:
// - {"action":"list","project":"my-proj"}
// - {"action":"get","id":"proxy-1"}
// - {"action":"create","body":{...}}
// - {"action":"update","id":"proxy-1","body":{...}}
// - {"action":"delete","id":"proxy-1"}
func toolHTTPProxies(ctx context.Context, _ *mcp.CallToolRequest, in RoutedInput) (*mcp.CallToolResult, any, error) {
	_, err := auth.EnsureAuth(ctx)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	p, err := resolveProjectName(in.Project)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	cli, err := api.NewProjectControlPlaneClient(ctx, p)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	switch strings.ToLower(string(in.Action)) {
	case string(ActionList):
		list, err := api.FetchList(ctx, cli, "networking.datumapis.com", "HTTPProxy", "default")
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(list, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, list, nil
	case string(ActionGet):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		obj, err := api.FetchObject(ctx, cli, "networking.datumapis.com", "HTTPProxy", "default", in.ID)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionCreate):
		obj, err := api.CreateObject(ctx, cli, "networking.datumapis.com", "HTTPProxy", "default", in.Body)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionUpdate):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		obj, err := api.UpdateObjectSpec(ctx, cli, "networking.datumapis.com", "HTTPProxy", "default", in.ID, in.Body)
		if err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(obj, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, obj, nil
	case string(ActionDelete):
		if in.ID == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: id is required")
		}
		if err := api.DeleteObject(ctx, cli, "networking.datumapis.com", "HTTPProxy", "default", in.ID); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deleted"}}}, map[string]string{"deleted": in.ID}, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported httpproxies action: %s", in.Action)
	}
}

// APIs tool: list/get CRDs under the project control-plane.
// - {"action":"list","project":"proj"}
// - {"action":"get","project":"proj","name":"domains"}
func toolAPIs(ctx context.Context, _ *mcp.CallToolRequest, in APIInfoInput) (*mcp.CallToolResult, any, error) {
	_, err := auth.EnsureAuth(ctx)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	p, err := resolveProjectName(in.Project)
	if err != nil {
		return &mcp.CallToolResult{IsError: true}, nil, err
	}
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "get"
	}
	switch a {
	case "list":
		var out any
		if err := api.ListResourceDefinitions(ctx, p, &out); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
	case "get":
		// Prefer explicit group+version; keep name for back-compat (optional)
		g := strings.TrimSpace(in.Group)
		v := strings.TrimSpace(in.Version)
		if g == "" || v == "" {
			return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("invalid params: group and version are required for get")
		}
		var out any
		if err := api.GetResourceDefinition(ctx, p, g, v, strings.TrimSpace(in.Kind), &out); err != nil {
			return &mcp.CallToolResult{IsError: true}, nil, err
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
	default:
		return &mcp.CallToolResult{IsError: true}, nil, fmt.Errorf("unsupported apis action: %s", in.Action)
	}
}

// NewMCPServer constructs the MCP server with all registered tools.
func NewMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "datum-mcp", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "organizations", Description: "Manage organization context. Actions: list|get|set (name)."}, toolOrganizationMemberships)
	mcp.AddTool(s, &mcp.Tool{Name: "projects", Description: "Manage projects. Actions: list|get|create|set. list/create require active org or 'org'; set uses body.name."}, toolProjects)
	mcp.AddTool(s, &mcp.Tool{Name: "users", Description: "List users under the active org or 'org'. Actions: list."}, toolUsers)
	mcp.AddTool(s, &mcp.Tool{Name: "domains", Description: "CRUD for domains. Actions: list|get|create|update|delete. Fields: project (optional), id (for get/update/delete), body (for create/update)."}, toolDomains)
	mcp.AddTool(s, &mcp.Tool{Name: "httpproxies", Description: "CRUD for HTTP proxies. Actions: list|get|create|update|delete. Fields: project (optional), id (for get/update/delete), body (for create/update)."}, toolHTTPProxies)
	mcp.AddTool(s, &mcp.Tool{Name: "apis", Description: "List/get CRDs under the current project. Actions: list|get. Fields: project (optional), name (for get)."}, toolAPIs)
	return s
}

// Run starts the server over stdio (default transport).
func Run(ctx context.Context) error {
	s := NewMCPServer()
	log.Printf("datum-mcp running (stdio)")
	return s.Run(ctx, &mcp.StdioTransport{})
}

// RunHTTP starts the server using the streamable HTTP transport at addr (e.g., "localhost:9000").
func RunHTTP(ctx context.Context, addr string) error {
	s := NewMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return s }, nil)
	log.Printf("datum-mcp listening (http) on %s", addr)
	return http.ListenAndServe(addr, handler)
}
