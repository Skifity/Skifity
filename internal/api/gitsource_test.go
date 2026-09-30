package api

import (
	"net/http"
	"testing"

	"skifity/internal/store"
)

func TestAnAppCannotBorrowAnotherTeamsGitConnection(t *testing.T) {
	// Detection already refused it; creating the app did not, and the
	// connection is what a build clones with and a webhook is made with.
	h := newHarness(t)
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	theirs := store.GitSource{TeamID: rival.team.ID, Kind: "github_pat", Name: "rival", BaseURL: "https://github.com"}
	if err := h.db.CreateGitSource(t.Context(), &theirs); err != nil {
		t.Fatal(err)
	}
	ours := store.GitSource{TeamID: acme.team.ID, Kind: "github_pat", Name: "acme", BaseURL: "https://github.com"}
	if err := h.db.CreateGitSource(t.Context(), &ours); err != nil {
		t.Fatal(err)
	}

	create := func(source string) int {
		status, _ := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/apps", map[string]any{
			"name": "private-" + source, "source_type": "git",
			"repo_url": "https://github.com/rival/secret", "git_source_id": source,
		})
		return status
	}
	if status := create(theirs.ID); status != http.StatusNotFound {
		t.Fatalf("an app with another team's Git connection answered %d", status)
	}
	if status := create(ours.ID); status != http.StatusCreated {
		t.Fatalf("an app with the team's own connection answered %d", status)
	}

	status, _ := h.do(acme, http.MethodPost, "/api/environments/"+acme.env.ID+"/stack", map[string]any{
		"repo_url": "https://github.com/rival/secret", "git_source_id": theirs.ID,
		"services": []map[string]any{{"name": "web", "build": "."}},
	})
	if status != http.StatusNotFound {
		t.Fatalf("a stack with another team's Git connection answered %d", status)
	}
}
