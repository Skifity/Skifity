package gitsrc

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strings"
)

// Bitbucket Cloud's webhooks.
//
// Nothing about them is shaped like GitHub's. The event is in X-Event-Key, a
// push is a list of the refs it changed with the old and new commit of each,
// and a pull request names its source and destination repositories rather
// than saying "fork". Kubero's parser read GitHub's field names out of these
// payloads, found nothing, and — because it could not check a signature it
// had no code for — marked every delivery verified. Here the payload is read
// as Atlassian documents it, and a delivery is verified before it is read at
// all (VerifyBitbucketSignature, called by the API by the connection's kind).
//
// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/
// https://support.atlassian.com/bitbucket-cloud/docs/manage-webhooks/

// BitbucketEvents are the events a Bitbucket repository has to send for
// everything the panel does with one: pushes deploy, a pull request opened or
// pushed to makes or updates its preview, and one merged or declined takes it
// away. The webhook the panel registers asks for exactly these, and the
// instructions for adding one by hand name the same.
var BitbucketEvents = []string{
	"repo:push",
	"pullrequest:created",
	"pullrequest:updated",
	"pullrequest:fulfilled",
	"pullrequest:rejected",
}

// VerifyBitbucketSignature checks the X-Hub-Signature header: an HMAC of the
// raw body with the webhook's secret, written method=hexdigest the way WebSub
// defines it. Bitbucket sends sha256 today and says that may change, so the
// stronger two WebSub names are read too; sha1 is not, since Bitbucket never
// sent it and it is the one a signature should not rest on.
//
// No header is a refusal, not a pass. Bitbucket only signs deliveries for a
// hook whose secret is set, so an unsigned delivery is a hook somebody added
// without the secret — or somebody who is not Bitbucket.
func VerifyBitbucketSignature(secret string, body []byte, header string) error {
	if secret == "" {
		return errors.New("this Git connection has no webhook secret, so pushes cannot be verified")
	}
	method, signature, ok := strings.Cut(strings.TrimSpace(header), "=")
	if !ok || signature == "" {
		return ErrBadSignature
	}
	var newHash func() hash.Hash
	switch strings.ToLower(method) {
	case "sha256":
		newHash = sha256.New
	case "sha384":
		newHash = sha512.New384
	case "sha512":
		newHash = sha512.New
	default:
		return ErrBadSignature
	}
	mac := hmac.New(newHash, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(signature))) {
		return ErrBadSignature
	}
	return nil
}

// bitbucketRepository is the Repository entity every event carries.
type bitbucketRepository struct {
	// FullName is workspace/slug, the path the repository has in its URL.
	FullName string `json:"full_name"`
	// UUID does not change when a repository is renamed or moved, so it is
	// what tells two repositories apart when both are there.
	UUID  string `json:"uuid"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// repoURL is the repository's browser address, which is what an app stores.
// The html link is used when it is on bitbucket.org; anything else — the
// examples in Atlassian's own documentation put api.bitbucket.org there — is
// replaced by the address the full name makes.
func (r bitbucketRepository) repoURL() string {
	if parsed, err := url.Parse(r.Links.HTML.Href); err == nil && strings.EqualFold(parsed.Hostname(), "bitbucket.org") {
		return NormaliseRepoURL(r.Links.HTML.Href)
	}
	if r.FullName != "" {
		return NormaliseRepoURL(BitbucketURL + "/" + r.FullName)
	}
	return NormaliseRepoURL(r.Links.HTML.Href)
}

// bitbucketAuthor is a commit's author: the raw "Name <email>" Git recorded,
// and the Bitbucket account it matched, when it matched one.
type bitbucketAuthor struct {
	Raw  string `json:"raw"`
	User *struct {
		DisplayName string `json:"display_name"`
	} `json:"user"`
}

// name is who to show in the deploy history. The address in the raw form is
// left out, as it is for every other host.
func (a bitbucketAuthor) name() string {
	if a.User != nil && strings.TrimSpace(a.User.DisplayName) != "" {
		return strings.TrimSpace(a.User.DisplayName)
	}
	name, _, _ := strings.Cut(a.Raw, "<")
	return strings.TrimSpace(name)
}

// bitbucketRef is one side of a change in a push: a branch or a tag, and the
// commit it points at.
type bitbucketRef struct {
	// Type is branch or tag — or annotated_tag, which the documentation
	// names for the old side — and named_branch or bookmark for Mercurial,
	// which Bitbucket no longer hosts.
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target struct {
		Type    string          `json:"type"`
		Hash    string          `json:"hash"`
		Message string          `json:"message"`
		Author  bitbucketAuthor `json:"author"`
	} `json:"target"`
}

type bitbucketPush struct {
	Repository bitbucketRepository `json:"repository"`
	Push       struct {
		Changes []struct {
			// New is null for a ref the push deleted, Old for one it made.
			New     *bitbucketRef `json:"new"`
			Old     *bitbucketRef `json:"old"`
			Created bool          `json:"created"`
			Closed  bool          `json:"closed"`
			Forced  bool          `json:"forced"`
		} `json:"changes"`
	} `json:"push"`
}

// bitbucketEnd is a pull request's source or destination.
type bitbucketEnd struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		// Hash is abbreviated in a webhook, to twelve characters: see
		// FullCommit, and the API's completeBitbucketCommit.
		Hash string `json:"hash"`
	} `json:"commit"`
	Repository *bitbucketRepository `json:"repository"`
}

type bitbucketPullRequest struct {
	Repository  bitbucketRepository `json:"repository"`
	PullRequest struct {
		ID     int    `json:"id"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
		Source      bitbucketEnd `json:"source"`
		Destination bitbucketEnd `json:"destination"`
	} `json:"pullrequest"`
}

