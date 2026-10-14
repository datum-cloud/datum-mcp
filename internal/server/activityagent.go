package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	activityclient "go.miloapis.com/activity/pkg/client/clientset/versioned/typed/activity/v1alpha1"
	activityagentdocs "go.miloapis.com/activity/pkg/mcp/agentdocs"
	"go.miloapis.com/activity/pkg/mcp/httpserver"
	activitytools "go.miloapis.com/activity/pkg/mcp/tools"

	"github.com/datum-cloud/datum-mcp/internal/api"
)

// The activity toolset (DATUM_MCP_DISABLE_TOOLSETS=activity, which also gates
// the generic 'activity' tool) mounts Activity's own query tools straight from
// the activity repo, plus the knowledge and skills written to go with them.
// It registers exactly the tools the in-cluster activity-mcp serves the
// assistant (httpserver.ExposedTools): raw audit logs and activity policies are
// an operator's view, not a project member's, and stay out here too.

const activityGuideToolDescription = "Read Activity's own guidance for answering what happened in the project: who changed " +
	"what, when, whether it worked, and what the platform said about it afterwards. Call with no skill FIRST: it returns " +
	"how changes are recorded, which query tool answers which question, and the list of skills. Then call with " +
	"skill='<name>' to load one procedure - e.g. 'what-changed' when something broke and the question is what changed, " +
	"'who-changed-resource' for one resource's history. Read-only."

// activityPreamble maps the assistant-facing documents onto this server.
const activityPreamble = `These documents are Activity's own, written for Datum's in-product assistant. In this MCP server:

- "The project the person is working in" is this server's ACTIVE PROJECT (see 'projects' action=set). The query tools
  take no project argument; to look at another project, set it active first and say so.
- Every Activity tool here is read-only. Anything a procedure suggests changing goes through this server's write tools
  with dryRun: true first, and for real only after the user has said yes.`

var activityToolset = agentToolset{
	Name:             "activity",
	Subject:          "Activity",
	GuideTitle:       "Activity guidance",
	GuideDescription: activityGuideToolDescription,
	Docs:             activityagentdocs.FS,
	KnowledgeFile:    activityagentdocs.KnowledgeFile,
	SkillsDir:        activityagentdocs.SkillsDir,
	Preamble:         activityPreamble,
	PromptArg: &mcp.PromptArgument{
		Name: "resource", Description: "The resource in question as '<Kind> <name>', e.g. 'HTTPProxy my-app', if there is one", Required: false,
	},
	PromptLead: "The resource in question is %q.",
	Register: func(s *mcp.Server) {
		activitytools.NewToolProviderWithResolver(activityClient).
			RegisterToolsWithOptions(s, activitytools.RegisterOptions{Tools: httpserver.ExposedTools})
	},
}

// activityClient resolves the Activity API client one call reads through: this
// server's own login, pointed at the active project.
func activityClient(ctx context.Context) (activityclient.ActivityV1alpha1Interface, error) {
	p, err := activeProject(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := api.NewProjectRESTConfig(ctx, p)
	if err != nil {
		return nil, err
	}
	return activityclient.NewForConfig(cfg)
}
