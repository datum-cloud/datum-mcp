package server

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentdocs "go.datum.net/network-services-operator/docs/agent"
	"go.datum.net/network-services-operator/pkg/albagent"

	"github.com/datum-cloud/datum-mcp/internal/api"
	"github.com/datum-cloud/datum-mcp/internal/auth"
)

// The alb toolset (DATUM_MCP_DISABLE_TOOLSETS=alb) mounts networking's own
// read-only Application Load Balancer tools - alb_list, alb_get, alb_diagnose,
// alb_reason_explain, alb_traffic_summary - straight from
// network-services-operator, plus the knowledge and skills written to go with
// them. Nothing about how a load balancer is read lives in this repo: the
// diagnosis walk, the reason catalog and the product decoding are the same
// code the in-cluster assistant and `datumctl alb` run, so the three can't
// describe one load balancer differently.
//
// What this file adds is only what differs on a customer's machine: the
// identity and project come from this server's own login and active project
// rather than a request header, and the documents - written for the assistant,
// whose plan/apply tools don't exist here - are served with a preamble mapping
// them onto this server's write path.

const albGuideToolName = "alb_guide"

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

// albSkill is one procedure from networking's docs/agent/skills.
type albSkill struct {
	Name        string // file name without .md, e.g. "alb-create"
	Title       string // from the "# Skill: <title>" heading
	Description string // the opening "Use when ..." paragraph
	Body        string
}

// albSkills reads every skill embedded in network-services-operator. Titles
// and descriptions come from the documents themselves rather than a copy kept
// here, so a skill networking adds or rewords shows up without a change in
// this repo.
func albSkills() (map[string]albSkill, error) {
	entries, err := fs.ReadDir(agentdocs.FS, agentdocs.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("reading embedded alb skills: %w", err)
	}
	out := map[string]albSkill{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := agentdocs.FS.ReadFile(path.Join(agentdocs.SkillsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded alb skill %s: %w", e.Name(), err)
		}
		sk := parseALBSkill(strings.TrimSuffix(e.Name(), ".md"), string(b))
		out[sk.Name] = sk
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no alb skills embedded in %s", agentdocs.SkillsDir)
	}
	return out, nil
}

// parseALBSkill pulls the title from the leading "# Skill: ..." heading and the
// description from the first paragraph after it.
func parseALBSkill(name, body string) albSkill {
	sk := albSkill{Name: name, Title: name, Body: body}
	lines := strings.Split(body, "\n")
	i := 0
	for ; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "# ") {
			sk.Title = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "# "), "Skill:"))
			i++
			break
		}
	}
	var para []string
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			break
		}
		para = append(para, t)
	}
	sk.Description = strings.Join(para, " ")
	return sk
}

func sortedSkillNames(skills map[string]albSkill) []string {
	names := make([]string, 0, len(skills))
	for n := range skills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// albPromptName is the MCP prompt a skill is offered as. Skills not already
// named for load balancers get an alb- prefix, so "dns-delegation" can't be
// mistaken for (or collide with) a DNS prompt like configure-dns.
func albPromptName(skill string) string {
	if strings.HasPrefix(skill, "alb-") {
		return skill
	}
	return "alb-" + skill
}

// albDeps resolves what one alb_* call reads through: this server's own login,
// pointed at the active project.
func albDeps(ctx context.Context) (albagent.ToolDeps, error) {
	// Project first: with none active there's nothing to read, and no reason
	// to open a browser login just to say so.
	p, err := resolveProjectName("")
	if err != nil {
		return albagent.ToolDeps{}, err
	}
	if _, err := auth.EnsureAuth(ctx); err != nil {
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

type ALBGuideInput struct {
	Skill string `json:"skill,omitempty" jsonschema:"Skill to load, e.g. 'alb-create' or 'alb-not-serving'. Omit to get the orientation document and the list of skills."`
}

func toolALBGuide(skills map[string]albSkill) mcp.ToolHandlerFor[ALBGuideInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ALBGuideInput) (*mcp.CallToolResult, any, error) {
		name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Skill)), ".md")
		if name == "" {
			knowledge, err := agentdocs.FS.ReadFile(agentdocs.KnowledgeFile)
			if err != nil {
				return errResult(fmt.Errorf("reading embedded alb knowledge: %w", err), nil)
			}
			return textResult(albPreamble + "\n\n---\n\n" + string(knowledge) + "\n\n---\n\n" + albSkillsIndex(skills))
		}
		sk, ok := skills[name]
		if !ok {
			return errResult(
				fmt.Errorf("unknown alb skill %q; known skills: %s", in.Skill, strings.Join(sortedSkillNames(skills), ", ")),
				&SuggestedAction{Tool: albGuideToolName},
			)
		}
		return textResult(albPreamble + "\n\n---\n\n" + sk.Body)
	}
}

func albSkillsIndex(skills map[string]albSkill) string {
	var b strings.Builder
	b.WriteString("Skills (load one with alb_guide skill='<name>'):\n")
	for _, n := range sortedSkillNames(skills) {
		fmt.Fprintf(&b, "- %s: %s\n", n, skills[n].Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

// textResult returns documents as plain text rather than okResult's JSON, so
// the Markdown reads as written instead of as one escaped string.
func textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

func albSkillPrompt(sk albSkill) mcp.PromptHandler {
	return func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := albPreamble + "\n\n---\n\n" + sk.Body
		if lb := strings.TrimSpace(req.Params.Arguments["load_balancer"]); lb != "" {
			text = fmt.Sprintf("The load balancer in question is %q.\n\n", lb) + text
		}
		return &mcp.GetPromptResult{
			Description: sk.Title,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	}
}

// registerALB adds the alb toolset: networking's read-only tools, alb_guide,
// and one prompt per skill.
func registerALB(s *mcp.Server) error {
	skills, err := albSkills()
	if err != nil {
		return err
	}

	albagent.RegisterTools(s, albDeps)

	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name:        albGuideToolName,
		Title:       "Load balancer guidance",
		Description: albGuideToolDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &notDestructive, IdempotentHint: true},
	}, toolALBGuide(skills))

	for _, n := range sortedSkillNames(skills) {
		sk := skills[n]
		s.AddPrompt(&mcp.Prompt{
			Name:        albPromptName(sk.Name),
			Description: "Application Load Balancer: " + sk.Description,
			Arguments: []*mcp.PromptArgument{
				{Name: "load_balancer", Description: "Name of the load balancer (HTTPProxy), if there is one already", Required: false},
			},
		}, albSkillPrompt(sk))
	}
	return nil
}
