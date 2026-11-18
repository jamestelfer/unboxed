package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	"github.com/jamestelfer/unboxed/internal/schoolbox"
	"github.com/jamestelfer/unboxed/internal/server"
)

// mcpCommand serves the read-only Schoolbox tools over stdio. Configuration is
// environment only: JWT is a Schoolbox bearer token, SCHOOLBOX_URL the site root.
func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "serve Schoolbox tools to an MCP client over stdio (reads JWT and SCHOOLBOX_URL)",
		ArgsUsage: " ",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Present() {
				return cli.Exit(fmt.Sprintf("mcp takes no arguments, got %q", cmd.Args().Slice()), exitUsage)
			}
			token, baseURL := os.Getenv("JWT"), os.Getenv("SCHOOLBOX_URL")
			if token == "" || baseURL == "" {
				return cli.Exit("JWT and SCHOOLBOX_URL must be set", exitUsage)
			}
			client, err := schoolbox.New(baseURL, token)
			if err != nil {
				return cli.Exit(err.Error(), exitUsage)
			}
			srv, err := server.New(server.Config{Client: client, BaseURL: baseURL})
			if err != nil {
				return err
			}
			if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("serve: %w", err)
			}
			return nil
		},
	}
}
