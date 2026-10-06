package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func promptRequest(args map[string]string) *mcp.GetPromptRequest {
	return &mcp.GetPromptRequest{Params: &mcp.GetPromptParams{Arguments: args}}
}

func promptText(t *testing.T, res *mcp.GetPromptResult) string {
	t.Helper()
	if len(res.Messages) != 1 {
		t.Fatalf("expected exactly one message, got %d", len(res.Messages))
	}
	tc, ok := res.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Messages[0].Content)
	}
	return tc.Text
}

func TestDeployHTTPProxyPrompt(t *testing.T) {
	res, err := deployHTTPProxyPrompt(context.Background(), promptRequest(map[string]string{
		"backend_service": "my-service",
		"backend_port":    "8080",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := promptText(t, res)
	for _, want := range []string{"organizations", "projects", "httpproxies", "my-service", "8080", `"value": "/"`} {
		if !strings.Contains(text, want) {
			t.Errorf("expected prompt text to mention %q, got:\n%s", want, text)
		}
	}
	// The prompt must not invert the real ownership model (HTTPProxy owns
	// Gateway/HTTPRoute, not the other way around).
	if strings.Contains(text, `gateways {"action": "create"`) || strings.Contains(text, `httproutes {"action": "create"`) {
		t.Errorf("prompt should not instruct creating gateways/httproutes directly; the HTTPProxy provisions them")
	}
	// A Workload never auto-creates its backing NetworkService, including on
	// redeploy - the prompt must tell the agent to check/create one rather
	// than assume it exists.
	if !strings.Contains(text, "networkservices") {
		t.Errorf("expected prompt text to mention networkservices, got:\n%s", text)
	}
	// A custom hostname belongs in the HTTPProxy's own spec.hostnames, not a
	// hand-created DNSRecordSet, which conflicts with the controller's own
	// auto-managed record for the same name.
	if !strings.Contains(text, "spec.hostnames") {
		t.Errorf("expected prompt text to mention spec.hostnames, got:\n%s", text)
	}
	if strings.Contains(text, `dnsrecordsets {"action": "create"`) {
		t.Errorf("prompt should not instruct creating a DNSRecordSet for a custom hostname; spec.hostnames auto-manages it")
	}
}

func TestDeployHTTPProxyPromptCustomPathPrefix(t *testing.T) {
	res, err := deployHTTPProxyPrompt(context.Background(), promptRequest(map[string]string{
		"backend_service": "svc",
		"backend_port":    "80",
		"path_prefix":     "/api",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := promptText(t, res)
	if !strings.Contains(text, `"value": "/api"`) {
		t.Errorf("expected custom path_prefix to be used, got:\n%s", text)
	}
}

func TestConfigureDNSPrompt(t *testing.T) {
	res, err := configureDNSPrompt(context.Background(), promptRequest(map[string]string{
		"domain_name": "example.com",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := promptText(t, res)
	for _, want := range []string{"dnszoneclasses", "dnszones", "example.com", "dnsrecordsets"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected prompt text to mention %q, got:\n%s", want, text)
		}
	}
}

func TestOnboardToProjectPrompt(t *testing.T) {
	res, err := onboardToProjectPrompt(context.Background(), promptRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := promptText(t, res)
	for _, want := range []string{"context", "organizations", "projects", "next_step"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected prompt text to mention %q, got:\n%s", want, text)
		}
	}
}
