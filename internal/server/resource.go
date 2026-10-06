package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const resourceToolDescription = "Generic escape hatch for any Datum-managed resource kind that doesn't have a dedicated tool " +
	"(e.g. Workload, Network, IPClaim). Use the 'apis' tool (action=list) first to discover a kind's group, and whether it's " +
	"namespaced, before calling this. Only datumapis.com/miloapis.com groups and the Gateway API groups are reachable here; " +
	"everything else (core Kubernetes machinery, RBAC, admission webhooks, etc., which the control plane also exposes) is " +
	"refused. iam.miloapis.com, billing.miloapis.com, and services.miloapis.com are reachable read-only (list|get) only, " +
	"same as their dedicated tools, regardless of toolset configuration. Same shape as every other resource tool otherwise: " +
	"actions list|get|create|update|delete, fields project/id/body/limit/continue/labelSelector/fieldSelector/dryRun/" +
	"resourceVersion. Can itself be disabled via DATUM_MCP_DISABLE_TOOLSETS=resource."

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
	if restricted, ok := sensitiveGroupActions[group]; ok {
		r.Actions = restricted
	}
	return r.handler(ctx, nil, in.RoutedInput)
}
