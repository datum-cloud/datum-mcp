package server

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SuggestedAction tells an agent exactly which tool call is likely to resolve
// the error it just received, instead of making it parse prose to figure out
// the recovery step itself.
type SuggestedAction struct {
	Tool   string         `json:"tool"`
	Action string         `json:"action,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
}

// ToolError is the structured JSON body returned for every tool failure.
type ToolError struct {
	Error           string           `json:"error"`
	SuggestedAction *SuggestedAction `json:"suggested_action,omitempty"`
}

// errResult builds a tool-error CallToolResult carrying a structured JSON
// body. It always returns a nil error: the typed mcp.AddTool handler wrapper
// discards CallToolResult.Content and replaces it with the plain err.Error()
// string whenever a non-nil error is returned (see ToolHandlerFor), so a
// structured body can only survive by going through the result itself.
func errResult(err error, suggestion *SuggestedAction) (*mcp.CallToolResult, any, error) {
	te := ToolError{Error: err.Error(), SuggestedAction: suggestion}
	b, _ := json.MarshalIndent(te, "", "  ")
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, te, nil
}

// okResult builds a successful CallToolResult from any JSON-marshalable value.
func okResult(v any) (*mcp.CallToolResult, any, error) {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, v, nil
}

// suggestListOrgs is the recovery action for "no active organization" errors:
// an agent rarely knows a valid org id up front, so the safe first step is to
// list memberships rather than guess a name to pass to 'set'.
func suggestListOrgs() *SuggestedAction {
	return &SuggestedAction{Tool: "organizations", Action: "list"}
}

// suggestListProjects is the recovery action for "no active project" errors,
// for the same reason as suggestListOrgs.
func suggestListProjects() *SuggestedAction {
	return &SuggestedAction{Tool: "projects", Action: "list"}
}

// suggestActions points an agent at the valid action set for a tool after an
// "unsupported action" error.
func suggestActions(tool, actions string) *SuggestedAction {
	return &SuggestedAction{Tool: tool, Action: actions}
}

// suggestGet is the recovery action for a resourceVersion conflict on
// update/delete: re-fetch the resource to get its current resourceVersion
// and retry with that.
func suggestGet(tool, id string) *SuggestedAction {
	return &SuggestedAction{Tool: tool, Action: "get", Args: map[string]any{"id": id}}
}
