package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/version"
)

// TunnelProtocol is the Upgrade token the panel opens a database tunnel for.
// internal/api has the same constant; a test there checks they agree.
const TunnelProtocol = "skifity-db-tunnel"

// databaseCredentials is the panel's answer to .../credentials.
type databaseCredentials struct {
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	URL      string `json:"url"`
}

// cmdDB works with an environment's managed databases.
//
//	skifity db                         list them
//	skifity db connect                 a local port that reaches the only one
//	skifity db connect orders          ...or the one called orders
//	skifity db connect orders --port 6543
func cmdDB(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("db", flag.ContinueOnError)
	flags.SetOutput(out)
	envID := flags.String("env", "", "the environment id")
	port := flags.Int("port", 0, "the local port to listen on; by default the database's own, or any free one")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	environment := *envID
	if environment == "" {
		if environment, err = resolveEnvironment(ctx, client, cfg); err != nil {
			return err
		}
	}

	switch action {
	case "list", "ls":
		var response struct {
			Items []store.Database `json:"items"`
		}
		if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/databases", nil, &response); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, response.Items)
		}
		if len(response.Items) == 0 {
			fmt.Fprintln(out, "No databases in this environment. Add one in the panel, under Databases.")
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "NAME\tID\tENGINE\tSTATUS")
		for _, database := range response.Items {
			fmt.Fprintf(table, "%s\t%s\t%s %s\t%s\n", database.Name, database.ID,
				database.Engine, database.EngineVersion, database.Status)
		}
		return table.Flush()
	case "connect":
		named := ""
		if len(positional) > 0 {
			named = positional[0]
		}
		database, err := findDatabase(ctx, client, environment, named)
		if err != nil {
			return err
		}
		return connectDatabase(ctx, client, database, *port, *asJSON, out)
	default:
		return errdoc.BadRequest(fmt.Sprintf(
			"Say list, or connect and a database's name: `%s db connect orders`.", version.Binary))
	}
}

// findDatabase picks a database by id, slug or name, or the only one there
// is when none is named.
func findDatabase(ctx context.Context, client *Client, environment, named string) (store.Database, error) {
	var response struct {
		Items []store.Database `json:"items"`
	}
	if err := client.Do(ctx, "GET", "/api/environments/"+environment+"/databases", nil, &response); err != nil {
		return store.Database{}, err
	}
	names := make([]string, 0, len(response.Items))
	for _, database := range response.Items {
		if named != "" && (database.ID == named || database.Slug == named || strings.EqualFold(database.Name, named)) {
			return database, nil
		}
		names = append(names, database.Name)
	}
	if named == "" && len(response.Items) == 1 {
		return response.Items[0], nil
	}
	if named != "" {
		// An id from another environment still works: the panel decides
		// whether this token may see it.
		var database store.Database
		if err := client.Do(ctx, "GET", "/api/databases/"+url.PathEscape(named), nil, &database); err == nil {
			return database, nil
		}
	}
	if len(names) == 0 {
		return store.Database{}, errdoc.New("cli.no_database", "There is no database here").
			WithCause("This environment has no managed databases.").
			WithImpact("Nothing was connected.").
			WithFix("Add one in the panel under Databases, or pass --env for another environment.")
	}
	if named == "" {
		return store.Database{}, errdoc.New("cli.ambiguous_database", "Which database did you mean?").
			WithCause("This environment has several databases.").
			WithImpact("Nothing was connected.").
			WithFix("Name one: %s", strings.Join(names, ", "))
	}
	return store.Database{}, errdoc.New("cli.unknown_database", "There is no database by that name").
		WithCause("Nothing in this environment is called %q.", named).
		WithImpact("Nothing was connected.").
		WithFix("Name one of: %s", strings.Join(names, ", "))
}

// connectDatabase listens on a local port and tunnels every connection made
// to it through the panel, until the command is stopped.
func connectDatabase(ctx context.Context, client *Client, database store.Database, port int, asJSON bool, out io.Writer) error {
	var credentials databaseCredentials
	if err := client.Do(ctx, "GET", "/api/databases/"+database.ID+"/credentials", nil, &credentials); err != nil {
		return err
	}
	listener, err := listenLocally(ctx, port, credentials.Port)
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	local := localURL(credentials.URL, address)

	if asJSON {
		if err := writeJSON(out, map[string]any{
			"database": database.Name, "engine": credentials.Engine,
			"address": address, "url": local,
		}); err != nil {
			_ = listener.Close()
			return err
		}
	} else {
		fmt.Fprintf(out, "%s (%s) is reachable on %s.\n", database.Name, credentials.Engine, address)
		if local != "" {
			fmt.Fprintf(out, "Connect with: %s\n", local)
		}
		fmt.Fprintln(out, "Every connection goes through the panel and is in the team's activity log. Ctrl-C closes the tunnel.")
	}
	return serveTunnel(ctx, client, database.ID, listener, out)
}

