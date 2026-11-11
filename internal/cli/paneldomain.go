package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/version"
)

// panelDomainAnswer is where the panel answers, as GET /api/panel/domain says.
type panelDomainAnswer struct {
	URL         string          `json:"url"`
	Host        string          `json:"host"`
	Scheme      string          `json:"scheme"`
	Domain      string          `json:"domain,omitempty"`
	State       string          `json:"state,omitempty"`
	Problem     *errdoc.Problem `json:"problem,omitempty"`
	Previous    []string        `json:"previous"`
	Certificate *struct {
		State    string     `json:"state"`
		Message  string     `json:"message,omitempty"`
		NotAfter *time.Time `json:"not_after,omitempty"`
	} `json:"certificate,omitempty"`
	DNSTarget   string          `json:"dns_target"`
	FreeAddress string          `json:"free_address,omitempty"`
	Movable     bool            `json:"movable"`
	Reason      *errdoc.Problem `json:"reason,omitempty"`
	// Restarting is taking the domain off: the panel restarts at URL.
	Restarting bool `json:"restarting,omitempty"`
}

// dnsCheckAnswer is what checking a domain's DNS answers.
type dnsCheckAnswer struct {
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
	Found    []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"found"`
	Expected []string `json:"expected"`
}

// panelDomainOptions are the flags of `skifity domains panel`.
type panelDomainOptions struct {
	check, ignoreDNS, remove, wait, asJSON bool
}

// How long `skifity domains panel NAME` follows a move, and how often it asks.
// A variable so a test does not wait five seconds a question.
var (
	panelMovePoll = 5 * time.Second
	panelMoveWait = 20 * time.Minute
)

// runPanelDomain is `skifity domains panel`, which puts the panel on a domain
// of its own, with HTTPS. A panel administrator's. Its flags, --json among
// them, are the domains command's.
//
//	skifity domains panel                              where the panel answers
//	skifity domains panel panel.example.com --check    what the name's DNS says, changing nothing
//	skifity domains panel panel.example.com            move the panel there, and follow it
//	skifity domains panel --remove                     cancel a move that stopped, or go back to sslip.io
//
// The panel keeps answering at the address it has until the certificate is
// issued, and at that address afterwards for webhooks and API tokens, so this
// command, which uses one, follows the move to the end from where it started.
func runPanelDomain(ctx context.Context, client *Client, args []string, opts panelDomainOptions, out io.Writer) error {
	const path = "/api/panel/domain"
	switch {
	case opts.remove:
		if len(args) != 0 {
			return errdoc.BadRequest(fmt.Sprintf("--remove takes no domain: `%s domains panel --remove`.", version.Binary))
		}
		var answer panelDomainAnswer
		if err := client.Do(ctx, "DELETE", path, nil, &answer); err != nil {
			return err
		}
		if opts.asJSON {
			return writeJSON(out, answer)
		}
		if answer.Restarting {
			fmt.Fprintf(out, "The domain is off, and the panel is restarting at %s, over plain HTTP.\n", answer.URL)
			fmt.Fprintf(out, "Everybody signs in again there. `%s login --url %s` points this CLI at it.\n", version.Binary, answer.URL)
			return nil
		}
		fmt.Fprintf(out, "The move was cancelled. The panel answers at %s, as it did.\n", answer.URL)
		return nil

	case len(args) == 0:
		if opts.check {
			return errdoc.BadRequest(fmt.Sprintf("Name the domain to check: `%s domains panel panel.example.com --check`.", version.Binary))
		}
		var answer panelDomainAnswer
		if err := client.Do(ctx, "GET", path, nil, &answer); err != nil {
			return err
		}
		if opts.asJSON {
			return writeJSON(out, answer)
		}
		printPanelDomain(out, answer)
		return nil

	case len(args) > 1:
		return errdoc.BadRequest(fmt.Sprintf("Name one domain: `%s domains panel panel.example.com`.", version.Binary))
	}

	domain := args[0]
	if opts.check {
		var check dnsCheckAnswer
		if err := client.Do(ctx, "POST", path+"/check", map[string]string{"domain": domain}, &check); err != nil {
			return err
		}
		if opts.asJSON {
			return writeJSON(out, check)
		}
		printPanelDNSCheck(out, check)
		return nil
	}

	body := map[string]any{"domain": domain}
	if opts.ignoreDNS {
		body["ignore_dns"] = true
	}
	var started panelDomainAnswer
	if err := client.Do(ctx, "POST", path, body, &started); err != nil {
		return err
	}
	if !opts.wait {
		if opts.asJSON {
			return writeJSON(out, started)
		}
		fmt.Fprintf(out, "Moving the panel to %s. `%s domains panel` says where it stands.\n", started.Domain, version.Binary)
		return nil
	}
	if !opts.asJSON {
		fmt.Fprintf(out, "Asking for a certificate for %s. The panel keeps answering at %s until it is issued.\n",
			started.Domain, started.URL)
	}
	answer, err := followPanelMove(ctx, client, started, out, !opts.asJSON)
	if err != nil {
		return err
	}
	if opts.asJSON {
		return writeJSON(out, answer)
	}
	fmt.Fprintf(out, "\nThe panel answers at %s now. Everybody signs in again there.\n", answer.URL)
	fmt.Fprintf(out, "%s keeps answering for the webhooks registered there and for API tokens, this CLI's included;\n"+
		"`%s login --url %s` points it at the new address.\n", started.URL, version.Binary, answer.URL)
	return nil
}

// followPanelMove asks where a move stands until it is over: the panel
// answering at the domain, or the move stopped with why. The panel is away for
// a moment while it restarts, and a question then is not an answer.
func followPanelMove(ctx context.Context, client *Client, started panelDomainAnswer, out io.Writer, say bool) (panelDomainAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, panelMoveWait)
	defer cancel()
	last := started.State
	for {
		select {
		case <-ctx.Done():
			return started, errdoc.BadRequest(fmt.Sprintf("The move to %s did not finish in time. `%s domains panel` says where it stands.",
				started.Domain, version.Binary))
		case <-time.After(panelMovePoll):
		}
		var answer panelDomainAnswer
		if err := client.Do(ctx, "GET", "/api/panel/domain", nil, &answer); err != nil {
			if last == "restarting" {
				continue
			}
			return started, err
		}
		if answer.Host == started.Domain && answer.Scheme == "https" {
			return answer, nil
		}
		if answer.State == last {
			continue
		}
		last = answer.State
		switch answer.State {
		case "failed":
			if answer.Problem != nil {
				return answer, answer.Problem
			}
			return answer, errdoc.BadRequest("The move stopped without saying why. `" + version.Binary + " domains panel` says where it stands.")
		case "restarting":
			if say {
				fmt.Fprintf(out, "The certificate is issued. The panel is restarting at https://%s.\n", answer.Domain)
			}
		}
	}
}

// printPanelDomain says where the panel answers and where a move stands.
func printPanelDomain(out io.Writer, answer panelDomainAnswer) {
	kind := "plain HTTP"
	if answer.Scheme == "https" {
		kind = "HTTPS"
		if c := answer.Certificate; c != nil && c.State == "ready" && c.NotAfter != nil {
			kind += ", certificate valid until " + c.NotAfter.Format("2006-01-02")
		}
	}
	fmt.Fprintf(out, "Panel     %s (%s)\n", orNone(answer.URL), kind)
	if len(answer.Previous) > 0 {
		fmt.Fprintf(out, "Also at   %s, for the webhooks registered there\n", strings.Join(answer.Previous, ", "))
	}
	switch answer.State {
	case "moving":
		fmt.Fprintf(out, "\nMoving to %s: asking for its certificate.\n", answer.Domain)
	case "restarting":
		fmt.Fprintf(out, "\nMoving to %s: the certificate is issued, and the panel is restarting there.\n", answer.Domain)
	case "failed":
		fmt.Fprintf(out, "\nThe move to %s stopped.\n", answer.Domain)
		if answer.Problem != nil {
			fmt.Fprintf(out, "\n%s\n", answer.Problem.Text())
		}
		fmt.Fprintf(out, "Try again with `%s domains panel %s`, or cancel it with `%s domains panel --remove`.\n",
			version.Binary, answer.Domain, version.Binary)
		return
	}
	if !answer.Movable {
		if answer.Reason != nil {
			fmt.Fprintf(out, "\n%s\n", answer.Reason.Text())
		}
		return
	}
	if answer.Scheme == "http" && answer.State == "" {
		fmt.Fprintf(out, "\nPut it on a domain of your own for HTTPS:\n")
		fmt.Fprintf(out, "  %s domains panel panel.example.com --check   what the name's DNS says\n", version.Binary)
		fmt.Fprintf(out, "  %s domains panel panel.example.com           move the panel there\n", version.Binary)
	}
}

// printPanelDNSCheck says what a domain's DNS says, and what to do about it.
func printPanelDNSCheck(out io.Writer, check dnsCheckAnswer) {
	found := make([]string, 0, len(check.Found))
	for _, record := range check.Found {
		found = append(found, record.Type+" "+record.Value)
	}
	target := ""
	if len(check.Expected) > 0 {
		target = check.Expected[0]
	}
	record := "an A record"
	if strings.Contains(target, ":") {
		record = "an AAAA record"
	}
	switch check.Status {
	case "here":
		fmt.Fprintf(out, "%s points at this server (%s). Move the panel there with `%s domains panel %s`.\n",
			check.Hostname, strings.Join(found, ", "), version.Binary, check.Hostname)
	case "missing":
		fmt.Fprintf(out, "%s has no DNS record yet. Create %s for it pointing at %s, and check again in a few minutes.\n",
			check.Hostname, record, orNone(target))
	case "proxied":
		fmt.Fprintf(out, "%s answers with Cloudflare's addresses (%s). Point the record at %s and set it to DNS only until the panel opens over HTTPS.\n",
			check.Hostname, strings.Join(found, ", "), orNone(target))
	case "unknown":
		fmt.Fprintf(out, "%s points at %s, and this panel does not know its own public address to compare. "+
			"Set Settings → Domains and HTTPS → Cluster public IP.\n", check.Hostname, strings.Join(found, ", "))
	default:
		fmt.Fprintf(out, "%s points at %s, which is not only this server. Change the record to %s, and check again.\n",
			check.Hostname, strings.Join(found, ", "), orNone(target))
	}
}
