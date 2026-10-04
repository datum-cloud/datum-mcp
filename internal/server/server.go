package server

import (
	"context"
	"fmt"
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

type Action string

const (
	ActionList   Action = "list"
	ActionGet    Action = "get"
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

// RoutedInput is the shared input shape for every CRD-backed CRUD tool.
type RoutedInput struct {
	// Optional per-request project override; if empty, uses the active project.
	Project string `json:"project,omitempty" jsonschema:"Optional project id; overrides the active project set via the 'projects' tool."`
	// Action: one of list|get|create|update|delete (or list|get for read-only resources).
	Action Action `json:"action" jsonschema:"The operation to perform."`
	// ID required for get/update/delete
	ID string `json:"id,omitempty" jsonschema:"Resource name; required for get, update, and delete."`
	// Body is the request payload for create/update
	Body map[string]any `json:"body,omitempty" jsonschema:"Resource manifest, e.g. {\"metadata\":{...},\"spec\":{...}}; required for create, merged into spec for update."`
}

type APIInfoInput struct {
	Project string `json:"project,omitempty" jsonschema:"Optional project id; overrides the active project."`
	Group   string `json:"group,omitempty" jsonschema:"API group, e.g. 'networking.datumapis.com'; required for action=get."`
	Version string `json:"version,omitempty" jsonschema:"API version, e.g. 'v1alpha'; required for action=get."`
	Kind    string `json:"kind,omitempty" jsonschema:"Resource Kind, e.g. 'Domain'; required for action=get."`
	Action  string `json:"action,omitempty" jsonschema:"One of list|get."`
	Detail  string `json:"detail,omitempty" jsonschema:"For action=get: 'structure' returns a condensed shape (types/properties/required) instead of the full OpenAPI schema. Default: full."`
}

type ProjectsInput struct {
	Action string         `json:"action" jsonschema:"One of list|get|set|create."`
	Org    string         `json:"org,omitempty" jsonschema:"Optional organization id; defaults to DATUM_ORG env or the active organization."`
	Body   map[string]any `json:"body,omitempty" jsonschema:"For action=set: {\"name\": \"<project-id>\"}. For action=create: a Project manifest."`
}

type OrgMembershipsInput struct {
	Action string `json:"action" jsonschema:"One of list|get|set."`
	Name   string `json:"name,omitempty" jsonschema:"Organization id; required for action=set, verified against your memberships."`
}

type UsersInput struct {
	Action string `json:"action" jsonschema:"Currently only 'list' is supported."`
	Org    string `json:"org,omitempty" jsonschema:"Optional organization id; defaults to DATUM_ORG env or the active organization."`
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
		return "", fmt.Errorf("no active organization set; set DATUM_ORG env, pass 'org', or call organizations action set")
	}
	return o, nil
}

// crudResource describes a single CRD-backed resource exposed as an MCP tool
// supporting list|get|create|update|delete (or, when ReadOnly is true, just
// list|get). Every resource tool shares this implementation so that response
// cleaning, error handling, and descriptions only need to be correct once.
type crudResource struct {
	Tool      string
	Group     string
	Kind      string
	Namespace string // "" for cluster-scoped resources
	Note      string // optional extra sentence appended to the tool description
	ReadOnly  bool
}

func (r crudResource) actions() string {
	if r.ReadOnly {
		return "list|get"
	}
	return "list|get|create|update|delete"
}

func (r crudResource) suggestValidAction() *SuggestedAction {
	return suggestActions(r.Tool, r.actions())
}

func (r crudResource) description() string {
	d := fmt.Sprintf(
		"Manage %s resources (%s/%s). Requires an active project (set via the 'projects' tool, action=set), or pass 'project'. "+
			"Typical sequence: organizations set -> projects set -> %s list to discover existing resources, then get/create/update/delete. "+
			"Actions: %s. Fields: project (optional, overrides active project), id (required for get/update/delete), body (required for create/update). "+
			"If you get \"no active project set\", call 'projects' with action=list then action=set.",
		r.Kind, r.Group, r.Kind, r.Tool, r.actions(),
	)
	if r.Note != "" {
		d += " " + r.Note
	}
	return d
}

func (r crudResource) annotations() *mcp.ToolAnnotations {
	destructive := !r.ReadOnly
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    r.ReadOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  false,
	}
}

