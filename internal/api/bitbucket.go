package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/gitsrc"
	"skifity/internal/store"
)

// What a Bitbucket connection needs from the API that the other kinds do not:
// a check of its token before it is saved, and the whole commit behind the
// abbreviated one its pull request webhooks carry. How Bitbucket is spoken to
// is in internal/gitsrc.

// resolveBitbucketCommit asks Bitbucket for a commit's whole hash. A variable,
// so a test can answer for a Bitbucket it does not run.
var resolveBitbucketCommit = gitsrc.ResolveBitbucketCommit

// bitbucketLookupTime bounds the one question a webhook asks Bitbucket before
// it answers. Bitbucket gives a delivery ten seconds.
const bitbucketLookupTime = 5 * time.Second

// checkBitbucketSource settles a Bitbucket connection before it is saved:
// bitbucket.org and nowhere else, a workspace that can go into a URL, an email
// that is one, and a token Bitbucket accepts. It answers the email to seal.
func (s *Server) checkBitbucketSource(r *http.Request, source *store.GitSource, token, email string) (string, error) {
	// Bitbucket Cloud has one address. Any other is a Bitbucket Data Center,
	// which speaks an API nothing here knows — and is where the token would
	// be sent.
	if source.BaseURL != "" && !strings.EqualFold(source.BaseURL, gitsrc.BitbucketURL) {
		return "", errdoc.BadRequest("A Bitbucket connection is for bitbucket.org and takes no server address. Bitbucket Data Center is not supported.")
	}
	source.BaseURL = gitsrc.BitbucketURL
	if source.Account != "" && !gitsrc.ValidBitbucketWorkspace(source.Account) {
		return "", errdoc.BadRequest("That is not a Bitbucket workspace's id: it is the part of a repository's address after bitbucket.org/.")
	}
	email = strings.TrimSpace(email)
	if email != "" && (len(email) > maxEmailLength || !strings.Contains(email, "@") || strings.ContainsAny(email, ": \t\r\n")) {
		return "", errdoc.BadRequest("That is not an email address. Give the Atlassian account's email with an API token, or leave it empty.")
	}

	err := checkBitbucketToken(r.Context(), gitsrc.ListRequest{
		Kind: "bitbucket", BaseURL: source.BaseURL, Token: token, Email: email, Account: source.Account,
	})
	var answered *gitsrc.HostError
	switch {
	case err == nil:
		return email, nil
	case errors.As(err, &answered) && (answered.Status == http.StatusUnauthorized || answered.Status == http.StatusForbidden):
		return "", errdoc.BitbucketTokenRefused(http.StatusText(answered.Status))
	case errors.As(err, &answered) && answered.Status == http.StatusNotFound && source.Account != "":
		return "", errdoc.BitbucketWorkspaceUnknown(source.Account)
	}
	s.log.Warn("could not check a Bitbucket token", "error", err)
	return "", errdoc.BitbucketCheckFailed(err.Error())
}

// completeBitbucketCommit gives a Bitbucket pull request event its whole
// commit. The webhook names the pull request's head by twelve characters,
// which the build cannot fetch — git fetches a commit by its whole name — and
// a status cannot be set on. Asked of the repository the pull request
// targets, where a pull request from the repository itself has the commit.
//
// When Bitbucket cannot say, the commit is left out rather than kept short,
// and the preview is built from the tip of the pull request's branch: the
// same code, one webhook later at worst.
func (s *Server) completeBitbucketCommit(r *http.Request, source store.GitSource, event gitsrc.PushEvent) gitsrc.PushEvent {
	// Only for a preview to build: a closed pull request builds nothing.
	if source.Kind != "bitbucket" || event.Kind != "pull_request_opened" || event.CommitSHA == "" ||
		gitsrc.FullCommit(event.CommitSHA) {
		return event
	}
	short := event.CommitSHA
	event.CommitSHA = ""
	if event.Fork {
		// Not in this repository at all. There is no preview to build.
		return event
	}
	token, email := s.gitCredentials(r, source)
	lookup, cancel := context.WithTimeout(r.Context(), bitbucketLookupTime)
	defer cancel()
	full, err := resolveBitbucketCommit(lookup, gitsrc.ListRequest{
		Kind: source.Kind, BaseURL: source.BaseURL, Token: token, Email: email, Account: source.Account,
	}, gitsrc.RepoName(event.RepoURL), short)
	if err != nil {
		s.log.Warn("could not find the whole commit of a Bitbucket pull request; building its branch",
			"git_source", source.ID, "pull_request", event.PullRequest, "error", err)
		return event
	}
	event.CommitSHA = full
	return event
}
