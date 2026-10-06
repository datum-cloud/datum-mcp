package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPrompts adds the MCP prompts that encode the most common
// multi-step workflows, so an agent doesn't have to re-derive a known-good
// call sequence from tool descriptions alone.
//
// The sequences below reflect the actual provisioning model as observed
// against a live org during the Phase 0 smoke test: an HTTPProxy provisions
// its own Gateway and HTTPRoute as owned children, and a DNSZone provisions
// its own default NS DNSRecordSet. Agents create the parent resource; the
// gateways/httproutes/dnsrecordsets tools are for inspecting (and, for
// dnsrecordsets, adding records beyond the defaults), not for driving
// creation themselves.
//
// One thing that isn't owned-child automatic, confirmed against a live org:
// a Workload never auto-creates the NetworkService an HTTPProxy needs as a
// backend, including on redeploy - that's a separate resource an agent must
// create (or verify still exists) itself. And a custom hostname on an
// HTTPProxy belongs in its own spec.hostnames, not a hand-created
// DNSRecordSet: the Gateway controller auto-manages the DNS record for each
// spec.hostnames entry, and a manually-created record for the same name
// conflicts with it and silently blocks certificate issuance.
func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "deploy-http-proxy",
		Description: "Expose a backend NetworkService on the public internet via an HTTPProxy.",
		Arguments: []*mcp.PromptArgument{
			{Name: "backend_service", Description: "Name of the NetworkService to route traffic to", Required: true},
			{Name: "backend_port", Description: "Port name declared on the backend NetworkService, e.g. 'http' (a name, not a number)", Required: true},
			{Name: "path_prefix", Description: "URL path prefix to match (default '/')", Required: false},
		},
	}, deployHTTPProxyPrompt)

	s.AddPrompt(&mcp.Prompt{
		Name:        "configure-dns",
		Description: "Create a managed DNS zone for a domain and point its nameservers at Datum.",
		Arguments: []*mcp.PromptArgument{
			{Name: "domain_name", Description: "Fully qualified domain name to manage, e.g. example.com", Required: true},
		},
	}, configureDNSPrompt)

	s.AddPrompt(&mcp.Prompt{
		Name:        "onboard-to-project",
		Description: "Get from a fresh session to a fully configured active organization and project.",
	}, onboardToProjectPrompt)
}

func deployHTTPProxyPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	backendService := req.Params.Arguments["backend_service"]
	backendPort := req.Params.Arguments["backend_port"]
	pathPrefix := req.Params.Arguments["path_prefix"]
	if pathPrefix == "" {
		pathPrefix = "/"
	}
	text := fmt.Sprintf(`Deploy an HTTPProxy that routes to %q:%q under path prefix %q:

0. alb_guide {"skill": "alb-create"} — networking's own procedure: what to check first, the defaults to state out loud
   (Force HTTPS, traffic protection), and dry-run then confirm with the user before every write below.
1. organizations {"action": "list"} then {"action": "set", "name": "<org-id>"}
2. projects {"action": "list"} then {"action": "set", "body": {"name": "<project-id>"}}
3. networkservices {"action": "get", "id": %q} — a Workload never auto-creates its backing NetworkService (not
   even on redeploy), so confirm one actually exists before creating the HTTPProxy. If it 404s, create it first:
   networkservices {"action": "create", "body": {"metadata": {"name": %q}, "spec": {"networkInterfaces": {"selector": {"matchLabels": {"compute.datumapis.com/workload-name": "<workload-name>"}}}, "ports": [{"name": "<port-name>", "port": <port-number>, "protocol": "TCP"}]}}}
   — the port name/number must match a container port on the Workload; inspect the Workload if unsure.
4. httpproxies {"action": "create", "body": {"metadata": {"name": "<proxy-name>"}, "spec": {"rules": [{"matches": [{"path": {"type": "PathPrefix", "value": %q}}], "backends": [{"networkService": {"name": %q, "port": %q}, "weight": 1}]}]}}}
5. alb_get {"name": "<proxy-name>"} for the generated hostname, then alb_diagnose {"name": "<proxy-name>"} if anything is
   wrong. Don't read the HTTPProxy's status.conditions yourself: they aggregate over hostnames and name none, and a clean
   status still doesn't prove the edge is serving it yet - confirm with a request to the generated hostname.

The HTTPProxy provisions its own Gateway and HTTPRoute automatically; use the
gateways/httproutes tools only to inspect those, not to create them yourself.

If you also want a custom domain attached: register and verify it with the domains tool, then add it to the
HTTPProxy's own spec.hostnames (httpproxies {"action": "update", "id": "<proxy-name>", "body": {"spec": {"hostnames": ["<custom-domain>"]}}}).
Do NOT create a DNSRecordSet for it yourself — the Gateway controller auto-manages the correct DNS record for
every entry in spec.hostnames (visible in a subsequent 'get's status.hostnameStatuses[].dnsRecords), and a
manually-created record for the same name conflicts with it and silently blocks certificate issuance.`,
		backendService, backendPort, pathPrefix, backendService, backendService, pathPrefix, backendService, backendPort)
	return &mcp.GetPromptResult{
		Description: "Step-by-step: deploy an HTTPProxy in front of a backend service",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		},
	}, nil
}

func configureDNSPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	domainName := req.Params.Arguments["domain_name"]
	text := fmt.Sprintf(`Configure managed DNS for %q:

1. organizations {"action": "list"} then {"action": "set", "name": "<org-id>"}
2. projects {"action": "list"} then {"action": "set", "body": {"name": "<project-id>"}}
3. dnszoneclasses {"action": "list"} — pick a dnsZoneClassName (usually there's exactly one).
4. dnszones {"action": "create", "body": {"metadata": {"name": "<zone-slug>"}, "spec": {"domainName": %q, "dnsZoneClassName": "<class-from-step-3>"}}}
5. dnszones {"action": "get", "id": "<zone-slug>"} — read status.domainRef.status.nameservers and tell the user to configure those at their registrar.

Creating the DNSZone auto-provisions its default NS DNSRecordSet; only use
dnsrecordsets to add records beyond that (A/AAAA/CNAME/TXT/etc.):

6. dnsrecordsets {"action": "create", "body": {"metadata": {"name": "<record-name>"}, "spec": {"dnsZoneRef": {"name": "<zone-slug>"}, "recordType": "<A|AAAA|CNAME|TXT|...>", "records": [...]}}}`, domainName, domainName)
	return &mcp.GetPromptResult{
		Description: "Step-by-step: configure managed DNS for a domain",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		},
	}, nil
}

func onboardToProjectPrompt(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	text := `Get set up for this session:

1. context {} — check authenticated, active_organization, active_project, and next_step.
2. If next_step mentions 'organizations': call organizations {"action": "list"}, pick one, then
   organizations {"action": "set", "name": "<org-id>"}. Then call context {} again.
3. If next_step (now) mentions 'projects': call projects {"action": "list"}, pick one, then
   projects {"action": "set", "body": {"name": "<project-id>"}}. Then call context {} again.
4. Once context {} returns next_step: null, you're ready to use any resource tool.`
	return &mcp.GetPromptResult{
		Description: "Step-by-step: onboard a fresh session to an active org/project",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		},
	}, nil
}
