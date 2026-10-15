package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dnsagentdocs "go.miloapis.com/dns-operator/docs/agent"
	"go.miloapis.com/dns-operator/pkg/dnsagent"

	"github.com/datum-cloud/datum-mcp/internal/api"
)

// The dns toolset (DATUM_MCP_DISABLE_TOOLSETS=dns, which also gates the
// dnszones/dnsrecordsets/dnszoneclasses tools) mounts DNS's own agent
// tools - dns_zones_list, dns_zones_get, dns_zone_diagnose,
// dns_delegation_check, dns_records_list, dns_records_get,
// dns_record_diagnose, dns_record_render, dns_zone_discovery_get - straight
// from dns-operator, plus the knowledge and skills written to go with them:
// the same code the in-cluster assistant and `datumctl dns` run. The
// dnszones/dnsrecordsets write tools are core and stay registered either way.

const dnsGuideToolDescription = "Read DNS's own guidance for zones and records (a DNSZone and the DNSRecordSets in it). " +
	"Call with no skill FIRST, before reading or changing one: it returns the DNS model, how to read its conditions and " +
	"who owns each record, and the list of skills. Then call with skill='<name>' to load one procedure - e.g. " +
	"'record-create' before adding or changing a record, 'zone-not-resolving' when a zone doesn't answer. " +
	"dns_zone_diagnose and dns_record_diagnose name the skill for the cause they find. Read-only."

// dnsPreamble maps the assistant-facing documents onto this server.
const dnsPreamble = `These documents are DNS's own, written for Datum's in-product assistant. In this MCP server:

- The dns_* tools read the ACTIVE PROJECT (see 'projects' action=set) and take no project argument. Use them to read
  zones and records - never reason from raw dnszones/dnsrecordsets status yourself when dns_zone_diagnose or
  dns_record_diagnose can walk it.
- resources_plan = make the write call ('dnsrecordsets' or 'dnszones', action=create or update) with the manifest
  dns_record_render returned and dryRun: true, then tell the user in their own words exactly what it will do.
- resources_apply = repeat that identical call without dryRun, only after the user has said yes. There is no plan
  token here, so nothing enforces that the body is unchanged: do not change it between the dry run and the real call.
- resources_list / schema_get = the dedicated list tools, and 'apis' action=get with detail=structure.`

var dnsToolset = agentToolset{
	Name:             "dns",
	Subject:          "DNS",
	GuideTitle:       "DNS guidance",
	GuideDescription: dnsGuideToolDescription,
	Docs:             dnsagentdocs.FS,
	KnowledgeFile:    dnsagentdocs.KnowledgeFile,
	SkillsDir:        dnsagentdocs.SkillsDir,
	Preamble:         dnsPreamble,
	PromptArg: &mcp.PromptArgument{
		Name: "zone", Description: "Name of the DNS zone (DNSZone) in question, if there is one already", Required: false,
	},
	PromptLead: "The DNS zone in question is %q.",
	Register:   func(s *mcp.Server) { dnsagent.RegisterTools(s, dnsDeps) },
}

// dnsDeps resolves what one dns_* call reads through: this server's own login,
// pointed at the active project.
func dnsDeps(ctx context.Context) (dnsagent.ToolDeps, error) {
	p, err := activeProject(ctx)
	if err != nil {
		return dnsagent.ToolDeps{}, err
	}
	scheme, err := dnsagent.NewScheme()
	if err != nil {
		return dnsagent.ToolDeps{}, err
	}
	cli, err := api.NewProjectTypedClient(ctx, p, scheme)
	if err != nil {
		return dnsagent.ToolDeps{}, err
	}
	return dnsagent.ToolDeps{Reader: dnsagent.NewClientReader(cli), Namespace: dnsagent.Namespace}, nil
}
