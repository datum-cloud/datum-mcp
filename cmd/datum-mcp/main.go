package main

import (
	"context"
	"fmt"
	"log"

	"github.com/datum-cloud/datum-mcp/internal/server"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	var mode string
	var host string
	var port int

	cmd := &cobra.Command{
		Use:   "datum-mcp",
		Short: "Datum MCP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			switch mode {
			case "stdio":
				return server.Run(ctx)
			case "http":
				addr := fmt.Sprintf("%s:%d", host, port)
				return server.RunHTTP(ctx, addr)
			default:
				return fmt.Errorf("unknown mode: %s", mode)
			}
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "stdio", "transport mode: stdio | http")
	cmd.Flags().StringVar(&host, "host", "localhost", "http host")
	cmd.Flags().IntVar(&port, "port", 8000, "http port")

	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		log.Fatal(err)
	}
}
