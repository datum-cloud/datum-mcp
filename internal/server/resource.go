package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const resourceToolDescription = "Generic escape hatch for any Datum-managed resource kind that doesn't have a dedicated tool (e.g. Workload, " +
	"Network, IPClaim), and never a wider door than the dedicated tool is. Use the 'apis' tool first to discover a " +
	"kind's group, and whether it's namespaced, before calling this. Only datumapis.com/miloapis.com groups and the " +
	"Gateway API groups are reachable here; everything else (core Kubernetes machinery, RBAC, admission webhooks, etc.) " +
	"is refused. iam.miloapis.com, billing.miloapis.com, and services.miloapis.com are reachable read-only (list|get) " +
	"only, same as their dedicated tools, regardless of toolset configuration. Same shape as every other resource tool " +
	"otherwise: actions list|get|create|update|delete, fields " +
	"project/id/body/limit/continue/labelSelector/fieldSelector/dryRun/resourceVersion. Can itself be disabled via " +
	"DATUM_MCP_DISABLE_TOOLSETS=resource."

// sensitiveGroupActions caps the actions reachable through the generic
// 'resource' escape hatch for API groups whose dedicated tools are
// deliberately read-only (see Phase 2 PR6, #88: iam/services/billing). This
// applies unconditionally, independent of DATUM_MCP_DISABLE_TOOLSETS and of
// whether a dedicated tool for the group happens to be registered: 'resource'
// must never be a wider door into these groups than the dedicated tool is,
// or an agent could use it to bypass the read-only boundary entirely — e.g.
// creating an iam.miloapis.com PolicyBinding that grants itself a role.
var sensitiveGroupActions = map[string][]Action{
	"iam.miloapis.com":      {ActionList, ActionGet},
	"billing.miloapis.com":  {ActionList, ActionGet},
	"services.miloapis.com": {ActionList, ActionGet},
}

// dedicatedKindActions caps the actions the generic 'resource' tool offers for
// a (group, kind) that already has a dedicated tool. The same invariant
// sensitiveGroupActions states per group holds per kind: 'resource' stands in
// for a kind's dedicated tool, so it must never offer an action that tool
// withholds — deleting an operator-authored ipam.miloapis.com IPClass, whose
// dedicated tool is list|get, was reachable that way.
//
// The map is built from the dedicated registrations, so adding a tool can't
// leave a wider 'resource' door behind, and it is consulted whether or not
// that tool is registered: DATUM_MCP_DISABLE_TOOLSETS removes a dedicated
// tool, and must not turn 'resource' into a wider door in its place.
var dedicatedKindActions = buildDedicatedKindActions(crudResources)

// nonCrudDedicatedKinds are the dedicated tools that aren't crudResources
// (projects, organizations and users, all over resourcemanager.miloapis.com),
// so there is no registration to derive them from. Each entry is the set of
// generic actions those tools perform: projects lists, gets and creates a
// Project ('set' only changes the active project locally), and organizations
// and users only ever list or read a membership.
var nonCrudDedicatedKinds = map[string][]Action{
	kindKey("resourcemanager.miloapis.com", "Project"):                {ActionList, ActionGet, ActionCreate},
	kindKey("resourcemanager.miloapis.com", "OrganizationMembership"): {ActionList, ActionGet},
}

func buildDedicatedKindActions(resources []crudResource) map[string][]Action {
	m := make(map[string][]Action, len(resources)+len(nonCrudDedicatedKinds))
	for _, r := range resources {
		if len(r.Actions) == 0 {
			// Unrestricted: the dedicated tool already allows every action,
			// so there is nothing to cap.
			continue
		}
		key := kindKey(r.Group, r.Kind)
		m[key] = append(m[key], r.Actions...)
	}
	for key, actions := range nonCrudDedicatedKinds {
		m[key] = append(m[key], actions...)
	}
	return m
}

// kindKey indexes the action caps by group and kind, case-folded so that the
// case a caller passes can't step around a cap.
func kindKey(group, kind string) string {
	return strings.ToLower(strings.TrimSpace(group)) + "/" + strings.ToLower(strings.TrimSpace(kind))
}

// genericResourceActions is the action cap the generic 'resource' tool applies
// for a (group, kind): nil means unrestricted, all five actions. Callers must
// not modify the returned slice.
func genericResourceActions(group, kind string) []Action {
	if restricted, ok := sensitiveGroupActions[strings.ToLower(strings.TrimSpace(group))]; ok {
		return restricted
	}
	return dedicatedKindActions[kindKey(group, kind)]
}

// allowedGroupSuffixes and allowedExactGroups gate the generic 'resource'
// tool to API groups the platform actually models as product resources. The
// control plane also exposes plain Kubernetes machinery (core v1 Secrets/
// ConfigMaps, RBAC, admission webhooks, API registration, etc.) that a
// cloud-infra-management agent has no business reaching generically; an
// allowlist by suffix doesn't need to keep up with every such group the way
// a denylist would.
var (
	allowedGroupSuffixes = []string{".datumapis.com", ".miloapis.com"}
	allowedExactGroups   = map[string]bool{
		"gateway.networking.k8s.io": true,
		"gateway.envoyproxy.io":     true,
	}
)

func allowedGroup(group string) bool {
	if allowedExactGroups[group] {
		return true
	}
	for _, suffix := range allowedGroupSuffixes {
		if strings.HasSuffix(group, suffix) {
			return true
		}
	}
	return false
}

// GenericResourceInput is RoutedInput plus the group/kind/namespace that a
// dedicated crudResource would otherwise fix at registration time.
type GenericResourceInput struct {
	RoutedInput
	Group     string `json:"group" jsonschema:"API group, e.g. 'compute.datumapis.com'."`
	Kind      string `json:"kind" jsonschema:"Resource Kind, e.g. 'Workload'."`
	Namespace string `json:"namespace,omitempty" jsonschema:"Required if the kind is namespaced (the 'apis' tool tells you); omit for cluster-scoped kinds."`
}

func toolResource(ctx context.Context, _ *mcp.CallToolRequest, in GenericResourceInput) (*mcp.CallToolResult, any, error) {
	group := strings.TrimSpace(in.Group)
	kind := strings.TrimSpace(in.Kind)
	if group == "" || kind == "" {
		return errResult(fmt.Errorf("invalid params: group and kind are required"), suggestActions("apis", "list"))
	}
	if !allowedGroup(group) {
		return errResult(
			fmt.Errorf("group %q is not reachable via the generic 'resource' tool; it's restricted to Datum-managed API groups", group),
			suggestActions("apis", "list"),
		)
	}
	r := crudResource{Tool: "resource", Group: group, Kind: kind, Namespace: strings.TrimSpace(in.Namespace)}
	if capped := genericResourceActions(group, kind); len(capped) > 0 {
		r.Actions = capped
	}
	return r.handler(ctx, nil, in.RoutedInput)
}
