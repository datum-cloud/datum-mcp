package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// coreTools is everything registered with every optional toolset off.
var coreTools = []string{"apis", "context", "domains", "organizations", "projects", "toolsets", "users"}

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
			t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "all")
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
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "all")
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
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "all")
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

func TestDefaultLoadsOnlyCoreAndTheDefaultToolsets(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	for _, name := range append([]string{"dnszones", "dns_guide", "networkservices", "network_guide", "httpproxies", "alb_guide"}, coreTools...) {
		if tools[name] == nil {
			t.Errorf("expected %q loaded by default", name)
		}
	}
	for _, name := range []string{"workloads", "compute_guide", "resource", "activity", "ipclasses"} {
		if tools[name] != nil {
			t.Errorf("expected %q not loaded by default", name)
		}
	}
	desc := tools["toolsets"].Description
	for _, name := range []string{"compute", "activity", "resource"} {
		if !strings.Contains(desc, "- "+name+": ") {
			t.Errorf("expected the toolsets tool to list %q as loadable, got:\n%s", name, desc)
		}
	}
	if strings.Contains(desc, "- dns: ") || strings.Contains(desc, "- alb: ") {
		t.Errorf("expected the toolsets tool not to list dns or alb, which are loaded")
	}
	if listedPrompts(t, connect(t))["deploy-http-proxy"] == nil {
		t.Errorf("expected deploy-http-proxy by default: alb and network are both loaded")
	}
}

// connectNotified is connect with a client that counts tools/list_changed
// notifications.
func connectNotified(t *testing.T) (*mcp.ClientSession, chan struct{}) {
	t.Helper()
	ctx := context.Background()
	changed := make(chan struct{}, 8)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := NewMCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
	}).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, changed
}

func TestLoadingAToolsetMidSessionAddsItsToolsAndTellsTheClient(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "dns,network")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs, changed := connectNotified(t)
	if listedPrompts(t, cs)["deploy-http-proxy"] != nil {
		t.Fatalf("expected deploy-http-proxy gated off before alb is loaded")
	}

	text, isErr := guideText(t, cs, "toolsets", map[string]any{"action": "load", "names": []string{"ALB", "dns"}})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var res ToolsetsLoadResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Loaded, []string{"alb"}) || !reflect.DeepEqual(res.AlreadyLoaded, []string{"dns"}) {
		t.Errorf("expected alb loaded and dns already loaded, got %+v", res)
	}
	if !strings.Contains(res.NextStep, "alb_guide") || !strings.Contains(res.NextStep, "DATUM_MCP_ENABLE_TOOLSETS=alb,dns,network") {
		t.Errorf("expected next_step to point at alb_guide and the restart fallback, got %q", res.NextStep)
	}

	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a tools/list_changed notification")
	}
	tools := listedTools(t, cs)
	for _, name := range append([]string{"httpproxies", "gateways"}, albToolNames...) {
		if tools[name] == nil {
			t.Errorf("expected %q after loading alb", name)
		}
	}
	if strings.Contains(tools["toolsets"].Description, "- alb: ") {
		t.Errorf("expected the toolsets tool to stop listing alb once it's loaded")
	}
	if listedPrompts(t, cs)["deploy-http-proxy"] == nil {
		t.Errorf("expected deploy-http-proxy once alb and network are both loaded")
	}
}

