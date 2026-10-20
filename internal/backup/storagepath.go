package backup

import (
	"context"
	"net"
	"strconv"

	"skifity/internal/kube"
)

// lookupHost resolves a bucket's host. A variable so a test can say what a
// name resolves to without a resolver.
var lookupHost = func(ctx context.Context, host string) ([]string, error) {
	if net.ParseIP(host) != nil {
		return []string{host}, nil
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

// resolveOrNothing is what a bucket's host resolves to here, or nothing. A
// name that does not resolve is not this step's failure to report: the job
// resolves the same name a moment later, and says so in its own log, where
// whoever reads the failed backup is looking.
func resolveOrNothing(ctx context.Context, host string) []string {
	addresses, err := lookupHost(ctx, host)
	if err != nil {
		return nil
	}
	return addresses
}

// Address is the host a backup job reaches the bucket at, and the port: the
// endpoint's own, or the scheme's when it names none.
func (s *Storage) Address() (string, int, error) {
	hostPort, secure, err := splitEndpoint(s.endpoint)
	if err != nil {
		return "", 0, err
	}
	host, portText, err := net.SplitHostPort(hostPort)
	if err != nil {
		if secure {
			return hostPort, 443, nil
		}
		return hostPort, 80, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, &net.AddrError{Err: "not a port", Addr: hostPort}
	}
	return host, port, nil
}

// openPathToStorage lets the backup and restore jobs in a namespace reach the
// bucket, when it is somewhere the environment's own policy keeps pods away
// from: on a server of this cluster, at home, on the office network, over a
// VPN. Done before every job rather than kept in step on a timer, so the bucket
// a job is handed is one it can reach, however recently the setting changed.
func openPathToStorage(ctx context.Context, client *kube.Client, namespace string, storage *Storage) error {
	host, port, err := storage.Address()
	if err != nil {
		return err
	}
	resolved := resolveOrNothing(ctx, host)
	if len(resolved) == 0 {
		return nil
	}
	nodes, err := client.NodeAddresses(ctx)
	if err != nil {
		return err
	}
	return client.AllowBackupStorage(ctx, namespace, kube.StorageAddresses(resolved, nodes), port)
}
