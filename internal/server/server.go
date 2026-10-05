package server

import (
	"context"
	"errors"
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

// ListParams is embedded in every tool input that supports a 'list' action.
// All fields are ignored for other actions.
type ListParams struct {
	Limit         int64  `json:"limit,omitempty" jsonschema:"Max items to return (list only); clamped to 500, default 100."`
	Continue      string `json:"continue,omitempty" jsonschema:"Continuation token from a previous list response's 'continue' field, to fetch the next page (list only)."`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"kubectl-style label selector, e.g. 'team=edge,env!=prod' (list only)."`
	FieldSelector string `json:"fieldSelector,omitempty" jsonschema:"kubectl-style field selector, e.g. 'metadata.name=foo'; only fields the resource registers as selectable are supported (list only)."`
}

func (p ListParams) toAPIOptions() api.ListOptions {
	return api.ListOptions{
		Limit:         p.Limit,
		Continue:      p.Continue,
		LabelSelector: p.LabelSelector,
		FieldSelector: p.FieldSelector,
	}
}

// listEnvelope is the standard shape every 'list' action returns.
func listEnvelope(list *unstructured.UnstructuredList) map[string]any {
	return map[string]any{
		"items":    api.CleanList(list),
		"count":    len(list.Items),
		"continue": list.GetContinue(),
	}
}

// RoutedInput is the shared input shape for every CRD-backed CRUD tool.
type RoutedInput struct {
	// Optional per-request project override; if empty, uses the active project.
	Project string `json:"project,omitempty" jsonschema:"Optional project id; overrides the active project set via the 'projects' tool."`
	// Action: one of list|get|create|update|delete (or list|get for read-only resources).
	Action Action `json:"action" jsonschema:"The operation to perform."`
	// ID required for get/update/delete
	ID string `json:"id,omitempty" jsonschema:"Resource name; required for get, update, and delete."`
	// Body is the request payload for create/update
	Body map[string]any `json:"body,omitempty" jsonschema:"Resource manifest, e.g. {\"metadata\":{...},\"spec\":{...}}; required for create. For update, body.spec is deep-merged into the existing spec field-by-field: a null field deletes it, nested objects merge recursively, and arrays/scalars replace wholesale — omitted fields are left untouched."`
	ListParams
	// DryRun validates create/update/delete server-side without persisting the change.
	DryRun bool `json:"dryRun,omitempty" jsonschema:"If true (create/update/delete only), validate and run admission without persisting the change; the response shows what would happen."`
	// ResourceVersion, for update/delete only: enables optimistic concurrency.
	ResourceVersion string `json:"resourceVersion,omitempty" jsonschema:"For update/delete only: the resourceVersion from a prior 'get'. If the resource has changed since, the call fails instead of overwriting it."`
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
	ListParams
	// DryRun applies to action=create only.
	DryRun bool `json:"dryRun,omitempty" jsonschema:"For action=create only: validate without persisting."`
}

type OrgMembershipsInput struct {
	Action string `json:"action" jsonschema:"One of list|get|set."`
	Name   string `json:"name,omitempty" jsonschema:"Organization id; required for action=set, verified against your memberships."`
	ListParams
}

type UsersInput struct {
	Action string `json:"action" jsonschema:"Currently only 'list' is supported."`
	Org    string `json:"org,omitempty" jsonschema:"Optional organization id; defaults to DATUM_ORG env or the active organization."`
	ListParams
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
	if v := activeOrgOrEmpty(); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no active organization set; set DATUM_ORG env, pass 'org', or call organizations action set")
}

// activeOrgOrEmpty mirrors resolveOrgName's precedence (DATUM_ORG env, then
// the persisted active org) but never errors, returning "" when unset. Used
// by the 'context' tool, which reports state rather than requiring it.
func activeOrgOrEmpty() string {
	if v := os.Getenv("DATUM_ORG"); v != "" {
		return v
	}
	o, _ := org.GetActive()
	return o
}

// fullActions is the complete action set a crudResource supports when
// Actions is left unset.
var fullActions = []Action{ActionList, ActionGet, ActionCreate, ActionUpdate, ActionDelete}

// crudResource describes a single CRD-backed resource exposed as an MCP tool.
// Every resource tool shares this implementation so that response cleaning,
// error handling, and descriptions only need to be correct once.
type crudResource struct {
	Tool      string
	Group     string
	Kind      string
	Namespace string // "" for cluster-scoped resources
	Note      string // optional extra sentence appended to the tool description
	// Actions restricts which actions this tool supports. Leaving it nil
	// means the full list|get|create|update|delete set; some resources are
	// system-managed and only support a subset (e.g. list|get|delete for a
	// kind a user can inspect and release but never directly create).
	Actions []Action
	// Toolset gates registration behind DATUM_MCP_DISABLE_TOOLSETS. "" means
	// always registered (the original, pre-Phase-2 tool set).
	Toolset string
}

func (r crudResource) allowedActions() []Action {
	if len(r.Actions) == 0 {
		return fullActions
	}
	return r.Actions
}

func (r crudResource) allows(a Action) bool {
	for _, allowed := range r.allowedActions() {
		if allowed == a {
			return true
		}
	}
	return false
}

func (r crudResource) readOnly() bool {
	return !r.allows(ActionCreate) && !r.allows(ActionUpdate) && !r.allows(ActionDelete)
}

func (r crudResource) actions() string {
	allowed := r.allowedActions()
	parts := make([]string, len(allowed))
	for i, a := range allowed {
		parts[i] = string(a)
	}
	return strings.Join(parts, "|")
}

func (r crudResource) suggestValidAction() *SuggestedAction {
	return suggestActions(r.Tool, r.actions())
}

func (r crudResource) description() string {
	d := fmt.Sprintf(
		"Manage %s resources (%s/%s). Requires an active project (set via the 'projects' tool, action=set), or pass 'project'. "+
			"Typical sequence: organizations set -> projects set -> %s list to discover existing resources, then get/create/update/delete. "+
			"Actions: %s. Fields: project (optional, overrides active project), id (required for get/update/delete), body (required for create/update), "+
			"limit/continue/labelSelector/fieldSelector (list only, paginated: default 100, max 500 per page), "+
			"dryRun (create/update/delete: validate without persisting), resourceVersion (update/delete: optimistic concurrency, from a prior get; "+
			"a stale value fails with a conflict error instead of overwriting). "+
			"If you get \"no active project set\", call 'projects' with action=list then action=set.",
		r.Kind, r.Group, r.Kind, r.Tool, r.actions(),
	)
	if r.Note != "" {
		d += " " + r.Note
	}
	return d
}

func (r crudResource) annotations() *mcp.ToolAnnotations {
	ro := r.readOnly()
	destructive := !ro
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    ro,
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
	action := Action(strings.ToLower(strings.TrimSpace(string(in.Action))))
	// A single check covers both "this resource doesn't support that action"
	// and "that's not a recognized action at all": allowedActions() only
	// ever contains the five known Action values, so an unrecognized string
	// like "bogus" fails the same way a disallowed-but-valid action does.
	if !r.allows(action) {
		return errResult(fmt.Errorf("unsupported %s action: %s", r.Tool, in.Action), r.suggestValidAction())
	}
	writeOpts := api.WriteOptions{DryRun: in.DryRun, ExpectedResourceVersion: in.ResourceVersion}
	switch action {
	case ActionList:
		list, err := api.FetchList(ctx, cli, r.Group, r.Kind, r.Namespace, in.ListParams.toAPIOptions())
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(listEnvelope(list))
	case ActionGet:
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		obj, err := api.FetchObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(api.CleanObject(obj))
	case ActionCreate:
		obj, err := api.CreateObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.Body, writeOpts)
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(withDryRunNote(api.CleanObject(obj), in.DryRun))
	case ActionUpdate:
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		obj, err := api.UpdateObjectSpec(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID, in.Body, writeOpts)
		if err != nil {
			var conflict *api.ConflictError
			if errors.As(err, &conflict) {
				return errResult(err, suggestGet(r.Tool, in.ID))
			}
			return errResult(err, nil)
		}
		return okResult(withDryRunNote(api.CleanObject(obj), in.DryRun))
	case ActionDelete:
		if in.ID == "" {
			return errResult(fmt.Errorf("invalid params: id is required"), nil)
		}
		if err := api.DeleteObject(ctx, cli, r.Group, r.Kind, r.Namespace, in.ID, writeOpts); err != nil {
			var conflict *api.ConflictError
			if errors.As(err, &conflict) {
				return errResult(err, suggestGet(r.Tool, in.ID))
			}
			return errResult(err, nil)
		}
		return okResult(withDryRunNote(map[string]any{"deleted": in.ID}, in.DryRun))
	default:
		// Unreachable: the allows() check above already rejected anything
		// that isn't one of these five. Kept as a defensive fallback.
		return errResult(fmt.Errorf("unsupported %s action: %s", r.Tool, in.Action), r.suggestValidAction())
	}
}

// withDryRunNote tags a successful write response as hypothetical when the
// caller asked for DryRun, so the agent doesn't mistake it for a persisted
// change.
func withDryRunNote(result map[string]any, dryRun bool) map[string]any {
	if dryRun {
		result["dryRun"] = true
	}
	return result
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
	{Tool: "dnszoneclasses", Group: "dns.networking.miloapis.com", Kind: "DNSZoneClass", Actions: []Action{ActionList, ActionGet},
		Note: "Cluster-scoped; lists the DNSZoneClasses available when creating a DNSZone."},

	// Phase 2 additions are tagged with a Toolset so they can be disabled via
	// DATUM_MCP_DISABLE_TOOLSETS (comma-separated, e.g. "billing,iam") without
	// affecting the original (untagged / Toolset "") tools above, which are
	// always registered.
}

// disabledToolsets parses DATUM_MCP_DISABLE_TOOLSETS into a lookup set.
// "core" can never be disabled this way - leave the set of always-on tools
// out of it entirely rather than special-casing it everywhere else.
func disabledToolsets() map[string]bool {
	set := map[string]bool{}
	for _, t := range strings.Split(os.Getenv("DATUM_MCP_DISABLE_TOOLSETS"), ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && t != "core" {
			set[t] = true
		}
	}
	return set
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
		list, err := api.FetchList(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "", in.ListParams.toAPIOptions())
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(listEnvelope(list))
	case "set":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return errResult(fmt.Errorf("invalid params: name is required"), nil)
		}
		// Membership verification always checks the full set, independent of
		// any list-only pagination the caller passed in, and must not miss a
		// membership just because the caller has more than one page of them.
		memItems, err := api.FetchAllItems(ctx, ucli, "resourcemanager.miloapis.com", "OrganizationMembership", "")
		if err != nil {
			return errResult(err, nil)
		}
		allowed := false
		for _, it := range memItems {
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
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "Project", "", in.ListParams.toAPIOptions())
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(listEnvelope(list))
	case "create":
		if in.Body == nil {
			return errResult(fmt.Errorf("invalid params: body is required for create"), nil)
		}
		obj, err := api.CreateObject(ctx, cli, "resourcemanager.miloapis.com", "Project", "", in.Body, api.WriteOptions{DryRun: in.DryRun})
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(withDryRunNote(api.CleanObject(obj), in.DryRun))
	case "set":
		name, _ := in.Body["name"].(string)
		if name == "" {
			return errResult(fmt.Errorf("invalid params: body.name is required"), nil)
		}
		// Membership verification always checks the full set, independent of
		// any list-only pagination the caller passed in, and must not miss a
		// project just because the org has more than one page of them.
		pitems, err := api.FetchAllItems(ctx, cli, "resourcemanager.miloapis.com", "Project", "")
		if err != nil {
			return errResult(err, nil)
		}
		found := false
		for _, it := range pitems {
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
		list, err := api.FetchList(ctx, cli, "resourcemanager.miloapis.com", "OrganizationMembership", "organization-"+orgName, in.ListParams.toAPIOptions())
		if err != nil {
			return errResult(err, nil)
		}
		return okResult(listEnvelope(list))
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
	s := mcp.NewServer(&mcp.Implementation{Name: "datum-mcp", Version: "0.4.0"}, nil)

	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "organizations",
		Description: "Discover and switch the active Datum Cloud organization. Most other tools require an active organization " +
			"(directly, via 'org', or via DATUM_ORG env). Actions: list (your organization memberships, paginated: default 100/page, " +
			"max 500; limit/continue/labelSelector/fieldSelector) | get (currently active org) | set (name: '<org-id>', verified against " +
			"your memberships). Typical sequence: list -> pick an org -> set. " +
			"If a tool fails with \"no active organization set\", call this tool with action=list, choose one, then action=set.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true},
	}, toolOrganizationMemberships)

	mcp.AddTool(s, &mcp.Tool{
		Name: "projects",
		Description: "Discover, switch, and create Datum Cloud projects within an organization. Most resource tools (domains, dnszones, etc.) " +
			"require an active project. Actions: list (org required, via 'org' field or active org; paginated: default 100/page, max 500; " +
			"limit/continue/labelSelector/fieldSelector) | get (currently active project) | set (body.name: '<project-id>', verified to exist " +
			"in the org) | create (body: Project manifest; dryRun to validate without persisting). " +
			"Typical sequence: organizations set -> projects list -> projects set. " +
			"If a tool fails with \"no active project set\", call this tool with action=list then action=set.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: false},
	}, toolProjects)

	mcp.AddTool(s, &mcp.Tool{
		Name: "users",
		Description: "List organization memberships (users) for an organization. Requires an active organization, or pass 'org'. " +
			"Actions: list (paginated: default 100/page, max 500; limit/continue/labelSelector/fieldSelector).",
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

	disabled := disabledToolsets()
	for _, r := range crudResources {
		if r.Toolset != "" && disabled[r.Toolset] {
			continue
		}
		mcp.AddTool(s, &mcp.Tool{
			Name:        r.Tool,
			Description: r.description(),
			Annotations: r.annotations(),
		}, r.handler)
	}

	resourceDestructive := true
	mcp.AddTool(s, &mcp.Tool{
		Name:        "resource",
		Description: resourceToolDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &resourceDestructive, IdempotentHint: false},
	}, toolResource)

	contextDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name:        "context",
		Description: contextToolDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &contextDestructive, IdempotentHint: true},
	}, toolContext)

	registerPrompts(s)

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