// bitbucketForkPreview is what the delivery log says about a pull request from
// a fork. Bitbucket Cloud has no refs/pull-requests in the repository a pull
// request targets (BCLOUD-5814), so the commit exists only in the fork, which
// the build does not clone and the connection's token may not be able to read.
const bitbucketForkPreview = "Bitbucket Cloud does not make a fork's commits fetchable from the repository a pull request targets, so it has no preview"

func parseBitbucket(event string, body []byte) ([]PushEvent, error) {
	switch event {
	// Bitbucket Data Center's "Test connection" sends this; Bitbucket Cloud
	// has no test delivery and its "Resend" repeats a real one. Answered the
	// way a GitHub ping is, and only once it has been verified like any other.
	case "diagnostics:ping":
		return []PushEvent{{Kind: "ping"}}, nil

	case "repo:push":
		var payload bitbucketPush
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("read push payload: %w", err)
		}
		repoURL := payload.Repository.repoURL()
		var events []PushEvent
		for _, change := range payload.Push.Changes {
			switch {
			case change.New == nil:
				// A deleted branch takes its preview away. A deleted tag
				// deploys nothing, as on every other host.
				if change.Old != nil && change.Old.Type == "branch" && change.Old.Name != "" {
					events = append(events, PushEvent{
						Kind: "push", RepoURL: repoURL, Branch: change.Old.Name,
						Deleted: true, Before: change.Old.Target.Hash,
					})
				}

			case change.New.Type == "branch":
				if change.New.Name == "" || change.New.Target.Hash == "" {
					continue
				}
				message := change.New.Target.Message
				ev := PushEvent{
					Kind:          "push",
					RepoURL:       repoURL,
					Branch:        change.New.Name,
					CommitSHA:     change.New.Target.Hash,
					CommitMessage: firstLine(message),
					CommitAuthor:  change.New.Target.Author.name(),
					Forced:        change.Forced,
					// The target is the newest commit on the branch after
					// the push, which is the one whose message counts.
					SkipMarker: SkipMarker(message),
				}
				if change.Old != nil {
					ev.Before = change.Old.Target.Hash
				}
				// No files: a Bitbucket push lists up to five commits and
				// none of the paths they touched. FilesKnown stays false,
				// which is "everything changed" — see PushEvent.
				events = append(events, ev)

			case change.New.Type == "tag" || change.New.Type == "annotated_tag":
				// The target is the commit the tag points at, annotated or
				// not, which is what gets built.
				if change.Closed || change.New.Name == "" || change.New.Target.Hash == "" {
					continue
				}
				events = append(events, PushEvent{
					Kind:          "tag",
					RepoURL:       repoURL,
					Tag:           change.New.Name,
					CommitSHA:     change.New.Target.Hash,
					CommitMessage: firstLine(change.New.Target.Message),
					CommitAuthor:  change.New.Target.Author.name(),
				})
			}
		}
		if len(events) == 0 {
			return nil, ErrUnsupportedEvent
		}
		return events, nil

	case "pullrequest:created", "pullrequest:updated", "pullrequest:fulfilled", "pullrequest:rejected":
		var payload bitbucketPullRequest
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("read pull request payload: %w", err)
		}
		pr := payload.PullRequest
		kind := "pull_request_opened"
		switch event {
		case "pullrequest:fulfilled", "pullrequest:rejected":
			// Merged or declined.
			kind = "pull_request_closed"
		default:
			// "updated" is also a new title or a new reviewer. One on a pull
			// request that is already merged or declined must not bring
			// its preview back.
			if pr.State != "" && !strings.EqualFold(pr.State, "OPEN") {
				return nil, ErrUnsupportedEvent
			}
		}
		if pr.ID <= 0 {
			return nil, fmt.Errorf("the pull request payload has no pull request number")
		}

		// A pull request from a repository other than the one it targets was
		// opened by somebody who does not necessarily have write access here.
		// Compared by the id that survives a rename when both sides have one,
		// by name otherwise, and anything missing counts as a fork.
		destination := payload.Repository
		if pr.Destination.Repository != nil {
			destination = *pr.Destination.Repository
		}
		fork := true
		if source := pr.Source.Repository; source != nil {
			switch {
			case source.UUID != "" && destination.UUID != "":
				fork = !strings.EqualFold(source.UUID, destination.UUID)
			case source.FullName != "" && destination.FullName != "":
				fork = !strings.EqualFold(source.FullName, destination.FullName)
			}
		}

		ev := PushEvent{
			Kind:          kind,
			RepoURL:       payload.Repository.repoURL(),
			Branch:        pr.Source.Branch.Name,
			SourceBranch:  pr.Source.Branch.Name,
			CommitSHA:     strings.ToLower(pr.Source.Commit.Hash),
			CommitMessage: pr.Title,
			CommitAuthor:  pr.Author.DisplayName,
			PullRequest:   pr.ID,
			Fork:          fork,
		}
		if fork && kind == "pull_request_opened" {
			ev.NoPreview = bitbucketForkPreview
		}
		return []PushEvent{ev}, nil

	default:
		return nil, ErrUnsupportedEvent
	}
}

// FullCommit reports whether a hash names one commit without a lookup: forty
// hex digits, or sixty-four in a SHA-256 repository. Bitbucket's pull request
// webhooks carry twelve, which a build cannot fetch and a status cannot be
// set on.
func FullCommit(sha string) bool {
	return validSHA(strings.ToLower(sha))
}
