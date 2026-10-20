package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolsetLoader registers optional toolsets, at startup and mid-session. The
// SDK announces every tool added to a running server to connected clients
// with notifications/tools/list_changed, so a toolset that isn't loaded at
// startup costs an agent one 'toolsets' call rather than a restart.
//
// In http mode one server serves every session, so a toolset one session
// loads is loaded for all of them.
type toolsetLoader struct {
	s   *mcp.Server
	sel toolsetSelection

	mu     sync.Mutex
	loaded map[string]bool

	ownersOnce sync.Once
	owners     map[string]string
}

func newToolsetLoader(s *mcp.Server, sel toolsetSelection) *toolsetLoader {
	return &toolsetLoader{s: s, sel: sel, loaded: map[string]bool{}}
}

func (l *toolsetLoader) isLoaded(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return name == "" || l.loaded[name]
}

// load registers one toolset's tools, guide and skill prompts, and reports
// whether it wasn't loaded already. The caller has checked it's loadable.
func (l *toolsetLoader) load(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded[name] {
		return false
	}
	l.loaded[name] = true

	for _, r := range crudResources {
		if r.Toolset == name {
			r.register(l.s)
		}
	}
	switch name {
	case "resource":
		resourceDestructive := true
		mcp.AddTool(l.s, &mcp.Tool{
			Name:        "resource",
			Description: resourceToolDescription,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &resourceDestructive, IdempotentHint: false},
		}, toolResource)
	case "activity":
		activityDestructive := true
		mcp.AddTool(l.s, &mcp.Tool{
			Name:        "activity",
			Description: activityToolDescription,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &activityDestructive, IdempotentHint: false},
		}, toolActivity)
	}
	for _, t := range agentToolsets {
		if t.Name != name {
			continue
		}
		// Only fails if a service shipped without its embedded documents;
		// serve the rest of the toolset rather than none of it.
		if err := t.register(l.s, l.sel); err != nil {
			log.Printf("%s toolset: %v", t.Name, err)
		}
	}
	return true
}

// toolOwners maps every optional tool to the toolset that provides it. Agent
// toolsets register their tools through their service packages, so the only
// complete record is what a server lists once each toolset is loaded: it's
// built on a scratch server, the first time a call misses.
func toolOwners() (map[string]string, error) {
	ctx := context.Background()
	scratch := mcp.NewServer(&mcp.Implementation{Name: "datum-mcp-toolset-index", Version: Version}, nil)
	l := newToolsetLoader(scratch, toolsetSelection{enable: map[string]bool{}})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := scratch.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "datum-mcp-toolset-index", Version: Version}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()

	owners := map[string]string{}
	for _, name := range knownToolsets() {
		l.load(name)
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, t := range res.Tools {
			if _, ok := owners[t.Name]; !ok {
				owners[t.Name] = name
			}
		}
	}
	return owners, nil
}

// ownerOf is the toolset that provides tool, or "" if no optional toolset does.
func (l *toolsetLoader) ownerOf(tool string) string {
	l.ownersOnce.Do(func() {
		owners, err := toolOwners()
		if err != nil {
			log.Printf("indexing toolsets: %v", err)
		}
		l.owners = owners
	})
	return l.owners[tool]
}

// unknownToolMiddleware answers a call to a tool whose toolset isn't loaded
// with that toolset and the call that loads it. Without it the SDK's bare
// "unknown tool" leaves an agent that knows the name, from a guide or an
// earlier session, nothing to try next.
func (l *toolsetLoader) unknownToolMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil {
			return next(ctx, method, req)
		}
		name := call.Params.Name
		toolset := l.ownerOf(name)
		if toolset == "" || l.isLoaded(toolset) {
			return next(ctx, method, req)
		}
		if !l.sel.loadable(toolset) {
			return nil, &jsonrpc.Error{
				Code: jsonrpc.CodeInvalidParams,
				Message: fmt.Sprintf("unknown tool %q: it's in the %s toolset, which DATUM_MCP_DISABLE_TOOLSETS turns off on this "+
					"server, so it can't be loaded from here: tell the user, rather than working around it", name, toolset),
			}
		}
		suggestion := SuggestedAction{Tool: "toolsets", Action: "load", Args: map[string]any{"names": []string{toolset}}}
		data, _ := json.Marshal(map[string]any{"suggested_action": suggestion})
		return nil, &jsonrpc.Error{
			Code: jsonrpc.CodeInvalidParams,
			Message: fmt.Sprintf("unknown tool %q: it's in the %s toolset, which isn't loaded. Load it with "+
				"toolsets {\"action\":\"load\",\"names\":[%q]}, then call %s again", name, toolset, toolset, name),
			Data: data,
		}
	}
}

// unloaded is every toolset that could still be loaded, sorted.
func (l *toolsetLoader) unloaded() []string {
	var out []string
	for _, name := range knownToolsets() {
		if l.sel.loadable(name) && !l.isLoaded(name) {
			out = append(out, name)
		}
	}
	return out
}

// toolsetsToolDescription names what's left to load, with what each covers,
// so an agent sees there's more to this server in the one place every client
// shows it: the tool list. It's re-registered after each load.
func (l *toolsetLoader) toolsetsToolDescription() string {
	var b strings.Builder
	b.WriteString("Load more Datum Cloud tools mid-session. Only some toolsets are loaded, to keep this tool list small; ")
	if names := l.unloaded(); len(names) > 0 {
		b.WriteString("these are one call away:\n")
		for _, name := range names {
			fmt.Fprintf(&b, "- %s: %s\n", name, toolsetSummaries[name])
		}
		b.WriteString("Before telling the user something can't be done here, check whether one of these covers it. ")
	} else {
		b.WriteString("every toolset this server allows is loaded now. ")
	}
	b.WriteString("Actions: list (every toolset and whether it's loaded) | load (names: the toolsets to add; their tools " +
		"appear in your tool list once the client refreshes it).")
	return b.String()
}

