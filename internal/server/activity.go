package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// activityQueryKinds maps the 'activity' tool's queryType to the backing
// Kind. All three are cluster-scoped, create-a-query-get-synchronous-results
// objects (confirmed via `datumctl explain <kind> --recursive` against a
// live org) - not persisted/listable things in the usual sense, even though
// list/get/delete are still available for inspecting or cleaning up past
// query objects.
var activityQueryKinds = map[string]string{
	"audit":  "AuditLogQuery",
	"events": "EventQuery",
	"feed":   "ActivityQuery",
}

const activityToolDescription = "Query audit logs, Kubernetes events, or the combined human-readable activity feed. " +
	"The primary action is 'create': submit a query and its results come back in the same response's status.results " +
	"- nothing is persisted the way domains/dnszones/etc. are. Requires an active project (or pass 'project'). " +
	"queryType selects the backing query, each with its own spec fields (pass them in body.spec): " +
	"'audit' (AuditLogQuery) - body.spec: startTime*, endTime* (relative like \"now-7d\" or RFC3339), filter (CEL expression), limit, continue. " +
	"'events' (EventQuery, up to 60 days vs. the native 24h Events list) - body.spec: startTime*, endTime*, namespace, fieldSelector (standard Kubernetes field-selector syntax, e.g. \"type=Warning\"), limit, continue. " +
	"'feed' (ActivityQuery; also covers datumctl's 'history' - add a spec.resource.* filter below to scope to one resource) - " +
	"body.spec: startTime*, endTime*, filter (CEL; fields: spec.changeSource, spec.actor.name/type/uid, spec.resource.apiGroup/kind/name/namespace/uid, spec.summary, spec.origin.type), search, limit, continue. " +
	"Example: {\"queryType\": \"audit\", \"action\": \"create\", \"body\": {\"metadata\": {\"name\": \"recent-deletions\"}, \"spec\": {\"startTime\": \"now-7d\", \"endTime\": \"now\", \"filter\": \"verb == 'delete'\", \"limit\": 100}}}."

// ActivityInput is RoutedInput plus the queryType that selects which of the
// three activity.miloapis.com query Kinds to use.
type ActivityInput struct {
	RoutedInput
	QueryType string `json:"queryType" jsonschema:"One of audit|events|feed. See the tool description for each one's distinct body.spec fields."`
}

func toolActivity(ctx context.Context, _ *mcp.CallToolRequest, in ActivityInput) (*mcp.CallToolResult, any, error) {
	queryType := strings.ToLower(strings.TrimSpace(in.QueryType))
	kind, ok := activityQueryKinds[queryType]
	if !ok {
		return errResult(
			fmt.Errorf("invalid params: queryType must be one of audit|events|feed, got %q", in.QueryType),
			suggestActions("activity", "queryType=audit|events|feed"),
		)
	}
	if in.Action == "" {
		in.Action = ActionCreate
	}
	r := crudResource{Tool: "activity", Group: "activity.miloapis.com", Kind: kind}
	return r.handler(ctx, nil, in.RoutedInput)
}
