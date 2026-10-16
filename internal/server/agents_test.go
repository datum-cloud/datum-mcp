package server

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"go.datum.net/compute/pkg/computeagent"
	"go.datum.net/network-services-operator/pkg/networkagent"
	"go.miloapis.com/activity/pkg/mcp/httpserver"
	"go.miloapis.com/dns-operator/pkg/dnsagent"
)

// agentToolNames is every tool each service's agent registers, as that service
// names them. If a service adds a tool it arrives here on its own; this list
// pins the ones the guide documents rely on.
var agentToolNames = map[string][]string{
	"alb": {"alb_list", "alb_get", "alb_diagnose", "alb_reason_explain", "alb_traffic_summary"},
	"network": {
		networkagent.ToolList, networkagent.ToolGet, networkagent.ToolDiagnose, networkagent.ToolServiceList,
		networkagent.ToolServiceDiagnose, networkagent.ToolReasonExplain, networkagent.ToolRender,
	},
	"compute": {
		computeagent.ToolWorkloadsList, computeagent.ToolWorkloadsGet, computeagent.ToolInstancesList,
		computeagent.ToolWorkloadDiagnose, computeagent.ToolReasonExplain, computeagent.ToolInstanceTypesList,
		computeagent.ToolWorkloadRender,
	},
	"dns": {
		dnsagent.ToolZonesList, dnsagent.ToolZonesGet, dnsagent.ToolZoneDiagnose, dnsagent.ToolDelegationCheck,
		dnsagent.ToolRecordsList, dnsagent.ToolRecordsGet, dnsagent.ToolRecordDiagnose, dnsagent.ToolRecordRender,
		dnsagent.ToolZoneDiscoveryGet,
	},
	"activity": httpserver.ExposedTools,
}

func TestEveryAgentToolsetHasItsToolsListed(t *testing.T) {
	for _, ts := range agentToolsets {
		if _, ok := agentToolNames[ts.Name]; !ok {
			t.Errorf("toolset %q: add its tools to agentToolNames", ts.Name)
		}
	}
}

func TestAgentToolsRegisteredReadOnly(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	for _, ts := range agentToolsets {
		for _, name := range append([]string{ts.guideToolName()}, agentToolNames[ts.Name]...) {
			tool, ok := tools[name]
			if !ok {
				t.Errorf("%s: expected tool %q to be registered", ts.Name, name)
				continue
			}
			// No service publishes a mutating agent tool, and no guide writes.
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Errorf("%s: expected %q to be annotated read-only", ts.Name, name)
			}
		}
	}
}

// AddTool replaces on a name collision rather than failing, so a service tool
// that shadowed one of this server's own would silently remove it.
func TestAgentToolsShadowNoOtherTool(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	for _, r := range crudResources {
		if _, ok := tools[r.Tool]; !ok {
			t.Errorf("expected %q to survive the agent toolsets", r.Tool)
		}
	}
	for _, name := range []string{"resource", "activity", "context", "organizations", "projects", "users", "apis"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected %q to survive the agent toolsets", name)
		}
	}
	seen := map[string]string{}
	for _, ts := range agentToolsets {
		for _, name := range append([]string{ts.guideToolName()}, agentToolNames[ts.Name]...) {
			if other, ok := seen[name]; ok {
				t.Errorf("tool %q is registered by both %s and %s", name, other, ts.Name)
			}
			seen[name] = ts.Name
		}
	}
}

func TestEachAgentToolsetCanBeDisabled(t *testing.T) {
	for _, ts := range agentToolsets {
		t.Run(ts.Name, func(t *testing.T) {
			t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", ts.Name)
			cs := connect(t)
			tools := listedTools(t, cs)
			for _, name := range append([]string{ts.guideToolName()}, agentToolNames[ts.Name]...) {
				if _, ok := tools[name]; ok {
					t.Errorf("expected %q to be gated off when %s is disabled", name, ts.Name)
				}
			}
			for name := range listedPrompts(t, cs) {
				if strings.HasPrefix(name, ts.Name+"-") {
					t.Errorf("expected prompt %q to be gated off when %s is disabled", name, ts.Name)
				}
			}
			// Every other toolset's guide stays.
			for _, other := range agentToolsets {
				if other.Name == ts.Name {
					continue
				}
				if _, ok := tools[other.guideToolName()]; !ok {
					t.Errorf("expected %q to stay when only %s is disabled", other.guideToolName(), ts.Name)
				}
			}
		})
	}
}

