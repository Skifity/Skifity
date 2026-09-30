package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"skifity/internal/store"
	"skifity/internal/tlscert/certtest"
)

// A team's own certificates: who may upload and read them, that the private
// key is sealed and never answered with, that no team can name another's
// hostnames, and that a change re-applies the apps it affects.

// issued is a certificate for these hostnames, signed by a CA of the test's,
// with its chain and key as a person would paste them.
func issued(t *testing.T, hostnames ...string) (chain, key string) {
	t.Helper()
	root := certtest.Root(t, "Example Root CA")
	intermediate := certtest.Issue(t, root, certtest.Options{CommonName: "Example Issuing CA", IsCA: true})
	leaf := certtest.Issue(t, intermediate, certtest.Options{CommonName: hostnames[0], DNSNames: hostnames})
	return string(certtest.Chain(leaf, intermediate)), string(leaf.KeyPEM)
}

// keyBody is the base64 of a PEM key, which is what would appear in an
// answer or a row if it had leaked — whatever it was wrapped in.
func keyBody(key string) string {
	lines := strings.Split(strings.TrimSpace(key), "\n")
	return strings.Join(lines[1:len(lines)-1], "")
}

// syncRecorder is a deployer that says which apps were re-applied, as they
// are, because a certificate change re-applies them in the background.
type syncRecorder struct {
	fakeDeployer
	synced chan string
}

func (f *syncRecorder) Sync(_ context.Context, appID string) error {
	f.synced <- appID
	return nil
}

// waitForSyncs collects the apps re-applied until none has been for a moment.
func (f *syncRecorder) waitForSyncs(t *testing.T, want int) []string {
	t.Helper()
	var got []string
	deadline := time.After(5 * time.Second)
	for len(got) < want {
		select {
		case appID := <-f.synced:
			got = append(got, appID)
		case <-deadline:
			t.Fatalf("only %d of %d apps were re-applied: %v", len(got), want, got)
		}
	}
	select {
	case extra := <-f.synced:
		t.Fatalf("an app the change does not affect was re-applied: %s", extra)
	case <-time.After(100 * time.Millisecond):
	}
	slices.Sort(got)
	return got
}

func (h *harness) domain(app store.App, hostname string) store.Domain {
	h.t.Helper()
	domain := store.Domain{AppID: app.ID, Hostname: hostname, TLS: true, Status: "pending"}
	if err := h.db.CreateDomain(h.t.Context(), &domain); err != nil {
		h.t.Fatalf("create domain: %v", err)
	}
	return domain
}