func (r crudResource) handler(ctx context.Context, _ *mcp.CallToolRequest, in RoutedInput) (*mcp.CallToolResult, any, error) {
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return errResult(err, nil)
	}
	p, err := resolveProjectName(in.Project)
	if err != nil {
		return errResult(err, suggestListProjects())
	}
	cli, err := api.NewProjectControlPlaneClient(ctx, p)
	if err != nil {
		return errResult(err, nil)
	}
	action := strings.ToLower(strings.TrimSpace(string(in.Action)))
	if r.ReadOnly && (action == string(ActionCreate) || action == string(ActionUpdate) || action == string(ActionDelete)) {
		return errResult(fmt.Errorf("%s is read-only; unsupported action: %s", r.Tool, in.Action), r.suggestValidAction())
	}
	switch action {
	case string(ActionList):
		list, err := api.FetchList(ctx, cli, r.Group, r.Kind, r.Namespace)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]any{"items": api.CleanList(list), "count": len(list.Items)})
	case string(ActionGet):
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		obj, err := api.FetchObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(api.CleanObject(obj))
	case string(ActionCreate):
		obj, err := api.CreateObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.Body)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(api.CleanObject(obj))
	case string(ActionUpdate):
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		obj, err := api.UpdateObjectSpec(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID, in.Body)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(api.CleanObject(obj))
	case string(ActionDelete):
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		if err := api.DeleteObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID); err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]string{"deleted": in.ID})
	default:
		return errResult(fmt.Errorf("unsupported %s action: %s", r.Tool, in.Action), r.suggestValidAction())
	}
}

// crudResources is the declarative list of every CRD-backed resource tool.
// Adding a new resource here is a one-line change; see Phase 2 of the MCP
// enhancement plan for the generic/dynamic escape hatch this list is a
// stopgap for.
var crudResources = []crudResource{
	{Tool: "domains", Group: "networking.datumapis.com", Kind: "Domain", Namespace: "default"},
	{Tool: "httpproxies", Group: "networking.datumapis.com", Kind: "HTTPProxy", Namespace: "default"},
	{Tool: "httproutes", Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "default",
		Note: "Targets Gateway API HTTPRoute resources."},
	{Tool: "gateways", Group: "gateway.networking.k8s.io", Kind: "Gateway", Namespace: "default",
		Note: "Targets Gateway API Gateway resources."},
	{Tool: "trafficprotectionpolicies", Group: "networking.datumapis.com", Kind: "TrafficProtectionPolicy", Namespace: "default",
		Note: "Policies target either a Gateway or HTTPRoute via spec.targetRefs."},
	{Tool: "dnszones", Group: "dns.networking.miloapis.com", Kind: "DNSZone", Namespace: "default"},
	{Tool: "dnsrecordsets", Group: "dns.networking.miloapis.com", Kind: "DNSRecordSet", Namespace: "default"},
	{Tool: "dnszoneclasses", Group: "dns.networking.miloapis.com", Kind: "DNSZoneClass", ReadOnly: true,
		Note: "Cluster-scoped; lists the DNSZoneClasses available when creating a DNSZone."},
}

// Organization memberships tool: list|get|set
func toolOrganizationMemberships(ctx context.Context, _ *mcp.CallToolRequest, in OrgMembershipsInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "list"
	}
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return errResult(err, nil)
	}
	userID := os.Getenv("DATUM_USER_ID")
	if userID == "" {
		var err error
		userID, err = authutil.GetSubject()
		if err != nil {
			return errResult(fmt.Errorf("failed to determine user ID: %w", err), nil)
		}
	}
	ucli, err := api.NewUserControlPlaneClient(ctx, userID)
	if err != nil {
		return errResult(err, nil)
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "")
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]any{"items": api.CleanList(list), "count": len(list.Items)})
	case "set":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return errResult(fmt.Errorf("invalid params: name is required"), nil)
		}
		memList, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "")
		if err != nil {
			return errResult(err, nil)
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
			return errResult(fmt.Errorf("you are not a member of organization %q", name), suggestListOrgs())
		}
		if err := org.SetActive(name); err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]string{"organization": name})
	case "get":
		if v := os.Getenv("DATUM_ORG"); v != "" {
			return okResult(map[string]string{"organization": v})
		}
		o, _ := org.GetActive()
		return okResult(map[string]string{"organization": o})
	default:
		return errResult(fmt.Errorf("unsupported organizations action: %s", in.Action), suggestActions("organizations", "list|get|set"))
	}
}

// Projects tool: list/set/get/create (list/create require an organization)
func toolProjects(ctx context.Context, _ *mcp.CallToolRequest, in ProjectsInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return errResult(err, nil)
	}
	orgName, err := resolveOrgName(in.Org)
	if err != nil {
		return errResult(err, suggestListOrgs())
	}
	cli, err := api.NewOrgControlPlaneClient(ctx, orgName)
	if err != nil {
		return errResult(err, nil)
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]any{"items": api.CleanList(list), "count": len(list.Items)})
	case "create":
		if in.Body == nil {
			return errResult(fmt.Errorf("invalid params: body is required for create"), nil)
		}
		obj, err := api.CreateObject(ctx, cli, "resourcemanager.miloapis.com", "Project", "", in.Body)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(api.CleanObject(obj))
	case "set":
		name, _ := in.Body["name"].(string)
		if name == "" {
			return errResult(fmt.Errorf("invalid params: body.name is required"), nil)
		}
		plist, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err != nil {
			return errResult(err, nil)
		}
		found := false
		for _, it := range plist.Items {
			if strings.EqualFold(it.GetName(), name) {
				found = true
				break
			}
		}
		if !found {
			return errResult(fmt.Errorf("project %q not found in org %q", name, orgName), suggestListProjects())
		}
		if err := project.SetActive(name); err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]string{"project": name})
	case "get":
		p, _ := project.GetActive()
		return okResult(map[string]string{"project": p})
	default:
		return errResult(fmt.Errorf("unsupported projects action: %s", in.Action), suggestActions("projects", "list|get|set|create"))
	}
}

