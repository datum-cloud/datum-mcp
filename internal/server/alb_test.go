package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect returns a client session against a freshly built server, the way a
// real MCP client sees it.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := NewMCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listedTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func listedPrompts(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Prompt {
	t.Helper()
	res, err := cs.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	out := map[string]*mcp.Prompt{}
	for _, p := range res.Prompts {
		out[p.Name] = p
	}
	return out
}

var albToolNames = []string{"alb_list", "alb_get", "alb_diagnose", "alb_reason_explain", "alb_traffic_summary", albGuideToolName}

func TestALBToolsRegistered(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	for _, name := range albToolNames {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("expected tool %q to be registered", name)
			continue
		}
		// networking publishes no mutating tool, and neither does alb_guide.
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("expected %q to be annotated read-only", name)
		}
	}
}

func TestALBToolsetCanBeDisabled(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "alb")
	cs := connect(t)
	tools := listedTools(t, cs)
	for _, name := range albToolNames {
		if _, ok := tools[name]; ok {
			t.Errorf("expected %q to be gated off when alb is disabled", name)
		}
	}
	for name := range listedPrompts(t, cs) {
		if strings.HasPrefix(name, "alb-") {
			t.Errorf("expected prompt %q to be gated off when alb is disabled", name)
		}
	}
	// The existing httpproxies tool is core and stays.
	if _, ok := tools["httpproxies"]; !ok {
		t.Errorf("expected 'httpproxies' (core) to remain registered")
	}
}

func TestEveryALBSkillIsParsedAndPrompted(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	skills, err := albSkills()
	if err != nil {
		t.Fatalf("albSkills: %v", err)
	}
	// The ones every load balancer conversation hinges on; if networking ever
	// renames them, alb_guide's description and albPreamble need to follow.
	for _, want := range []string{"alb-create", "alb-not-serving"} {
		if _, ok := skills[want]; !ok {
			t.Errorf("expected skill %q to be embedded", want)
		}
	}
	prompts := listedPrompts(t, connect(t))
	for name, sk := range skills {
		if sk.Title == "" || sk.Title == name {
			t.Errorf("skill %q: no title parsed from its '# Skill:' heading", name)
		}
		if !strings.HasPrefix(sk.Description, "Use when") {
			t.Errorf("skill %q: expected a 'Use when ...' description, got %q", name, sk.Description)
		}
		if _, ok := prompts[albPromptName(name)]; !ok {
			t.Errorf("skill %q: expected prompt %q", name, albPromptName(name))
		}
	}
	// AddPrompt replaces on a name collision rather than failing, so a skill
	// that shadowed an existing prompt would silently remove it.
	for _, existing := range []string{"deploy-http-proxy", "configure-dns", "onboard-to-project"} {
		if _, ok := prompts[existing]; !ok {
			t.Errorf("expected existing prompt %q to survive", existing)
		}
	}
}

func TestParseALBSkill(t *testing.T) {
	sk := parseALBSkill("x", "# Skill: doing a thing\n\nUse when one thing\nbreaks.\n\n## Steps\n\n1. fix it\n")
	if sk.Title != "doing a thing" {
		t.Errorf("title: got %q", sk.Title)
	}
	if sk.Description != "Use when one thing breaks." {
		t.Errorf("description: got %q", sk.Description)
	}
}

func guideText(t *testing.T, cs *mcp.ClientSession, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: albGuideToolName, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestALBGuideWithNoSkillOrientsAndIndexes(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	text, isErr := guideText(t, connect(t), map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	for _, want := range []string{
		"dryRun: true",                        // the preamble's plan mapping
		"Application Load Balancers on Datum", // the knowledge document
		"HostnamesInUse",                      // ...including how to read status
		"- alb-create: Use when",              // the skill index
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected orientation to contain %q", want)
		}
	}
}

func TestALBGuideLoadsOneSkillWithThePreamble(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	text, isErr := guideText(t, connect(t), map[string]any{"skill": "alb-create.md"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	// A skill loaded on its own still says "plan" and "apply", so it must
	// arrive with the mapping onto this server's write path.
	if !strings.HasPrefix(text, albPreamble) {
		t.Errorf("expected the skill to be preceded by the preamble")
	}
	if !strings.Contains(text, "# Skill: creating a load balancer") {
		t.Errorf("expected the alb-create body, got:\n%s", text)
	}
}

func TestALBGuideRejectsAnUnknownSkillWithTheList(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	text, isErr := guideText(t, connect(t), map[string]any{"skill": "../../etc/passwd"})
	if !isErr {
		t.Fatalf("expected an error result")
	}
	if !strings.Contains(text, "alb-not-serving") {
		t.Errorf("expected the error to list known skills, got:\n%s", text)
	}
}

func TestALBSkillPromptNamesTheLoadBalancer(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	res, err := connect(t).GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name:      "alb-not-serving",
		Arguments: map[string]string{"load_balancer": "my-app"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	text := promptText(t, res)
	if !strings.HasPrefix(text, `The load balancer in question is "my-app".`) {
		t.Errorf("expected the prompt to name the load balancer first, got:\n%s", text[:min(len(text), 200)])
	}
	if !strings.Contains(text, "alb_diagnose") {
		t.Errorf("expected the alb-not-serving procedure")
	}
}

func TestALBToolsNeedAnActiveProjectBeforeLoggingIn(t *testing.T) {
	// With no project active, albDeps must fail on that alone - before
	// EnsureAuth, which would otherwise open a browser login just to report
	// there's nothing to read.
	if _, err := resolveProjectName(""); err == nil {
		t.Skip("an earlier test left an active project set; albDeps would go on to log in")
	}
	_, err := albDeps(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no active project") {
		t.Fatalf("expected a no-active-project error, got %v", err)
	}
}
