package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/datum-cloud/datum-mcp/internal/discovery"
	"github.com/datum-cloud/datum-mcp/internal/mcp"
)

func main() {
	var port int
	flag.IntVar(&port, "port", 0, "Run HTTP server for manual testing on this port")
	flag.Parse()

	disc := discovery.New()
	disc.OpenAPIBase = os.Getenv("DATUM_OPENAPI_BASE")
	disc.BearerToken = os.Getenv("DATUM_ACCESS_TOKEN")

	// Initialize discovery (OpenAPI if configured, plus GitHub CRDs).
	if err := disc.Refresh(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "[datum-mcp] discovery init failed: %v\n", err)
	}

	svc := mcp.NewService(disc)
	// Run the MCP JSON-RPC bridge over STDIO; optional HTTP if --port > 0.
	svc.RunSTDIO(port)
}
