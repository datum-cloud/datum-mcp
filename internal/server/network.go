package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	networkagentdocs "go.datum.net/network-services-operator/docs/network-agent"
	"go.datum.net/network-services-operator/pkg/networkagent"

	"github.com/datum-cloud/datum-mcp/internal/api"
)

// The network toolset (DATUM_MCP_DISABLE_TOOLSETS=network) mounts networking's
// own read-only network and network service tools - network_list, network_get,
// network_diagnose, network_service_list, network_service_diagnose,
// network_reason_explain, network_render - straight from
// network-services-operator, plus the knowledge and skills written to go with
// them: the same code the in-cluster assistant and `datumctl network` run.

const networkGuideToolDescription = "Read networking's own guidance for networks and network services (a Network, the " +
	"NetworkService a load balancer routes to, and the contexts and interfaces that connect workloads to them). " +
	"Call with no skill FIRST, before reading or changing one: it returns the network model, the rules that always hold, " +
	"how to read status, and the list of skills. Then call with skill='<name>' to load one procedure - e.g. " +
	"'network-service-create' before exposing a workload, 'network-service-not-serving' when a service sends traffic nowhere. " +
	"network_diagnose and network_service_diagnose name the skill for the cause they find. Read-only."

// networkPreamble maps the assistant-facing documents onto this server.
const networkPreamble = `These documents are networking's own, written for Datum's in-product assistant. In this MCP server:

- The network_* tools read the ACTIVE PROJECT (see 'projects' action=set) and take no project argument. Use them to read
  networks and network services - never reason from raw status yourself when network_diagnose or
  network_service_diagnose can walk it.
- resources_list = the dedicated list tools ('networks', 'subnets', 'networkservices'), or 'resource' action=list for a
  kind without one (NetworkContext, NetworkBinding, NetworkInterface, ...). schema_get = 'apis' action=get with
  detail=structure.
- resources_plan = make the write call ('networks', 'networkservices', or 'resource') with the manifest network_render
  returned and dryRun: true, then tell the user in their own words exactly what it will do. A plan of several objects is
  one dry run per object, in the order the procedure gives.
- resources_apply = repeat those identical calls without dryRun, only after the user has said yes. There is no plan
  token here, so nothing enforces that the body is unchanged: do not change it between the dry run and the real call.`

var networkToolset = agentToolset{
	Name:             "network",
	Subject:          "Network",
	GuideTitle:       "Network guidance",
	GuideDescription: networkGuideToolDescription,
	Docs:             networkagentdocs.FS,
	KnowledgeFile:    networkagentdocs.KnowledgeFile,
	SkillsDir:        networkagentdocs.SkillsDir,
	Preamble:         networkPreamble,
	PromptArg: &mcp.PromptArgument{
		Name: "name", Description: "Name of the network or network service in question, if there is one already", Required: false,
	},
	PromptLead: "The network or network service in question is %q.",
	Register:   func(s *mcp.Server) { networkagent.RegisterTools(s, networkDeps) },
}

// networkDeps resolves what one network_* call reads through: this server's
// own login, pointed at the active project.
func networkDeps(ctx context.Context) (networkagent.ToolDeps, error) {
	p, err := activeProject(ctx)
	if err != nil {
		return networkagent.ToolDeps{}, err
	}
	scheme, err := networkagent.NewScheme()
	if err != nil {
		return networkagent.ToolDeps{}, err
	}
	cli, err := api.NewProjectTypedClient(ctx, p, scheme)
	if err != nil {
		return networkagent.ToolDeps{}, err
	}
	return networkagent.ToolDeps{Reader: networkagent.NewClientReader(cli), Namespace: networkagent.Namespace}, nil
}
