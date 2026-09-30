// Command skifity is the panel server, the command line tool and the MCP
// server, in one binary.
//
// One artifact means one thing to build, sign, ship and upgrade, and the three
// roles share the API client and the error formatting, so a failure reads the
// same in the panel, in a terminal and in an AI assistant.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"skifity/internal/cli"
	"skifity/internal/config"
	"skifity/internal/guard"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/mcpserver"
	"skifity/internal/serverapp"
	"skifity/internal/version"
	"skifity/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if len(args) == 0 {
		cli.Run(ctx, nil, os.Stdout, os.Stderr)
		return
	}

	switch args[0] {
	case "server":
		if err := runServer(ctx, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "\n%s: %s\n\n", version.Binary, err)
			os.Exit(1)
		}

	case "mcp":
		if err := runMCP(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "\n%s mcp: %s\n\n", version.Binary, err)
			os.Exit(1)
		}

	case "edge-guard":
		if err := runGuard(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "\n%s edge-guard: %s\n\n", version.Binary, err)
			os.Exit(1)
		}

	default:
		os.Exit(cli.Run(ctx, args, os.Stdout, os.Stderr))
	}
}

// runServer starts the panel.
func runServer(ctx context.Context, args []string) error {
	configPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			}
		case "--help", "-h":
			fmt.Printf(`%s server - run the panel

Usage:
  %s server [--config <path>]

Configuration is read from the file, then from the environment. Every
%s_ environment variable overrides the matching file setting.

  SKIFITY_LISTEN            address to bind, default :8080
  SKIFITY_DATABASE_PATH     where the panel's database lives
  SKIFITY_MASTER_KEY_PATH   where the master encryption key lives
  SKIFITY_KUBECONFIG        empty inside a cluster, a path outside one
  SKIFITY_NAMESPACE         the namespace the panel runs in
  SKIFITY_PUBLIC_URL        how users reach the panel
  SKIFITY_LOG_LEVEL         debug, info, warn or error
  SKIFITY_DEV_MODE          serve the frontend from Vite instead of the binary

Everything else is configured in the panel itself, at runtime.
`, version.Binary, version.Binary, "SKIFITY")
			return nil
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	return serverapp.Run(ctx, cfg, web.Handler(cfg.DevMode, cfg.DevFrontendURL))
}

// runMCP starts the MCP server over stdin and stdout.
func runMCP(ctx context.Context) error {
	cfg, err := cli.LoadConfig()
	if err != nil {
		return fmt.Errorf("%w\n\nAn MCP server needs a token. Run `%s login` first, "+
			"or set SKIFITY_CONFIG to a config file that has one", err, version.Binary)
	}
	return mcpserver.New(cfg).Run(ctx)
}

// runGuard starts the request firewall.
//
// It is not a command anybody types: the panel puts it in a Deployment, and the
// environment it reads is set there. It is in this binary rather than a second
// one because there is one artifact to build, sign and ship — ADR-0011 — and
// because a guard that could drift from the panel's own rule format would be a
// firewall that stops matching after an upgrade.
//
// Nothing here talks to the panel, the database or the Kubernetes API. The
// rules arrive as a file.
func runGuard(ctx context.Context) error {
	rules := os.Getenv("SKIFITY_GUARD_RULES")
	if rules == "" {
		return fmt.Errorf("SKIFITY_GUARD_RULES is not set, so there are no rules to enforce")
	}
	address := os.Getenv("SKIFITY_GUARD_ADDRESS")
	if address == "" {
		address = ":9000"
	}
	data := os.Getenv("SKIFITY_GUARD_DATA")
	if data == "" {
		data = os.TempDir()
	}

	g := guard.New(guard.Options{
		ConfigPath:  rules,
		DataDir:     data,
		PodCIDR:     envOr("SKIFITY_POD_CIDR", kube.PodCIDR),
		ServiceCIDR: envOr("SKIFITY_SERVICE_CIDR", kube.ServiceCIDR),
		Log:         logging.New(os.Stdout, os.Getenv("SKIFITY_LOG_FORMAT"), os.Getenv("SKIFITY_LOG_LEVEL")),
	})
	defer g.Close()
	return g.Run(ctx, address)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
