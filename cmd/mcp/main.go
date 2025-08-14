package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/datum-cloud/datum-mcp/internal/kube"
	"github.com/datum-cloud/datum-mcp/internal/mcp"
)

func main() {
	var (
		port     int
		context  string
		ns       string
		kubepath string
	)
	flag.IntVar(&port, "port", 0, "Run HTTP server for manual testing on this port")
	flag.StringVar(&context, "kube-context", "", "kubectl --context to use")
	flag.StringVar(&ns, "namespace", "", "Default namespace (-n) for validation")
	flag.StringVar(&kubepath, "kubectl", "kubectl", "Path to kubectl binary")
	flag.Parse()

	k := kube.New()
	k.Path = kubepath
	k.Context = context
	k.Namespace = ns

	svc := mcp.NewService(k)
	// Run the MCP JSON-RPC bridge over STDIO; optional HTTP if --port > 0.
	svc.RunSTDIO(port)

	fmt.Fprintf(os.Stderr, "[datum-mcp] exiting\n")
}
