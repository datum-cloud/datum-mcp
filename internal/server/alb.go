package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentdocs "go.datum.net/network-services-operator/docs/agent"
	"go.datum.net/network-services-operator/pkg/albagent"

	"github.com/datum-cloud/datum-mcp/internal/api"
)

// The alb toolset (DATUM_MCP_DISABLE_TOOLSETS=alb, which also gates the
// httpproxies/httproutes/gateways/trafficprotectionpolicies tools) mounts
// networking's own read-only Application Load Balancer tools - alb_list,
// alb_get, alb_diagnose, alb_reason_explain, alb_traffic_summary - straight from
// network-services-operator, plus the knowledge and skills written to go with
// them. Nothing about how a load balancer is read lives in this repo: the
// diagnosis walk, the reason catalog and the product decoding are the same
// code the in-cluster assistant and `datumctl alb` run, so the three can't
// describe one load balancer differently.
//
// See agentToolset for what this server adds on top.

const albGuideToolDescription = "Read networking's own guidance for Application Load Balancers (an HTTPProxy and what's attached to it). " +
	"Call with no skill FIRST, before reading or changing a load balancer: it returns how a load balancer is put together, how to read its " +
	"status without getting it wrong (several conditions here read the opposite of what they look like), and the list of skills. " +
	"Then call with skill='<name>' to load one procedure - e.g. 'alb-create' before creating or changing one, 'alb-not-serving' when one " +
	"isn't working. alb_diagnose names the skill for the cause it finds. Read-only."

// albPreamble maps the assistant-facing documents onto this server. It's
// prepended to every document alb_guide returns, because a skill loaded on its
// own still says "plan" and "apply".
const albPreamble = `These documents are networking's own, written for Datum's in-product assistant. In this MCP server:

- The alb_* tools read the ACTIVE PROJECT (see 'projects' action=set) and take no project argument. Use them to read
  load balancers - never reason from raw httpproxies status yourself when alb_diagnose can walk it.
- "Validate" / "plan" a change = make the write call (httpproxies, trafficprotectionpolicies, domains, resource, ...)
  with dryRun: true, then tell the user in their own words exactly what it will do.
- "Apply" = repeat that identical call without dryRun, only after the user has said yes. There is no plan token here,
  so nothing enforces that the body is unchanged: do not change it between the dry run and the real call.
- resources_list / schema_get = the dedicated list tools, and 'apis' action=get with detail=structure.
- Network services: a load balancer never creates one. The 'networkservices' tool can, but only as its own step the
  user explicitly agreed to - never implicitly to make a route work.
- Never set a basic-auth password through this server. Give the user the command to run themselves:
  echo '<password>' | datumctl alb auth set <name> --user <user> --password-stdin
- The user owns their DNS and domain verification records: tell them the exact record, don't create it for them in
  a zone they didn't ask you to touch.`

var albToolset = agentToolset{
	Name:             "alb",
	Subject:          "Application Load Balancer",
	GuideTitle:       "Load balancer guidance",
	GuideDescription: albGuideToolDescription,
	Docs:             agentdocs.FS,
	KnowledgeFile:    agentdocs.KnowledgeFile,
	SkillsDir:        agentdocs.SkillsDir,
	Preamble:         albPreamble,
	PromptArg: &mcp.PromptArgument{
		Name: "load_balancer", Description: "Name of the load balancer (HTTPProxy), if there is one already", Required: false,
	},
	PromptLead: "The load balancer in question is %q.",
	Register:   func(s *mcp.Server) { albagent.RegisterTools(s, albDeps) },
}

// albDeps resolves what one alb_* call reads through: this server's own login,
// pointed at the active project.
func albDeps(ctx context.Context) (albagent.ToolDeps, error) {
	p, err := activeProject(ctx)
	if err != nil {
		return albagent.ToolDeps{}, err
	}
	scheme, err := albagent.NewScheme()
	if err != nil {
		return albagent.ToolDeps{}, err
	}
	cli, err := api.NewProjectTypedClient(ctx, p, scheme)
	if err != nil {
		return albagent.ToolDeps{}, err
	}
	cfg, err := api.NewProjectRESTConfig(ctx, p)
	if err != nil {
		return albagent.ToolDeps{}, err
	}
	return albagent.ToolDeps{
		Reader:    albagent.NewClientReader(cli),
		Namespace: albagent.Namespace,
		Logs:      albagent.NewClientLogReader(cfg),
	}, nil
}
