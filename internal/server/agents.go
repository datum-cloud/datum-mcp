package server

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/datum-cloud/datum-mcp/internal/auth"
)

// An agent toolset mounts one service's own read-only agent tools straight from
// that service's repo, plus the knowledge and skills written to go with them:
// the same code and documents the in-cluster assistant gets from that service's
// MCP server, so the two can't describe one resource differently. Nothing about
// how a service's resources are read lives in this repo.
//
// What a toolset adds is only what differs on a customer's machine: identity and
// project come from this server's own login and active project rather than a
// request header, and the documents - written for the assistant, whose
// plan/apply tools don't exist here - are served behind a preamble mapping them
// onto this server's write path.
type agentToolset struct {
	// Name is the toolset (DATUM_MCP_DISABLE_TOOLSETS), the prefix of its guide
	// tool (<name>_guide) and of its skill prompts (<name>-).
	Name string
	// Subject is what the toolset is about, leading each skill prompt's
	// description, e.g. "Application Load Balancer".
	Subject string

	GuideTitle       string
	GuideDescription string

	// Docs holds KnowledgeFile and one Markdown file per skill under SkillsDir.
	Docs          fs.FS
	KnowledgeFile string
	SkillsDir     string

	// Preamble maps the assistant-facing documents onto this server. It's
	// prepended to every document the guide returns, because a skill loaded on
	// its own still says "plan" and "apply".
	Preamble string

	// PromptArg optionally names the object a skill prompt is about. When the
	// caller fills it in, PromptLead (formatted with the value, %q) opens the
	// prompt.
	PromptArg  *mcp.PromptArgument
	PromptLead string

	// Register adds the service's own tools.
	Register func(*mcp.Server)
}

// agentToolsets is every service whose agent tools this server mounts.
var agentToolsets = []agentToolset{albToolset, networkToolset, computeToolset, dnsToolset, activityToolset}

func (t agentToolset) guideToolName() string { return t.Name + "_guide" }

// promptName is the MCP prompt a skill is offered as. Skills not already named
// for the toolset get its prefix, so "dns-delegation" can't be mistaken for (or
// collide with) a DNS prompt like configure-dns, and two services' skills of
// the same name ("domain-verification") stay distinct.
func (t agentToolset) promptName(skill string) string {
	if strings.HasPrefix(skill, t.Name+"-") {
		return skill
	}
	return t.Name + "-" + skill
}

// agentSkill is one procedure from a service's skills directory.
type agentSkill struct {
	Name        string // file name without .md, e.g. "alb-create"
	Title       string // from the "# Skill: <title>" heading
	Description string // the opening "Use when ..." paragraph
	Body        string
}

// skills reads every skill the service embeds. Titles and descriptions come
// from the documents themselves rather than a copy kept here, so a skill the
// service adds or rewords shows up without a change in this repo.
func (t agentToolset) skills() (map[string]agentSkill, error) {
	entries, err := fs.ReadDir(t.Docs, t.SkillsDir)
	if err != nil {
		return nil, fmt.Errorf("reading embedded %s skills: %w", t.Name, err)
	}
	out := map[string]agentSkill{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := fs.ReadFile(t.Docs, path.Join(t.SkillsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded %s skill %s: %w", t.Name, e.Name(), err)
		}
		sk := parseSkill(strings.TrimSuffix(e.Name(), ".md"), string(b))
		out[sk.Name] = sk
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s skills embedded in %s", t.Name, t.SkillsDir)
	}
	return out, nil
}

// parseSkill pulls the title from the leading "# Skill: ..." heading and the
// description from the first paragraph after it.
func parseSkill(name, body string) agentSkill {
	sk := agentSkill{Name: name, Title: name, Body: body}
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

func sortedSkillNames(skills map[string]agentSkill) []string {
	names := make([]string, 0, len(skills))
	for n := range skills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// activeProject is the project every agent tool reads: this server's active
// one, under its own login. Project first: with none active there's nothing to
// read, and no reason to open a browser login just to say so.
func activeProject(ctx context.Context) (string, error) {
	p, err := resolveProjectName(ctx, "")
	if err != nil {
		return "", err
	}
	if _, err := auth.EnsureAuth(ctx); err != nil {
		return "", err
	}
	return p, nil
}

type GuideInput struct {
	Skill string `json:"skill,omitempty" jsonschema:"Skill to load, by name from the list a call with no skill returns. Omit to get the orientation document and the list of skills."`
}

func (t agentToolset) guide(skills map[string]agentSkill) mcp.ToolHandlerFor[GuideInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in GuideInput) (*mcp.CallToolResult, any, error) {
		name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(in.Skill)), ".md")
		if name == "" {
			knowledge, err := fs.ReadFile(t.Docs, t.KnowledgeFile)
			if err != nil {
				return errResult(fmt.Errorf("reading embedded %s knowledge: %w", t.Name, err), nil)
			}
			return textResult(t.Preamble + "\n\n---\n\n" + string(knowledge) + "\n\n---\n\n" + t.skillsIndex(skills))
		}
		sk, ok := skills[name]
		if !ok {
			return errResult(
				fmt.Errorf("unknown %s skill %q; known skills: %s", t.Name, in.Skill, strings.Join(sortedSkillNames(skills), ", ")),
				&SuggestedAction{Tool: t.guideToolName()},
			)
		}
		return textResult(t.Preamble + "\n\n---\n\n" + sk.Body)
	}
}

func (t agentToolset) skillsIndex(skills map[string]agentSkill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Skills (load one with %s skill='<name>'):\n", t.guideToolName())
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

func (t agentToolset) skillPrompt(sk agentSkill) mcp.PromptHandler {
	return func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := t.Preamble + "\n\n---\n\n" + sk.Body
		if t.PromptArg != nil {
			if v := strings.TrimSpace(req.Params.Arguments[t.PromptArg.Name]); v != "" {
				text = fmt.Sprintf(t.PromptLead, v) + "\n\n" + text
			}
		}
		return &mcp.GetPromptResult{
			Description: sk.Title,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	}
}

// register adds the toolset: the service's own tools, <name>_guide, and one
// prompt per skill.
func (t agentToolset) register(s *mcp.Server) error {
	skills, err := t.skills()
	if err != nil {
		return err
	}

	t.Register(s)

	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name:        t.guideToolName(),
		Title:       t.GuideTitle,
		Description: t.GuideDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &notDestructive, IdempotentHint: true},
	}, t.guide(skills))

	for _, n := range sortedSkillNames(skills) {
		sk := skills[n]
		p := &mcp.Prompt{
			Name:        t.promptName(sk.Name),
			Description: t.Subject + ": " + sk.Description,
		}
		if t.PromptArg != nil {
			p.Arguments = []*mcp.PromptArgument{t.PromptArg}
		}
		s.AddPrompt(p, t.skillPrompt(sk))
	}
	return nil
}