func TestACertificateIsUploadedSealedAndNeverAnsweredWith(t *testing.T) {
	h := newHarness(t)
	deployer := &syncRecorder{synced: make(chan string, 16)}
	h.api.deployer = deployer
	acme := h.newTenant("acme")
	shop, blog, other := h.app(acme, "shop"), h.app(acme, "blog"), h.app(acme, "other")
	h.domain(shop, "shop.example.com")
	h.domain(blog, "blog.example.com")
	h.domain(other, "other.example.org")
	chain, key := issued(t, "*.example.com")
	path := "/api/teams/" + acme.team.ID + "/certificates"

	status, body := h.do(acme, http.MethodPost, path,
		map[string]string{"name": "Company wildcard", "certificate": chain, "private_key": key})
	if status != http.StatusCreated {
		t.Fatalf("uploading answered %d\n%s", status, body)
	}
	var saved struct {
		Certificate struct {
			ID        string   `json:"id"`
			Hostnames []string `json:"hostnames"`
			State     string   `json:"state"`
			Domains   []struct {
				Hostname string `json:"hostname"`
				AppName  string `json:"app_name"`
			} `json:"domains"`
		} `json:"certificate"`
		Replaced bool `json:"replaced"`
		Updating int  `json:"updating"`
	}
	if err := json.Unmarshal([]byte(body), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Replaced || saved.Certificate.State != "valid" || len(saved.Certificate.Domains) != 2 || saved.Updating != 2 {
		t.Errorf("the answer is %s", body)
	}
	if got := deployer.waitForSyncs(t, 2); !slices.Equal(got, sortedIDs(shop.ID, blog.ID)) {
		t.Errorf("re-applied %v, want the two apps the wildcard covers", got)
	}

	// The key, in no answer and in no column in the clear.
	secret := keyBody(key)
	if strings.Contains(body, secret) || strings.Contains(body, "PRIVATE KEY") {
		t.Fatal("the upload's answer carries the private key")
	}
	_, list := h.do(acme, http.MethodGet, path, nil)
	if strings.Contains(list, secret) || strings.Contains(list, "PRIVATE KEY") || !strings.Contains(list, "*.example.com") {
		t.Fatalf("the list carries the private key, or not the certificate:\n%s", list)
	}
	rows, err := h.db.QueryContext(t.Context(), `SELECT * FROM certificates`)
	if err != nil {
		t.Fatal(err)
	}
	columns, _ := rows.Columns()
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			text, _ := value.(string)
			if strings.Contains(text, secret) || strings.Contains(text, "PRIVATE KEY") {
				t.Errorf("certificates.%s holds the private key in the clear", columns[i])
			}
		}
	}
	rows.Close()
	// Sealed under the context naming the certificate, and only that.
	_, sealed, err := h.db.CertificateMaterial(t.Context(), acme.team.ID, saved.Certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := h.keyring.Open(sealed, store.CertificateContext(acme.team.ID, saved.Certificate.ID))
	if err != nil || !strings.Contains(string(opened), "PRIVATE KEY") {
		t.Fatalf("the sealed key does not open under its own context: %v", err)
	}
	if _, err := h.keyring.Open(sealed, store.CertificateContext(acme.team.ID, "crt_elsewhere")); err == nil {
		t.Fatal("the sealed key opens under another certificate's context")
	}

	// A domain the certificate covers says which one and until when, with the
	// certificate's state for its status.
	_, domains := h.do(acme, http.MethodGet, "/api/apps/"+shop.ID+"/domains", nil)
	if !strings.Contains(domains, `"name":"Company wildcard"`) || !strings.Contains(domains, `"status":"active"`) {
		t.Errorf("the domain does not say which certificate it uses:\n%s", domains)
	}
	_, domains = h.do(acme, http.MethodGet, "/api/apps/"+other.ID+"/domains", nil)
	if strings.Contains(domains, `"certificate"`) {
		t.Errorf("a domain the certificate does not cover says it uses it:\n%s", domains)
	}

	// Replacing it under the same name keeps its id, and re-applies every app
	// either version covers.
	chain, key = issued(t, "shop.example.com", "other.example.org")
	status, body = h.do(acme, http.MethodPost, path,
		map[string]string{"name": "Company wildcard", "certificate": chain, "private_key": key})
	if status != http.StatusOK || !strings.Contains(body, `"replaced":true`) ||
		!strings.Contains(body, saved.Certificate.ID) || strings.Contains(body, keyBody(key)) {
		t.Fatalf("replacing answered %d\n%s", status, body)
	}
	if got := deployer.waitForSyncs(t, 3); !slices.Equal(got, sortedIDs(shop.ID, blog.ID, other.ID)) {
		t.Errorf("re-applied %v, want every app the old or the new version covers", got)
	}

	// Removing it: the apps it served go back to Let's Encrypt.
	status, body = h.do(acme, http.MethodDelete, path+"/"+saved.Certificate.ID, nil)
	if status != http.StatusOK || strings.Contains(body, keyBody(key)) {
		t.Fatalf("removing answered %d\n%s", status, body)
	}
	if got := deployer.waitForSyncs(t, 2); !slices.Equal(got, sortedIDs(shop.ID, other.ID)) {
		t.Errorf("re-applied %v after removing it", got)
	}
	if _, list := h.do(acme, http.MethodGet, path, nil); !strings.Contains(list, `"total":0`) {
		t.Errorf("the certificate is still listed:\n%s", list)
	}
}

func sortedIDs(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// An administrator's to change, anybody's in the team to read, and nobody
// else's at all.
func TestOnlyAnAdministratorChangesTheTeamsCertificates(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	other := h.newTenant("other")
	member := h.newMember(acme, "bob", store.RoleMember)
	viewer := h.newMember(acme, "val", store.RoleViewer)
	chain, key := issued(t, "shop.example.com")
	path := "/api/teams/" + acme.team.ID + "/certificates"
	upload := map[string]string{"name": "shop", "certificate": chain, "private_key": key}

	if status, body := h.do(member, http.MethodPost, path, upload); status != http.StatusForbidden {
		t.Errorf("a member uploaded a certificate: %d\n%s", status, body)
	}
	status, body := h.do(acme, http.MethodPost, path, upload)
	if status != http.StatusCreated {
		t.Fatalf("the owner could not upload: %d\n%s", status, body)
	}
	var saved struct {
		Certificate struct {
			ID string `json:"id"`
		} `json:"certificate"`
	}
	_ = json.Unmarshal([]byte(body), &saved)

	for _, reader := range []tenant{member, viewer} {
		if status, body := h.do(reader, http.MethodGet, path, nil); status != http.StatusOK || !strings.Contains(body, "shop.example.com") {
			t.Errorf("somebody in the team could not read its certificates: %d\n%s", status, body)
		}
	}
	for _, who := range []tenant{member, viewer} {
		if status, _ := h.do(who, http.MethodDelete, path+"/"+saved.Certificate.ID, nil); status != http.StatusForbidden {
			t.Errorf("somebody who is not an administrator removed a certificate: %d", status)
		}
	}

	// Another team: not found, whichever way it asks.
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, path, nil},
		{http.MethodPost, path, upload},
		{http.MethodDelete, path + "/" + saved.Certificate.ID, nil},
		{http.MethodDelete, "/api/teams/" + other.team.ID + "/certificates/" + saved.Certificate.ID, nil},
	} {
		status, body := h.do(other, request.method, request.path, request.body)
		if status != http.StatusNotFound {
			t.Errorf("%s %s answered another team %d, want 404\n%s", request.method, request.path, status, body)
		}
	}
	if list, _ := h.db.ListCertificates(t.Context(), acme.team.ID); len(list) != 1 {
		t.Errorf("the team has %d certificates, want the one it uploaded", len(list))
	}
}

