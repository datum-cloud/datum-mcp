package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	computeagentdocs "go.datum.net/compute/docs/agent"
	"go.datum.net/compute/pkg/computeagent"

	"github.com/datum-cloud/datum-mcp/internal/api"
)

// The compute toolset (DATUM_MCP_DISABLE_TOOLSETS=compute, which also gates the
// workloads tool) mounts compute's own agent tools -
// compute_workloads_list, compute_workloads_get, compute_instances_list,
// compute_workload_diagnose, compute_reason_explain,
// compute_instance_types_list, compute_workload_render - straight from the
// compute repo, plus the knowledge and skills written to go with them: the same
// code the in-cluster assistant and `datumctl compute` run.

const computeGuideToolDescription = "Read compute's own guidance for workloads (a Workload, the WorkloadDeployments it " +
	"rolls out per placement, and their Instances). Call with no skill FIRST, before reading or changing one: it returns " +
	"the compute model, how to read its conditions, and the list of skills. Then call with skill='<name>' to load one " +
	"procedure - e.g. 'workload-create' before creating or changing a workload, 'workload-not-available' when one isn't " +
	"running. compute_workload_diagnose names the skill for the cause it finds. Read-only."

// computePreamble maps the assistant-facing documents onto this server.
const computePreamble = `These documents are compute's own, written for Datum's in-product assistant. In this MCP server:

- The compute_* tools read the ACTIVE PROJECT (see 'projects' action=set) and take no project argument. Use them to read
  workloads - never reason from raw workloads/instances status yourself when compute_workload_diagnose can walk it.
- resources_list / resources_get = the dedicated tools ('workloads', 'networks'), compute_instances_list for a
  workload's instances, or 'resource' for a kind without one (e.g. group=compute.datumapis.com kind=Instance
  namespace=default for one instance by name, or kind=RuntimeClass). schema_get = 'apis' action=get with
  detail=structure.
- resources_plan = 'workloads' action=create (or update) with the manifest compute_workload_render returned and
  dryRun: true, then tell the user in their own words exactly what it will do. A plan that also creates a Network is
  one dry run per object, the Network first.
- resources_apply = repeat those identical calls without dryRun, only after the user has said yes. There is no plan
  token here, so nothing enforces that the body is unchanged: do not change it between the dry run and the real call.
- locations_list = 'resource' action=list group=locations.miloapis.com kind=Location.
- quota_get = 'resource' action=list group=quota.miloapis.com kind=AllowanceBucket namespace=milo-system, with
  labelSelector=quota.miloapis.com/consumer-kind=Project for this project's own buckets.
- report_capability_gap__* does not exist here. When compute can't do what the user asked, tell them so plainly
  rather than working around it.`

var computeToolset = agentToolset{
	Name:             "compute",
	Subject:          "Compute",
	GuideTitle:       "Compute guidance",
	GuideDescription: computeGuideToolDescription,
	Docs:             computeagentdocs.FS,
	KnowledgeFile:    computeagentdocs.KnowledgeFile,
	SkillsDir:        computeagentdocs.SkillsDir,
	Preamble:         computePreamble,
	PromptArg: &mcp.PromptArgument{
		Name: "workload", Description: "Name of the workload in question, if there is one already", Required: false,
	},
	PromptLead: "The workload in question is %q.",
	Register:   func(s *mcp.Server) { computeagent.RegisterTools(s, computeDeps) },
}

// computeDeps resolves what one compute_* call reads through: this server's
// own login, pointed at the active project.
func computeDeps(ctx context.Context) (computeagent.ToolDeps, error) {
	p, err := activeProject(ctx)
	if err != nil {
		return computeagent.ToolDeps{}, err
	}
	scheme, err := computeagent.NewScheme()
	if err != nil {
		return computeagent.ToolDeps{}, err
	}
	cli, err := api.NewProjectTypedClient(ctx, p, scheme)
	if err != nil {
		return computeagent.ToolDeps{}, err
	}
	return computeagent.ToolDeps{Reader: computeagent.NewClientReader(cli), Namespace: computeagent.Namespace}, nil
}