// Users tool: list users in an organization (requires active org or 'org')
func toolUsers(ctx context.Context, _ *mcp.CallToolRequest, in UsersInput) (*mcp.CallToolResult, any, error) {
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "list"
	}
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return errResult(err, nil)
	}
	orgName, err := resolveOrgName(in.Org)
	if err != nil {
		return errResult(err, suggestListOrgs())
	}
	cli, err := api.NewOrgControlPlaneClient(ctx, orgName)
	if err != nil {
		return errResult(err, nil)
	}
	switch a {
	case "list":
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "OrganizationMembership", "organization-"+orgName)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(map[string]any{"items": api.CleanList(list), "count": len(list.Items)})
	default:
		return errResult(fmt.Errorf("unsupported users action: %s", in.Action), suggestActions("users", "list"))
	}
}

// APIs tool: list/get CRDs under the project control-plane.
func toolAPIs(ctx context.Context, _ *mcp.CallToolRequest, in APIInfoInput) (*mcp.CallToolResult, any, error) {
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return errResult(err, nil)
	}
	p, err := resolveProjectName(in.Project)
	if err != nil {
		return errResult(err, suggestListProjects())
	}
	a := strings.ToLower(strings.TrimSpace(in.Action))
	if a == "" {
		a = "get"
	}
	switch a {
	case "list":
		var out any
		if err := api.ListResourceDefinitions(ctx, p, &out); err != nil {
			return errResult(err, nil)
		}
		return okResult(out)
	case "get":
		g := strings.TrimSpace(in.Group)
		v := strings.TrimSpace(in.Version)
		if g == "" || v == "" {
			return errResult(fmt.Errorf("invalid params: group and version are required for get"), nil)
		}
		structureOnly := strings.EqualFold(strings.TrimSpace(in.Detail), "structure")
		var out any
		if err := api.GetResourceDefinition(ctx, p, g, v, strings.TrimSpace(in.Kind), structureOnly, &out); err != nil {
			return errResult(err, nil)
		}
		return okResult(out)
	default:
		return errResult(fmt.Errorf("unsupported apis action: %s", in.Action), suggestActions("apis", "list|get"))
	}
}

// NewMCPServer constructs the MCP server with all registered tools.
func NewMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "datum-mcp", Version: "0.2.0"}, nil)

	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "organizations",
		Description: "Discover and switch the active Datum Cloud organization. Most other tools require an active organization " +
			"(directly, via 'org', or via DATUM_ORG env). Actions: list (your organization memberships) | get (currently active org) | " +
			"set (name: '<org-id>', verified against your memberships). Typical sequence: list -> pick an org -> set. " +
			"If a tool fails with \"no active organization set\", call this tool with action=list, choose one, then action=set.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true},
	}, toolOrganizationMemberships)

	mcp.AddTool(s, &mcp.Tool{
		Name: "projects",
		Description: "Discover, switch, and create Datum Cloud projects within an organization. Most resource tools (domains, dnszones, etc.) " +
			"require an active project. Actions: list (org required, via 'org' field or active org) | get (currently active project) | " +
			"set (body.name: '<project-id>', verified to exist in the org) | create (body: Project manifest). " +
			"Typical sequence: organizations set -> projects list -> projects set. " +
			"If a tool fails with \"no active project set\", call this tool with action=list then action=set.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: false},
	}, toolProjects)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "users",
		Description: "List organization memberships (users) for an organization. Requires an active organization, or pass 'org'. Actions: list.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, toolUsers)

	mcp.AddTool(s, &mcp.Tool{
		Name: "apis",
		Description: "Discover CRD groups/versions/resources, or fetch the OpenAPI schema for a specific Kind, under the current project " +
			"control-plane. Useful to inspect the fields of resource kinds that don't have a dedicated tool. Actions: list (group/version/resource " +
			"names) | get (requires group, version, kind; optional detail='structure' for a condensed shape instead of the full schema). " +
			"Fields: project (optional).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, toolAPIs)

	for _, r := range crudResources {
		mcp.AddTool(s, &mcp.Tool{
			Name:        r.Tool,
			Description: r.description(),
			Annotations: r.annotations(),
		}, r.handler)
	}

	return s
}

// Run starts the server over stdio (default transport).
func Run(ctx context.Context) error {
	s := NewMCPServer()
	return s.Run(ctx, &mcp.StdioTransport{})
}

// RunHTTP starts the server using the streamable HTTP transport at addr (e.g., "localhost:9000").
func RunHTTP(ctx context.Context, addr string) error {
	s := NewMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return s }, nil)
	return http.ListenAndServe(addr, handler)
}
