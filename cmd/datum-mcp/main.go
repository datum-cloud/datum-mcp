package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/server"
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	var mode string
	var host string
	var port int
	var allowNonLoopback bool

	cmd := &cobra.Command{
		Use:   "datum-mcp",
		Short: "Datum MCP server",
		// main reports the error itself, once.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Flags parsed fine, so usage won't help with anything that
			// fails from here on.
			cmd.SilenceUsage = true
			if err := checkHeadlessConfig(); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			switch mode {
			case "stdio":
				return server.Run(ctx)
			case "http":
				return server.RunHTTP(ctx, host, port, allowNonLoopback)
			default:
				return fmt.Errorf("unknown mode: %s", mode)
			}
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "stdio", "transport mode: stdio | http")
	cmd.Flags().StringVar(&host, "host", "localhost", "http host")
	cmd.Flags().IntVar(&port, "port", 8000, "http port")
	cmd.Flags().BoolVar(&allowNonLoopback, "allow-non-loopback", false,
		"allow http mode to bind a non-loopback host (DANGEROUS: this transport has no authentication or Origin check of its own)")

	return cmd
}

// checkHeadlessConfig fails startup when DATUM_TOKEN is set but the API
// hostname can't be found, instead of starting a server whose every tool call
// would fail the same way.
func checkHeadlessConfig() error {
	if os.Getenv("DATUM_TOKEN") == "" {
		return nil
	}
	_, err := authutil.GetAPIHostname()
	return err
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		log.Fatal(err)
	}
}