// listenLocally listens on 127.0.0.1 only: a tunnel is for the person who
// opened it, not for whoever shares their network. A port asked for is that
// port or an error; otherwise the database's own port, so a client's defaults
// work, and any free one when something already has it.
func listenLocally(ctx context.Context, asked, native int) (net.Listener, error) {
	var config net.ListenConfig
	if asked > 0 {
		listener, err := config.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(asked)))
		if err != nil {
			return nil, errdoc.New("cli.port_in_use", "That port is taken").
				WithCause("Port %d on this computer could not be listened on: %v.", asked, err).
				WithImpact("Nothing was connected.").
				WithFix("Pass another --port, or leave it out to take any free one.")
		}
		return listener, nil
	}
	if native > 0 {
		if listener, err := config.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(native))); err == nil {
			return listener, nil
		}
	}
	listener, err := config.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on this computer: %w", err)
	}
	return listener, nil
}

// localURL is the database's connection string with its host swapped for the
// tunnel's local address, or "" when there is no connection string.
func localURL(connection, address string) string {
	parsed, err := url.Parse(connection)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Host = address
	return parsed.String()
}

// serveTunnel accepts local connections until ctx ends, each one relayed
// through a tunnel of its own.
func serveTunnel(ctx context.Context, client *Client, databaseID string, listener net.Listener, out io.Writer) error {
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	defer func() { _ = listener.Close() }()

	var (
		wg      sync.WaitGroup
		printMu sync.Mutex
	)
	defer wg.Wait()
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept a connection: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = local.Close() }()
			defer runsafe.Recover(nil, "a database tunnel", nil)
			// Ctrl-C ends the connections still open too, rather than
			// waiting for a client that may never hang up.
			defer context.AfterFunc(ctx, func() { _ = local.Close() })()
			remote, err := client.OpenTunnel(ctx, databaseID)
			if err != nil {
				printMu.Lock()
				fmt.Fprintf(out, "A connection could not be tunnelled: %s\n", tunnelFailure(err))
				printMu.Unlock()
				return
			}
			relay(local, remote)
		}()
	}
}

// tunnelFailure is one line about why a tunnel was refused.
func tunnelFailure(err error) string {
	var problem *errdoc.Problem
	if errors.As(err, &problem) {
		if problem.Fix != "" {
			return problem.Title + ". " + problem.Fix
		}
		return problem.Title
	}
	return err.Error()
}

// relay copies both ways until either side is done, then closes both.
func relay(local net.Conn, remote io.ReadWriteCloser) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = local.Close()
			_ = remote.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeBoth()
		defer runsafe.Recover(nil, "a database tunnel, towards the panel", nil)
		_, _ = io.Copy(remote, local)
	}()
	go func() {
		defer wg.Done()
		defer closeBoth()
		defer runsafe.Recover(nil, "a database tunnel, towards the client", nil)
		_, _ = io.Copy(local, remote)
	}()
	wg.Wait()
}

// OpenTunnel asks the panel for a tunnel to a database and returns the
// connection it becomes: what is written reaches the database, and what the
// database says can be read.
func (c *Client) OpenTunnel(ctx context.Context, databaseID string) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/databases/"+url.PathEscape(databaseID)+"/tunnel", nil)
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", TunnelProtocol)
	req.Header.Set("User-Agent", version.UserAgent())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.tunnelClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the panel at %s: %w", c.baseURL, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, decodeProblem(resp)
		}
		return nil, errdoc.New("cli.tunnel_refused", "The panel did not open a tunnel").
			WithCause("It answered %s to a request for one.", resp.Status).
			WithImpact("This connection was closed.").
			WithFix("Something between here and the panel may not pass upgraded connections through; a panel reached directly does.")
	}
	conn, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		_ = resp.Body.Close()
		return nil, errors.New("the tunnel's connection cannot be written to")
	}
	return conn, nil
}

// tunnelClient is an HTTP/1.1 client with no deadline but the context's:
// HTTP/2 has no upgrades, and a tunnel lasts as long as whoever is using it.
func (c *Client) tunnelClient() *http.Client {
	if c.transport != nil {
		return &http.Client{Transport: c.transport}
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		// Setting this is also what keeps the transport from offering h2.
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
}