func (l *toolsetLoader) registerTool() {
	notDestructive := false
	mcp.AddTool(l.s, &mcp.Tool{
		Name:        "toolsets",
		Description: l.toolsetsToolDescription(),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &notDestructive, IdempotentHint: true},
	}, l.handle)
}

type ToolsetsInput struct {
	Action string   `json:"action" jsonschema:"One of list|load."`
	Names  []string `json:"names,omitempty" jsonschema:"Toolsets to load (load only), e.g. [\"alb\", \"compute\"]."`
}

type ToolsetInfo struct {
	Name   string `json:"name"`
	Covers string `json:"covers"`
	// State is loaded, available (load it with action=load) or disabled
	// (DATUM_MCP_DISABLE_TOOLSETS: can't be loaded from here).
	State string `json:"state"`
	Guide string `json:"guide,omitempty"`
}

type ToolsetsLoadResult struct {
	Loaded        []string `json:"loaded,omitempty"`
	AlreadyLoaded []string `json:"already_loaded,omitempty"`
	NextStep      string   `json:"next_step"`
}

func guideFor(name string) string {
	for _, t := range agentToolsets {
		if t.Name == name {
			return t.guideToolName()
		}
	}
	return ""
}

func (l *toolsetLoader) handle(_ context.Context, _ *mcp.CallToolRequest, in ToolsetsInput) (*mcp.CallToolResult, any, error) {
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "list":
		var out []ToolsetInfo
		for _, name := range knownToolsets() {
			state := "available"
			switch {
			case l.isLoaded(name):
				state = "loaded"
			case !l.sel.loadable(name):
				state = "disabled"
			}
			out = append(out, ToolsetInfo{Name: name, Covers: toolsetSummaries[name], State: state, Guide: guideFor(name)})
		}
		return okResult(map[string]any{"toolsets": out})
	case "load":
		return l.handleLoad(in.Names)
	default:
		return errResult(fmt.Errorf("unknown action %q: use list or load", in.Action), &SuggestedAction{Tool: "toolsets", Action: "list"})
	}
}

func (l *toolsetLoader) handleLoad(names []string) (*mcp.CallToolResult, any, error) {
	known := map[string]bool{}
	for _, name := range knownToolsets() {
		known[name] = true
	}
	var want []string
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		switch {
		case n == "":
		case !known[n]:
			return errResult(fmt.Errorf("unknown toolset %q; known toolsets: %s", n, strings.Join(knownToolsets(), ", ")),
				&SuggestedAction{Tool: "toolsets", Action: "list"})
		case !l.sel.loadable(n):
			return errResult(fmt.Errorf("the %s toolset is turned off by DATUM_MCP_DISABLE_TOOLSETS and can't be loaded from here: "+
				"tell the user, rather than working around it", n), nil)
		default:
			want = append(want, n)
		}
	}
	if len(want) == 0 {
		return errResult(fmt.Errorf("load needs names, e.g. [\"alb\"]"), &SuggestedAction{Tool: "toolsets", Action: "list"})
	}

	var res ToolsetsLoadResult
	var guides []string
	for _, n := range want {
		if l.load(n) {
			res.Loaded = append(res.Loaded, n)
		} else {
			res.AlreadyLoaded = append(res.AlreadyLoaded, n)
		}
		if g := guideFor(n); g != "" {
			guides = append(guides, g)
		}
	}
	if len(res.Loaded) > 0 {
		l.registerTool()
		registerPrompts(l.s, l.isLoaded)
	}

	var all []string
	for _, name := range knownToolsets() {
		if l.isLoaded(name) {
			all = append(all, name)
		}
	}
	sort.Strings(guides)
	res.NextStep = "Your client has been told its tool list changed; the new tools appear once it refreshes."
	if len(guides) > 0 {
		res.NextStep += " Start with " + strings.Join(guides, ", ") + "."
	}
	res.NextStep += " If the tools don't appear, the client doesn't pick up tool list changes: ask the user to set " +
		"DATUM_MCP_ENABLE_TOOLSETS=" + strings.Join(all, ",") + " and restart datum-mcp."
	return okResult(res)
}

// PrintToolsets writes every toolset, whether it's loaded at startup under the
// current environment, and what it covers: the --list-toolsets output.
func PrintToolsets(w io.Writer) error {
	sel := selectedToolsets()
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOLSET\tAT STARTUP\tCOVERS")
	fmt.Fprintln(tw, "core\tloaded\torganizations, projects, users, domains, apis, context, toolsets (always on)")
	for _, name := range knownToolsets() {
		state := "available"
		switch {
		case sel.on(name):
			state = "loaded"
		case !sel.loadable(name):
			state = "disabled"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, state, toolsetSummaries[name])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\nAvailable toolsets load mid-session through the 'toolsets' tool. Set DATUM_MCP_ENABLE_TOOLSETS "+
		"(comma-separated, or \"all\"; default %s) for what loads at startup, and DATUM_MCP_DISABLE_TOOLSETS for what "+
		"can't be loaded at all.\n", strings.Join(defaultToolsets, ","))
	return err
}
