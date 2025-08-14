package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type jsonrpcReq struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type jsonrpcResp struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *jsonrpcError  `json:"error,omitempty"`
	Method  string         `json:"method,omitempty"` // for notifications
	Params  map[string]any `json:"params,omitempty"` // for notifications
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var ignored = map[string]bool{
	"resources/list":            true,
	"prompts/list":              true,
	"notifications/cancelled":   true,
	"notifications/initialized": true,
}

func (s *Service) RunSTDIO(port int) {
	fmt.Fprintf(os.Stderr, "[datum-mcp] STDIO mode ready\n")
	// Optional HTTP for manual testing.
	if port > 0 {
		go func() {
			if err := ServeHTTP(s, port); err != nil {
				fmt.Fprintf(os.Stderr, "[datum-mcp] HTTP server error: %v\n", err)
			}
		}()
	}

	sc := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 10*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req jsonrpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			reply(jsonrpcResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo": map[string]any{
						"name":    "datum-mcp",
						"version": "2.2.0",
					},
					"capabilities": map[string]any{},
				},
			})
			notify("notifications/initialized", map[string]any{})
			continue

		case "tools/list":
			reply(jsonrpcResp{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": toolsList(),
				},
			})
			continue

		case "tools/call":
			name, _ := req.Params["name"].(string)
			args, _ := req.Params["arguments"].(map[string]any)
			if name == "" {
				replyErr(req.ID, -32602, "Missing tool name")
				continue
			}
			switch name {
			case "datum_list_crds":
				res := s.ListCRDs()
				replyToolOK(req.ID, res)

			case "datum_skeleton_crd":
				// Hidden from tools/list, still callable by name
				var r SkeletonReq
				if args != nil {
					r.APIVersion, _ = args["apiVersion"].(string)
					r.Kind, _ = args["kind"].(string)
				}
				resp, err := s.Skeleton(r)
				if err != nil {
					replyErr(req.ID, -32603, err.Error())
					continue
				}
				replyToolOK(req.ID, resp)

			case "datum_list_supported":
				var r ListSupReq
				if args != nil {
					r.APIVersion, _ = args["apiVersion"].(string)
					r.Kind, _ = args["kind"].(string)
				}
				resp, err := s.ListSupported(r)
				if err != nil {
					replyErr(req.ID, -32603, err.Error())
					continue
				}
				replyToolOK(req.ID, resp)

			case "datum_prune_crd":
				var r PruneReq
				if args != nil {
					r.YAML, _ = args["yaml"].(string)
				}
				resp, err := s.Prune(r)
				if err != nil {
					if bad, _ := IsUnsupportedRemoved(err); bad {
						replyErr(req.ID, -32603, err.Error())
						continue
					}
					replyErr(req.ID, -32603, err.Error())
					continue
				}
				replyToolOK(req.ID, resp)

			case "datum_validate_crd":
				var r ValReq
				if args != nil {
					r.YAML, _ = args["yaml"].(string)
				}
				resp := s.Validate(r)
				replyToolOK(req.ID, resp)

			case "datum_refresh_discovery":
				ok, count, err := s.RefreshDiscovery()
				if err != nil {
					replyErr(req.ID, -32603, err.Error())
					continue
				}
				replyToolOK(req.ID, map[string]any{"ok": ok, "count": count})

			default:
				replyErr(req.ID, -32601, fmt.Sprintf("Unknown tool %s", name))
			}
			continue
		default:
			if ignored[req.Method] {
				if req.ID != nil {
					root := strings.SplitN(req.Method, "/", 2)[0]
					reply(jsonrpcResp{
						JSONRPC: "2.0",
						ID:      req.ID,
						Result:  map[string]any{root: []any{}},
					})
				}
				continue
			}
			if req.ID != nil {
				replyErr(req.ID, -32601, "Unknown method "+req.Method)
			}
		}
	}
}

func notify(method string, params map[string]any) {
	emit(jsonrpcResp{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func reply(resp jsonrpcResp) { emit(resp) }

func replyErr(id any, code int, msg string) {
	reply(jsonrpcResp{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	})
}

func replyToolOK(id any, payload any) {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		b, _ = json.Marshal(payload)
	}
	reply(jsonrpcResp{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": string(b),
				},
			},
		},
	})
}

func emit(resp jsonrpcResp) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(resp)
}

func toolsList() []map[string]any {
	// Skeleton tool is ALWAYS hidden here.
	return []map[string]any{
		{
			"name":        "datum_list_crds",
			"description": "List all apiVersion/kind pairs known to the control plane.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		},
		{
			"name":        "datum_list_supported",
			"description": "List legal field paths (prefers spec.* when present; otherwise top-level fields).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"apiVersion": map[string]any{"type": "string"},
					"kind":       map[string]any{"type": "string"},
				},
				"required": []any{"apiVersion", "kind"},
			},
		},
		{
			"name":        "datum_prune_crd",
			"description": "Strip unsupported fields (422 if any were removed).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"yaml": map[string]any{"type": "string"},
				},
				"required": []any{"yaml"},
			},
		},
		{
			"name":        "datum_validate_crd",
			"description": "Validate with Kubernetes server dry-run (Strict field validation).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"yaml": map[string]any{"type": "string"},
				},
				"required": []any{"yaml"},
			},
		},
		{
			"name":        "datum_refresh_discovery",
			"description": "Refresh the OpenAPI discovery cache.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}},
		},
	}
}