// Every refusal is the certificate's own, with a code the interface has words
// for, and nothing is stored.
func TestARefusedCertificateIsNotStored(t *testing.T) {
	h := newHarness(t)
	acme := h.newTenant("acme")
	path := "/api/teams/" + acme.team.ID + "/certificates"
	chain, key := issued(t, "shop.example.com")
	_, otherKey := issued(t, "shop.example.com")

	for name, c := range map[string]struct {
		body map[string]string
		code string
	}{
		"no name":           {map[string]string{"name": " ", "certificate": chain, "private_key": key}, "request.invalid"},
		"the wrong key":     {map[string]string{"name": "shop", "certificate": chain, "private_key": otherKey}, "certificate.key_mismatch"},
		"not a certificate": {map[string]string{"name": "shop", "certificate": "hello", "private_key": key}, "certificate.unreadable"},
		"no key":            {map[string]string{"name": "shop", "certificate": chain, "private_key": ""}, "certificate.key_unreadable"},
	} {
		status, body := h.do(acme, http.MethodPost, path, c.body)
		if status != http.StatusBadRequest || !strings.Contains(body, c.code) {
			t.Errorf("%s answered %d, want 400 %s\n%s", name, status, c.code, body)
		}
	}
	if list, _ := h.db.ListCertificates(t.Context(), acme.team.ID); len(list) != 0 {
		t.Errorf("%d refused certificates were stored", len(list))
	}
}

// The ingress serves every certificate in the cluster by name, so a team may
// not name another team's hostname, or the panel's — and a team may not add a
// hostname another team's certificate names.
func TestNoTeamsCertificateNamesAnotherTeamsHostname(t *testing.T) {
	h := newHarness(t)
	h.api.cfg.PublicURL = "https://panel.example.com"
	acme := h.newTenant("acme")
	rival := h.newTenant("rival")
	h.domain(h.app(rival, "store"), "store.example.net")
	path := "/api/teams/" + acme.team.ID + "/certificates"

	for name, hostnames := range map[string][]string{
		"another team's domain": {"acme.example.net", "store.example.net"},
		"the panel's hostname":  {"panel.example.com"},
	} {
		chain, key := issued(t, hostnames...)
		status, body := h.do(acme, http.MethodPost, path,
			map[string]string{"name": name, "certificate": chain, "private_key": key})
		if status != http.StatusConflict || !strings.Contains(body, "certificate.hostname_taken") {
			t.Errorf("a certificate naming %s answered %d\n%s", name, status, body)
		}
	}

	// A wildcard over another team's hostname is fine: an exact name wins
	// over a wildcard wherever both are loaded.
	chain, key := issued(t, "*.example.net")
	if status, body := h.do(acme, http.MethodPost, path,
		map[string]string{"name": "wildcard", "certificate": chain, "private_key": key}); status != http.StatusCreated {
		t.Fatalf("a wildcard was refused: %d\n%s", status, body)
	}
	// The same wildcard from another team is not.
	chain, key = issued(t, "*.example.net")
	if status, body := h.do(rival, http.MethodPost, "/api/teams/"+rival.team.ID+"/certificates",
		map[string]string{"name": "wildcard", "certificate": chain, "private_key": key}); status != http.StatusConflict {
		t.Errorf("a second team's identical wildcard answered %d\n%s", status, body)
	}

	// An exact name in a team's certificate is that team's.
	chain, key = issued(t, "intranet.example.org")
	if status, body := h.do(acme, http.MethodPost, path,
		map[string]string{"name": "intranet", "certificate": chain, "private_key": key}); status != http.StatusCreated {
		t.Fatalf("an exact certificate was refused: %d\n%s", status, body)
	}
	status, body := h.do(rival, http.MethodPost, "/api/apps/"+h.app(rival, "web").ID+"/domains",
		map[string]any{"hostname": "intranet.example.org"})
	if status != http.StatusConflict || !strings.Contains(body, "domain.named_by_certificate") {
		t.Errorf("another team added a hostname this team's certificate names: %d\n%s", status, body)
	}
	// The team itself may, and the domain it adds says so.
	status, body = h.do(acme, http.MethodPost, "/api/apps/"+h.app(acme, "intranet").ID+"/domains",
		map[string]any{"hostname": "intranet.example.org"})
	if status != http.StatusCreated || !strings.Contains(body, `"name":"intranet"`) {
		t.Errorf("the team's own hostname was not served with its certificate: %d\n%s", status, body)
	}
}