func TestDisabledToolsetsCantBeLoaded(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "billing")
	cs := connect(t)
	text, isErr := guideText(t, cs, "toolsets", map[string]any{"action": "load", "names": []string{"iam", "billing"}})
	if !isErr || !strings.Contains(text, "DATUM_MCP_DISABLE_TOOLSETS") {
		t.Errorf("expected loading billing to fail naming DATUM_MCP_DISABLE_TOOLSETS, got: %s", text)
	}
	if tools := listedTools(t, cs); tools["billingaccounts"] != nil || tools["roles"] != nil {
		t.Errorf("expected a refused load to load nothing, iam included")
	}
	if strings.Contains(listedTools(t, cs)["toolsets"].Description, "- billing: ") {
		t.Errorf("expected the toolsets tool not to offer billing")
	}

	text, _ = guideText(t, cs, "toolsets", map[string]any{"action": "list"})
	var list struct {
		Toolsets []ToolsetInfo `json:"toolsets"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, ts := range list.Toolsets {
		states[ts.Name] = ts.State
	}
	if states["billing"] != "disabled" || states["dns"] != "loaded" || states["compute"] != "available" {
		t.Errorf("expected billing disabled, dns loaded, compute available, got %v", states)
	}
}

func TestLoadingAnUnknownToolsetNamesTheKnownOnes(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	text, isErr := guideText(t, connect(t), "toolsets", map[string]any{"action": "load", "names": []string{"dnz"}})
	if !isErr || !strings.Contains(text, "known toolsets: activity, alb") {
		t.Errorf("expected an unknown-toolset error listing the known ones, got: %s", text)
	}
}

func TestEnableAllLoadsEverything(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "all")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	sel := selectedToolsets()
	for _, name := range knownToolsets() {
		if !sel.on(name) {
			t.Errorf("expected %q on with all", name)
		}
	}
	if len(sel.unknown()) != 0 {
		t.Errorf("expected all not to be reported as unknown, got %v", sel.unknown())
	}
}

// defaultVisibleBudget is the model-visible tool list with the default
// toolsets: what a fresh client pays before its first call.
const defaultVisibleBudget = 48_000

func TestDefaultToolListStaysWithinBudget(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	tools := listedTools(t, connect(t))
	visible := 0
	for _, tool := range tools {
		in, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		visible += len(tool.Name) + len(tool.Description) + len(in)
	}
	t.Logf("default tools/list: %d tools, %d bytes model-visible (~%dK tokens)", len(tools), visible, visible*10/39/1000)
	if visible > defaultVisibleBudget {
		t.Errorf("default model-visible tool list is %d bytes, over the %d budget: %s", visible, defaultVisibleBudget, budgetHint)
	}
}

// callErr calls a tool expecting a JSON-RPC error rather than a tool result.
func callErr(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *jsonrpc.Error {
	t.Helper()
	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected a JSON-RPC error calling %s, got %v", tool, err)
	}
	return rpcErr
}

func TestCallingAnUnloadedToolPointsAtItsToolset(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)

	for tool, toolset := range map[string]string{
		"networks":                   "vpc",
		"compute_guide":              "compute",
		agentToolNames["compute"][0]: "compute",
		"resource":                   "resource",
	} {
		e := callErr(t, cs, tool, map[string]any{"action": "list"})
		want := `toolsets {"action":"load","names":["` + toolset + `"]}`
		if e.Code != jsonrpc.CodeInvalidParams || !strings.Contains(e.Message, want) {
			t.Errorf("%s: expected -32602 pointing at %s, got %d %q", tool, want, e.Code, e.Message)
		}
		var data struct {
			SuggestedAction SuggestedAction `json:"suggested_action"`
		}
		if err := json.Unmarshal(e.Data, &data); err != nil || data.SuggestedAction.Tool != "toolsets" || data.SuggestedAction.Action != "load" {
			t.Errorf("%s: expected a toolsets load suggested_action in data, got %s", tool, e.Data)
		}
	}

	if e := callErr(t, cs, "no_such_tool", nil); e.Message != `unknown tool "no_such_tool"` {
		t.Errorf("expected the plain SDK error for a tool no toolset provides, got %q", e.Message)
	}
}

func TestCallingAToolAfterLoadingItsToolsetReachesIt(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	cs := connect(t)
	callErr(t, cs, "networks", map[string]any{"action": "list"})
	if text, isErr := guideText(t, cs, "toolsets", map[string]any{"action": "load", "names": []string{"vpc"}}); isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	text, isErr := guideText(t, cs, "networks", map[string]any{"action": "explode"})
	if !isErr || !strings.Contains(text, "explode") {
		t.Errorf("expected the networks tool itself to reject the action, got %q", text)
	}
}

func TestCallingADisabledToolSaysItCantBeLoaded(t *testing.T) {
	t.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "")
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "vpc")
	e := callErr(t, connect(t), "networks", map[string]any{"action": "list"})
	if !strings.Contains(e.Message, "DATUM_MCP_DISABLE_TOOLSETS") || strings.Contains(e.Message, `"action":"load"`) || e.Data != nil {
		t.Errorf("expected a disabled-toolset error with no load suggestion, got %q (data %s)", e.Message, e.Data)
	}
}
