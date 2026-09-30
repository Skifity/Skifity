package events_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Every event the server publishes is listened for, and every event the panel
// listens for is published.
//
// Both halves had drifted. The server was publishing seven kinds nothing in the
// interface handled — "audit", which is what the Activity page is for; "server",
// "app" and "domain", which is the whole output of the watcher; "database" and
// "backups"; and the project and app lifecycle events — so a page sat on stale
// data until somebody reloaded it. And the add-server page was handling "step",
// which nothing has ever published.
//
// This reads both sides out of the source, because a list kept by hand is a
// list that goes stale in exactly the way this is here to prevent.
func TestEveryEventIsBothPublishedAndHandled(t *testing.T) {
	published := publishedEvents(t)
	handled := handledEvents(t)

	if len(published) < 10 || len(handled) < 8 {
		t.Fatalf("found %d published and %d handled events, so this test has stopped reading the source",
			len(published), len(handled))
	}

	// A log line is a special case at both ends: it is published per deployment
	// and per operation, and consumed by a component that renders it directly
	// rather than refetching. It is in both lists, which is the point.
	for _, name := range published {
		if !slices.Contains(handled, name) {
			t.Errorf("the server publishes %q and nothing in the interface listens for it", name)
		}
	}
	for _, name := range handled {
		if !slices.Contains(published, name) {
			t.Errorf("the interface listens for %q and nothing publishes it", name)
		}
	}
}

// publishedEvents reads the second argument of every hub.Publish call.
func publishedEvents(t *testing.T) []string {
	t.Helper()
	call := regexp.MustCompile(`Publish\([^,]+,\s*"([\w.]+)"`)
	seen := map[string]bool{}

	root := filepath.Join("..", "..", "internal")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range call.FindAllStringSubmatch(string(body), -1) {
			seen[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the server: %v", err)
	}
	// The watcher publishes through a helper whose kind is a parameter, so its
	// three are named where it calls that helper instead.
	helper := regexp.MustCompile(`w\.publish\([^,]+,\s*"([\w.]+)"`)
	body, err := os.ReadFile(filepath.Join("..", "watch", "watch.go"))
	if err != nil {
		t.Fatalf("read the watcher: %v", err)
	}
	for _, match := range helper.FindAllStringSubmatch(string(body), -1) {
		seen[match[1]] = true
	}
	return sorted(seen)
}

// handledEvents reads the keys of every handler map passed to useEvents.
func handledEvents(t *testing.T) []string {
	t.Helper()
	// A handler is a key at the start of a line inside the map: either a bare
	// identifier or a quoted one, because "app.created" cannot be bare.
	handler := regexp.MustCompile(`(?m)^\s{4,8}"?([a-z][\w.]*)"?:\s*(\(|async)`)
	seen := map[string]bool{}

	root := filepath.Join("..", "..", "web", "src")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".tsx") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, block := range useEventsBlocks(text) {
			for _, match := range handler.FindAllStringSubmatch(block, -1) {
				seen[match[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the interface: %v", err)
	}
	// Added by useEvents itself for every subscriber, and published by the
	// stream handler rather than through the hub.
	delete(seen, "desync")
	return sorted(seen)
}

// useEventsBlocks returns the text of each useEvents(...) call.
func useEventsBlocks(text string) []string {
	var out []string
	for index := 0; ; {
		start := strings.Index(text[index:], "useEvents(")
		if start < 0 {
			return out
		}
		start += index
		// Start at the opening bracket of the call itself, not at the "u" of
		// useEvents: counting from there is balanced before it has begun.
		depth, end := 0, start+len("useEvents(")-1
		for ; end < len(text); end++ {
			switch text[end] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if end >= len(text) {
			return out
		}
		out = append(out, text[start:end])
		index = end
	}
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
