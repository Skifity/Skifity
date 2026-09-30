// Package cli is the skifity command line tool.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// Client talks to a panel's API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Config is the CLI's stored configuration.
//
// It lives in the user's config directory rather than the project, so a token
// is not committed to a repository by accident.
type Config struct {
	PanelURL string `json:"panel_url"`
	Token    string `json:"token"`
	TeamID   string `json:"team_id,omitempty"`
	TeamName string `json:"team_name,omitempty"`
}

// ConfigPath returns where the CLI keeps its configuration.
func ConfigPath() (string, error) {
	if override := os.Getenv("SKIFITY_CONFIG"); override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find your configuration directory: %w", err)
	}
	return filepath.Join(dir, version.Binary, "config.json"), nil
}

// LoadConfig reads the stored configuration, letting the environment override
// it.
//
// SKIFITY_URL and SKIFITY_TOKEN exist so that a CI job, a container or an AI
// assistant can use the CLI without an interactive sign-in, and so that a
// machine with no stored configuration at all still works. They win over the
// file, because someone who sets them meant them.
func LoadConfig() (Config, error) {
	var cfg Config

	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read %s: %w", path, readErr)
	}

	if url := strings.TrimSpace(os.Getenv("SKIFITY_URL")); url != "" {
		cfg.PanelURL = url
	}
	if token := strings.TrimSpace(os.Getenv("SKIFITY_TOKEN")); token != "" {
		cfg.Token = token
	}
	if team := strings.TrimSpace(os.Getenv("SKIFITY_TEAM")); team != "" {
		cfg.TeamID = team
	}

	if cfg.PanelURL == "" || cfg.Token == "" {
		return Config{}, fmt.Errorf(
			"you are not signed in. Run `%s login` first, or set SKIFITY_URL and SKIFITY_TOKEN",
			version.Binary)
	}
	return cfg, nil
}

// SaveConfig writes the configuration with permissions that keep the token
// private.
func SaveConfig(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the configuration: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// NewClient builds a client from a configuration.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(cfg.PanelURL, "/"),
		token:   cfg.Token,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Do sends a request and decodes the response into out.
//
// Errors come back as the panel's own Problem, so the CLI shows the same cause,
// impact and fix the panel does.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the panel at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeProblem(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("read the panel's answer: %w", err)
	}
	return nil
}

// DoLong is Do for a request whose length is the work's rather than the
// network's.
//
// The ordinary client gives up after a minute, which is right for a request
// that asks the panel a question and wrong for one that waits for a migration
// to finish. The context is still the deadline — Ctrl-C ends it, and a caller
// that wants a cap sets one.
func (c *Client) DoLong(ctx context.Context, method, path string, body, out any) error {
	long := &Client{baseURL: c.baseURL, token: c.token, http: &http.Client{Timeout: 0}}
	return long.Do(ctx, method, path, body, out)
}

// Stream opens a server-sent event stream and calls onEvent for each message.
func (c *Client) Stream(ctx context.Context, path string, onEvent func(event, data string) bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build the request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", version.UserAgent())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// A stream has no overall deadline: following logs is meant to stay open.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the panel at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeProblem(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	eventName := "message"
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			// A blank line ends one event.
			if data.Len() > 0 {
				if !onEvent(eventName, data.String()) {
					return nil
				}
			}
			eventName, data = "message", strings.Builder{}
		case strings.HasPrefix(line, ":"):
			// A comment, used as a keepalive.
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("read the stream: %w", err)
	}
	return nil
}

// decodeProblem turns an error response back into a Problem.
func decodeProblem(resp *http.Response) error {
	var envelope struct {
		Error *errdoc.Problem `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != nil {
		return envelope.Error
	}
	// An answer that is not one of ours usually means something else is on
	// that address.
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	return errdoc.New("cli.unexpected_response", "The panel answered with something unexpected").
		WithCause("HTTP %s from %s: %s", resp.Status, resp.Request.URL.Path, snippet).
		WithImpact("The command did not complete.").
		WithFix("Check that the URL points at a Skifity panel and not at something else.")
}

// Query builds a query string from pairs, skipping empty values.
func Query(pairs ...string) string {
	values := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			values.Set(pairs[i], pairs[i+1])
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}