func TestEveryAgentSkillIsParsedAndPrompted(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	prompts := listedPrompts(t, connect(t))
	owner := map[string]string{}
	total := 0
	for _, ts := range agentToolsets {
		skills, err := ts.skills()
		if err != nil {
			t.Fatalf("%s: skills: %v", ts.Name, err)
		}
		for name, sk := range skills {
			if sk.Title == "" {
				t.Errorf("%s skill %q: no title parsed from its heading", ts.Name, name)
			}
			// Every service opens a skill with when to use it ("Use when ...",
			// "Use for ..."); that's what the index and prompt list show.
			if !strings.HasPrefix(sk.Description, "Use ") {
				t.Errorf("%s skill %q: expected a 'Use ...' description, got %q", ts.Name, name, sk.Description)
			}
			p := ts.promptName(name)
			if _, ok := prompts[p]; !ok {
				t.Errorf("%s skill %q: expected prompt %q", ts.Name, name, p)
			}
			// AddPrompt replaces on a name collision rather than failing, so two
			// services' skills of the same name would silently lose one.
			if other, ok := owner[p]; ok {
				t.Errorf("prompt %q is claimed by both %s and %s", p, other, ts.Name)
			}
			owner[p] = ts.Name
			total++
		}
	}
	for _, existing := range []string{"deploy-http-proxy", "configure-dns", "onboard-to-project"} {
		if _, ok := prompts[existing]; !ok {
			t.Errorf("expected existing prompt %q to survive", existing)
		}
		total++
	}
	if len(prompts) != total {
		t.Errorf("expected %d prompts (every skill plus the existing ones), got %d", total, len(prompts))
	}
}

func TestEveryAgentGuideOrientsAndIndexes(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	for _, ts := range agentToolsets {
		text, isErr := guideText(t, cs, ts.guideToolName(), map[string]any{})
		if isErr {
			t.Fatalf("%s: unexpected error: %s", ts.Name, text)
		}
		if !strings.HasPrefix(text, ts.Preamble) {
			t.Errorf("%s: expected the orientation to open with the preamble", ts.Name)
		}
		if !strings.Contains(text, "Skills (load one with "+ts.guideToolName()+" skill='<name>'):") {
			t.Errorf("%s: expected the skill index", ts.Name)
		}
		skills, _ := ts.skills()
		for name := range skills {
			if !strings.Contains(text, "- "+name+": Use ") {
				t.Errorf("%s: expected %q in the skill index", ts.Name, name)
			}
		}
	}
}

// Every service's documents were written for the assistant, so any of them
// may say "plan" or "apply" - each preamble has to map those onto this
// server's write path.
func TestEveryPreambleMapsTheAssistantsTools(t *testing.T) {
	for _, ts := range agentToolsets {
		knowledge, err := fs.ReadFile(ts.Docs, ts.KnowledgeFile)
		if err != nil {
			t.Fatalf("%s: %v", ts.Name, err)
		}
		skills, err := ts.skills()
		if err != nil {
			t.Fatalf("%s: %v", ts.Name, err)
		}
		docs := string(knowledge)
		for _, sk := range skills {
			docs += sk.Body
		}
		for _, baseTool := range []string{
			"resources_plan", "resources_apply", "resources_list", "resources_get",
			"schema_get", "locations_list", "quota_get", "report_capability_gap",
		} {
			if strings.Contains(docs, baseTool) && !strings.Contains(ts.Preamble, baseTool) {
				t.Errorf("%s: its documents mention %s, but the preamble doesn't say what it is here", ts.Name, baseTool)
			}
		}
		if !strings.Contains(ts.Preamble, "ACTIVE PROJECT") {
			t.Errorf("%s: the preamble should say the tools read the active project", ts.Name)
		}
	}
}

func TestAgentToolsNeedAnActiveProjectBeforeLoggingIn(t *testing.T) {
	// With no project active, every toolset's deps must fail on that alone -
	// before EnsureAuth, which would otherwise open a browser login just to
	// report there's nothing to read.
	if _, err := resolveProjectName(context.Background(), ""); err == nil {
		t.Skip("an earlier test left an active project set; deps would go on to log in")
	}
	ctx := context.Background()
	for name, resolve := range map[string]func() error{
		"alb":      func() error { _, err := albDeps(ctx); return err },
		"network":  func() error { _, err := networkDeps(ctx); return err },
		"compute":  func() error { _, err := computeDeps(ctx); return err },
		"dns":      func() error { _, err := dnsDeps(ctx); return err },
		"activity": func() error { _, err := activityClient(ctx); return err },
	} {
		if err := resolve(); err == nil || !strings.Contains(err.Error(), "no active project") {
			t.Errorf("%s: expected a no-active-project error, got %v", name, err)
		}
	}
}
