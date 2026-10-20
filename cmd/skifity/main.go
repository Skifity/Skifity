// Command skifity is the panel server, the command line tool and the MCP
// server, in one binary.
//
// One artifact means one thing to build, sign, ship and upgrade, and the three
// roles share the API client and the error formatting, so a failure reads the
// same in the panel, in a terminal and in an AI assistant.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"skifity/internal/cli"
	"skifity/internal/config"
	"skifity/internal/errdoc"
	"skifity/internal/guard"
	"skifity/internal/kube"
	"skifity/internal/logging"
	"skifity/internal/mcpserver"
	"skifity/internal/netfence"
	"skifity/internal/sealed"
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
			// A failure the panel knows how to explain is printed whole: it is
			// what somebody reads in `kubectl logs` when the pod will not stay up.
			var problem *errdoc.Problem
			if errors.As(err, &problem) {
				fmt.Fprintf(os.Stderr, "\n%s: %s\n", version.Binary, problem.Text())
			} else {
				fmt.Fprintf(os.Stderr, "\n%s: %s\n\n", version.Binary, err)
			}
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

	// The first container of every pod that runs a team's code, holding the
	// rest back until the pod's network policy is in place. See
	// internal/netfence.
	case "wait-for-fence":
		os.Exit(runWaitForFence(ctx))

	// A backup job's seal and open steps: the panel's own image, sealing the
	// dump before it is uploaded and opening it after it is downloaded. The
	// passphrase arrives in the environment, from a Secret that lives as long
	// as the job.
	case "backup-seal", "backup-open":
		if err := runSeal(args[0], args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "\n%s %s: %s\n\n", version.Binary, args[0], err)
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
		Log:         logging.New(os.Stdout, os.Getenv("SKIFITY_LOG_LEVEL"), os.Getenv("SKIFITY_LOG_FORMAT")),
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

// fencePatience is how long a new pod waits for its network policy before it
// starts without it.
const fencePatience = time.Minute

// runWaitForFence waits for the pod's network policy and reports how it went.
//
// Only being stopped is a failure. Anything else — no address to test against,
// a cluster that never fences anything — is said in the pod's log and the pod
// starts: a container that could keep every pod from starting is a worse hole
// than the moment it closes.
func runWaitForFence(ctx context.Context) int {
	target, err := netfence.Target(os.Getenv("SKIFITY_HOST_IP"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s; starting without waiting for the network policy\n", err)
		return 0
	}
	result, err := netfence.Wait(ctx, target, fencePatience, nil)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "stopped while waiting for the network policy: %s\n", err)
		return 1
	case !result.Fenced:
		fmt.Fprintf(os.Stderr, "the network policy was not in place after %s, so this pod starts without it\n",
			result.Waited.Round(time.Second))
	default:
		fmt.Printf("the network policy is in place (%s)\n", result.Waited.Round(10*time.Millisecond))
	}
	return 0
}

// runSeal seals or opens one file in place.
func runSeal(command string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: %s %s <file>", version.Binary, command)
	}
	passphrase := os.Getenv("SKIFITY_BACKUP_PASSPHRASE")
	if passphrase == "" {
		return fmt.Errorf("SKIFITY_BACKUP_PASSPHRASE is not set")
	}
	if command == "backup-seal" {
		fmt.Println("==> Sealing the backup")
		return sealed.SealFile(args[0], passphrase)
	}
	fmt.Println("==> Opening the backup")
	return sealed.OpenFile(args[0], passphrase)
}
