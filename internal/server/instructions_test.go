package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEveryToolsetHasASummary(t *testing.T) {
	known := map[string]bool{}
	for _, name := range knownToolsets() {
		known[name] = true
		if toolsetSummaries[name] == "" {
			t.Errorf("toolset %q has no entry in toolsetSummaries", name)
		}
	}
	for name := range toolsetSummaries {
		if !known[name] {
			t.Errorf("toolsetSummaries has %q, which isn't a toolset", name)
		}
	}
}

func instructions(t *testing.T) string {
	t.Helper()
	res := connect(t).InitializeResult()
	if res == nil || res.Instructions == "" {
		t.Fatalf("expected server instructions at initialize")
	}
	return res.Instructions
}

func TestInstructionsListEveryToolsetByDefault(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	got := instructions(t)
	for _, name := range knownToolsets() {
		if !strings.Contains(got, "- "+name+": ") {
			t.Errorf("expected toolset %q in the instructions", name)
		}
	}
	if strings.Contains(got, "Turned off") {
		t.Errorf("expected no turned-off section with every toolset on, got:\n%s", got)
	}
	if !strings.Contains(got, "'context'") {
		t.Errorf("expected the instructions to say to start with 'context'")
	}
}

func TestInstructionsNameTurnedOffToolsetsAndHowToEnableThem(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "alb,compute")
	got := instructions(t)
	on, off, found := strings.Cut(got, "Turned off in this session")
	if !found {
		t.Fatalf("expected a turned-off section, got:\n%s", got)
	}
	for _, name := range []string{"alb", "compute"} {
		if strings.Contains(on, "- "+name+": ") || !strings.Contains(off, "- "+name+": ") {
			t.Errorf("expected %q listed only as turned off", name)
		}
	}
	if !strings.Contains(on, "- dns: ") {
		t.Errorf("expected dns still listed as on")
	}
	if !strings.Contains(off, "DATUM_MCP_ENABLE_TOOLSETS") {
		t.Errorf("expected the instructions to say how to turn a toolset on")
	}
}

// Each service's documents tell the assistant to "hand off" to another
// service's agent; here that has to mean the other toolset on this server.
func TestEveryGuideMapsHandOffsToTheOtherToolsets(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	for _, ts := range agentToolsets {
		text, isErr := guideText(t, cs, ts.guideToolName(), map[string]any{})
		if isErr {
			t.Fatalf("%s: unexpected error: %s", ts.Name, text)
		}
		pre, _, _ := strings.Cut(text, "\n---\n")
		if !strings.Contains(pre, "Hand-offs:") {
			t.Errorf("%s: expected a hand-off mapping in the preamble", ts.Name)
		}
		for _, other := range agentToolsets {
			if other.Name == ts.Name {
				continue
			}
			if !strings.Contains(pre, other.Subject+" -> "+other.guideToolName()) {
				t.Errorf("%s: expected the hand-off mapping to send %s to %s", ts.Name, other.Subject, other.guideToolName())
			}
		}
		if strings.Contains(pre, ts.Subject+" -> ") {
			t.Errorf("%s: the hand-off mapping shouldn't point a toolset at itself", ts.Name)
		}
	}
}

func TestHandOffsToATurnedOffToolsetSayItsOff(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "compute")
	text, _ := guideText(t, connect(t), "network_guide", map[string]any{})
	pre, _, _ := strings.Cut(text, "\n---\n")
	if strings.Contains(pre, "compute_guide") {
		t.Errorf("expected no pointer at compute_guide while compute is off")
	}
	if !strings.Contains(pre, "Compute -> the compute toolset, which is turned off") {
		t.Errorf("expected the mapping to say compute is off, got:\n%s", pre)
	}
}

func TestSkillPromptsCarryTheHandOffMapping(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	ctx := context.Background()
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	for _, ts := range agentToolsets {
		var name string
		for _, p := range prompts.Prompts {
			if strings.HasPrefix(p.Name, ts.Name+"-") {
				name = p.Name
				break
			}
		}
		if name == "" {
			continue
		}
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name})
		if err != nil {
			t.Fatalf("GetPrompt %s: %v", name, err)
		}
		if text := res.Messages[0].Content.(*mcp.TextContent).Text; !strings.Contains(text, "Hand-offs:") {
			t.Errorf("%s: expected the skill prompt to carry the hand-off mapping", name)
		}
	}
}
