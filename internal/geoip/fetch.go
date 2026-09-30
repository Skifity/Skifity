package geoip

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fetching the databases.
//
// DB-IP publish a new file on the first of each month and keep the old ones, so
// a URL naming a month is stable: it either exists for ever or it never
// existed. That is why the month is in the URL rather than a "latest" link — a
// download that silently returns last year's data is a firewall quietly
// deciding on facts that have moved.
//
// The first days of a month are the exception: the new file is not there yet.
// So a fetch that 404s falls back one month, once, which is the whole of the
// special case.

// MaxDatabaseBytes bounds what will be written to disk.
//
// The free files are around 10 MB. A hundred is far above anything legitimate
// and far below filling a node's disk, which is what an unbounded copy from a
// URL an operator pasted would eventually do.
const MaxDatabaseBytes = 100 << 20

// Fetcher downloads a database to a file.
type Fetcher struct {
	// Client is the HTTP client. A caller that has a guarded one — refusing
	// link-local and loopback addresses — passes it here, because the URL is
	// configurable and therefore somewhere a request can be pointed.
	Client *http.Client
	// Dir is where the files are kept.
	Dir string
}

// URLFor renders one of the default URLs for a month.
func URLFor(template string, when time.Time) string {
	return fmt.Sprintf(template, when.Format("2006-01"))
}

// Fetch downloads a database and returns the path it was written to.
//
// urlTemplate is either one of the defaults, which carry a %s for the month, or
// an operator's own URL, which is used as it stands. A file that is already
// there and current is not downloaded again: this runs on every start of a
// process that may restart often, and re-downloading 10 MB each time would be a
// component that spends its life fetching.
func (f Fetcher) Fetch(ctx context.Context, name, urlTemplate string, now time.Time) (string, error) {
	if strings.TrimSpace(urlTemplate) == "" {
		return "", nil
	}
	path := filepath.Join(f.Dir, name+".mmdb")

	// Current enough: the data changes once a month, so a file younger than a
	// week is the file that would be downloaded.
	if info, err := os.Stat(path); err == nil && now.Sub(info.ModTime()) < 7*24*time.Hour {
		return path, nil
	}

	attempts := []string{urlTemplate}
	if strings.Contains(urlTemplate, "%s") {
		// The new month's file does not exist until it is published, so the
		// first days of a month need last month's.
		attempts = []string{
			URLFor(urlTemplate, now),
			URLFor(urlTemplate, now.AddDate(0, -1, 0)),
		}
	}

	var lastErr error
	for _, url := range attempts {
		err := f.download(ctx, url, path)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if !errors.Is(err, errNotPublished) {
			break
		}
	}

	// A download that failed while a usable file is already on disk is not a
	// failure worth acting on: last month's data is far better than none, and
	// an outage at the publisher must not be an outage here.
	if _, statErr := os.Stat(path); statErr == nil {
		return path, nil
	}
	return "", lastErr
}

var errNotPublished = errors.New("that month's database is not published yet")

func (f Fetcher) download(ctx context.Context, url, path string) error {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", errNotPublished, url)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: the server answered %s", url, resp.Status)
	}

	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	// Written beside the target and renamed, so that a download cut off halfway
	// never becomes a database the next start opens and half-reads.
	temp, err := os.CreateTemp(f.Dir, ".download-*")
	if err != nil {
		return err
	}
	defer func() {
		temp.Close()
		os.Remove(temp.Name())
	}()

	var source io.Reader = resp.Body
	if strings.HasSuffix(url, ".gz") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("%s is not gzip: %w", url, err)
		}
		defer func() { _ = gz.Close() }()
		source = gz
	}

	written, err := io.Copy(temp, io.LimitReader(source, MaxDatabaseBytes+1))
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if written > MaxDatabaseBytes {
		return fmt.Errorf("%s is larger than the %d MB a database may be", url, MaxDatabaseBytes>>20)
	}
	if written == 0 {
		return fmt.Errorf("%s returned nothing", url)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
