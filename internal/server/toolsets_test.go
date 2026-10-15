package server

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// coreTools is everything registered with every optional toolset off.
var coreTools = []string{"apis", "context", "domains", "organizations", "projects", "users"}

func toolNames(tools map[string]*mcp.Tool) []string {
	out := make([]string, 0, len(tools))
	for n := range tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestEnableToolsetsCoreOnly(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "core")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	if got := toolNames(listedTools(t, cs)); !reflect.DeepEqual(got, coreTools) {
		t.Errorf("expected only the core tools %v, got %v", coreTools, got)
	}
	if got := listedPrompts(t, cs); len(got) != 1 || got["onboard-to-project"] == nil {
		t.Errorf("expected only onboard-to-project, got %v", got)
	}
}

func TestEnableToolsetsIsAnAllowlist(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "DNS")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	tools := listedTools(t, cs)
	want := append([]string{"dnszones", "dnsrecordsets", "dnszoneclasses", "dns_guide"}, agentToolNames["dns"]...)
	for _, name := range append(want, coreTools...) {
		if tools[name] == nil {
			t.Errorf("expected %q with dns enabled", name)
		}
	}
	if len(tools) != len(want)+len(coreTools) {
		t.Errorf("expected only core and dns tools, got %v", toolNames(tools))
	}
	prompts := listedPrompts(t, cs)
	if prompts["configure-dns"] == nil {
		t.Errorf("expected configure-dns with dns enabled")
	}
	if prompts["deploy-http-proxy"] != nil {
		t.Errorf("expected deploy-http-proxy gated off without alb and network")
	}
}

func TestDisableToolsetsAppliesAfterEnable(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "dns,ipam")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "ipam")
	tools := listedTools(t, connect(t))
	if tools["ipclasses"] != nil {
		t.Errorf("expected ipclasses gated off: disable wins over enable")
	}
	if tools["dnszones"] == nil {
		t.Errorf("expected dnszones to stay")
	}
}

// A service's write tools go with its agent tools, so disabling one never
// leaves a write tool whose description points at a guide that isn't there.
func TestDisablingAServiceGatesItsWriteTools(t *testing.T) {
	cases := map[string][]string{
		"alb":     {"httpproxies", "httproutes", "gateways", "trafficprotectionpolicies"},
		"dns":     {"dnszones", "dnsrecordsets", "dnszoneclasses"},
		"network": {"networkservices"},
		"compute": {"workloads"},
	}
	for toolset, writeTools := range cases {
		t.Run(toolset, func(t *testing.T) {
			t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
			t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", toolset)
			tools := listedTools(t, connect(t))
			for _, name := range writeTools {
				if tools[name] != nil {
					t.Errorf("expected %q gated off with %s", name, toolset)
				}
			}
			if tools["domains"] == nil {
				t.Errorf("expected domains (core) to stay")
			}
		})
	}
}

func TestUnknownToolsetsAreReported(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "dns,dnz")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "billing,bogus")
	if got := selectedToolsets().unknown(); !reflect.DeepEqual(got, []string{"bogus", "dnz"}) {
		t.Errorf("expected [bogus dnz], got %v", got)
	}
}

// A field none of a tool's actions read is left out of its schema, and every
// field one of them does read is kept.
func TestCrudInputSchemasCarryOnlyTheirActionsFields(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	for _, r := range crudResources {
		t.Run(r.Tool, func(t *testing.T) {
			b, err := json.Marshal(tools[r.Tool].InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(b, &schema); err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{
				"body":            r.allows(ActionCreate) || r.allows(ActionUpdate),
				"dryRun":          r.allows(ActionCreate) || r.allows(ActionUpdate) || r.allows(ActionDelete),
				"resourceVersion": r.allows(ActionUpdate) || r.allows(ActionDelete),
				"action":          true,
				"id":              true,
				"project":         true,
				"limit":           true,
			}
			for field, present := range want {
				if _, ok := schema.Properties[field]; ok != present {
					t.Errorf("field %q: present=%v, want %v (actions %s)", field, ok, present, r.actions())
				}
			}
		})
	}
}

// The tool list is paid for in context by every session that loads this
// server, before any tool is called. These budgets exist so growth is a
// decision: if a change trips one, either trim it or raise the budget in the
// same PR and say why.
//
// visibleBudget covers what a model reads (name, description, input schema).
// wireBudget is the whole tools/list payload, including the agent tools'
// output schemas and annotations, which Claude clients don't put in context
// but which come from the service repos and so can grow on a version bump.
const (
	visibleBudget = 96_000 // bytes; ~3.9 bytes/token
	wireBudget    = 142_000
)

func TestToolListStaysWithinBudget(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	var visible, wire int
	for _, tool := range listedTools(t, connect(t)) {
		all, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		in, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		wire += len(all)
		visible += len(tool.Name) + len(tool.Description) + len(in)
	}
	t.Logf("tools/list: %d bytes model-visible (~%dK tokens), %d bytes on the wire", visible, visible*10/39/1000, wire)
	if visible > visibleBudget {
		t.Errorf("model-visible tool list is %d bytes, over the %d budget: %s", visible, visibleBudget, budgetHint)
	}
	if wire > wireBudget {
		t.Errorf("tools/list is %d bytes, over the %d budget: %s", wire, wireBudget, budgetHint)
	}
}

const budgetHint = "trim the new text (or gate it behind a toolset), or raise the budget in this PR and say why"
