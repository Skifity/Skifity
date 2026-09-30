package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"skifity/internal/cron"
	"skifity/internal/errdoc"
	"skifity/internal/netguard"
	"skifity/internal/store"
	"skifity/internal/templates"
	"skifity/internal/templates/remote"
)

// A team's own template catalogues.
//
// The built-in catalogue is fixed when the binary is built. A team adds its
// own by address, and its templates appear beside the built-in ones for that
// team and nobody else, installed through the same installTemplate. What is
// decided here rather than in internal/templates/remote:
//
//   - Who sees what. A catalogue belongs to one team, and a template is found
//     by the catalogue it is in and the team that owns that catalogue: an id
//     alone finds a built-in template, and a catalogue id from another team
//     finds nothing, exactly as a catalogue that does not exist.
//   - What is kept. A catalogue is saved once it has been downloaded and read,
//     and after that a refresh replaces the copy only with one that reads; one
//     that fails records why and leaves the copy alone. The copy is kept as it
//     was downloaded and read again with this version's rules, so a panel that
//     checks more than the one that fetched it applies what it checks.
//   - Ids. Two catalogues — or a catalogue and the built-in one — may both
//     have a template called wiki. Each is listed under its own catalogue, is
//     installed by naming the catalogue, and an app remembers which one it
//     came from, so an update is looked for there and nowhere else.
//   - Logos. See internal/templates/remote/icon.go: fetched by the panel when
//     the catalogue is, only from the catalogue's own host, served from here.

const (
	// maxTemplateCatalogues is how many catalogues one team may have.
	maxTemplateCatalogues = 20
	// maxCatalogueName is how long a catalogue's name may be; it is the
	// badge on every one of its templates.
	maxCatalogueName = 40
	// catalogueDownloadTimeout bounds adding or refreshing one catalogue,
	// its logos included.
	catalogueDownloadTimeout = 90 * time.Second
	// iconPhaseTimeout bounds the logos of one refresh, all of them.
	iconPhaseTimeout = 30 * time.Second
	// maxIconFetches is how many logos one refresh fetches. The rest keep
	// the ones they had.
	maxIconFetches = 200
)

// catalogueRef names the catalogue a template came from.
type catalogueRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// listedTemplate is a template as the catalogue page lists it: with the
// catalogue it came from, when that is a team's own.
type listedTemplate struct {
	templates.Template
	Catalogue *catalogueRef `json:"catalogue,omitempty"`
}

// catalogueView is a team's catalogue as anybody in the team sees it: how many
// of its templates can be installed, and why each of the others cannot.
type catalogueView struct {
	store.TemplateCatalogue
	Format    string           `json:"format,omitempty"`
	Templates int              `json:"templates"`
	Problems  []remote.Problem `json:"problems"`
}

// catalogueCache keeps each catalogue read, by the fingerprint of its copy,
// so a page of templates is not a gzip and a few hundred YAML files every time
// it is opened.
type catalogueCache struct {
	mu      sync.Mutex
	entries map[string]cachedCatalogue
	// refreshing is set while the daily pass runs, so a slow one is not
	// started again beside itself.
	refreshing atomic.Bool
}

type cachedCatalogue struct {
	sha       string
	catalogue remote.Catalogue
}

func (c *catalogueCache) get(id, sha string) (remote.Catalogue, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok || entry.sha != sha {
		return remote.Catalogue{}, false
	}
	return entry.catalogue, true
}

func (c *catalogueCache) put(id, sha string, catalogue remote.Catalogue) {
	// The logos are in the database; keeping their bytes here as well
	// would be the same pictures twice.
	catalogue.Icons = nil
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedCatalogue{}
	}
	c.entries[id] = cachedCatalogue{sha: sha, catalogue: catalogue}
}

func (c *catalogueCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// readCatalogue returns a catalogue's copy, read.
func (s *Server) readCatalogue(ctx context.Context, row store.TemplateCatalogueRow) (remote.Catalogue, error) {
	if cached, ok := s.catalogues.get(row.ID, row.BodySHA256); ok {
		return cached, nil
	}
	body, sha, err := s.db.TemplateCatalogueCopy(ctx, row.ID)
	if err != nil {
		return remote.Catalogue{}, err
	}
	catalogue, err := remote.Read(body)
	if err != nil {
		return remote.Catalogue{}, err
	}
	s.catalogues.put(row.ID, sha, catalogue)
	return catalogue, nil
}

// view is a catalogue as the API answers it. A copy this version cannot read
// is shown as a problem of the whole catalogue rather than hiding it.
func (s *Server) catalogueView(ctx context.Context, row store.TemplateCatalogueRow) catalogueView {
	view := catalogueView{TemplateCatalogue: row.TemplateCatalogue, Problems: []remote.Problem{}}
	catalogue, err := s.readCatalogue(ctx, row)
	if err != nil {
		view.Problems = append(view.Problems, remote.Problem{File: row.URL, Errors: []string{err.Error()}})
		return view
	}
	view.Format = catalogue.Format
	view.Templates = len(catalogue.Templates)
	view.Problems = append(view.Problems, catalogue.Problems...)
	return view
}

// teamTemplates are the templates a team's own catalogues offer, each naming
// the catalogue it came from.
func (s *Server) teamTemplates(ctx context.Context, teamID string) ([]listedTemplate, error) {
	rows, err := s.db.ListTemplateCatalogues(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := []listedTemplate{}
	for _, row := range rows {
		catalogue, err := s.readCatalogue(ctx, row)
		if err != nil {
			// Listed as a problem on the catalogue itself; its templates
			// are not offered.
			continue
		}
		icons, err := s.db.ListTemplateCatalogueIcons(ctx, row.ID, false)
		if err != nil {
			return nil, err
		}
		ref := &catalogueRef{ID: row.ID, Name: row.Name}
		for _, template := range catalogue.Templates {
			if icon, ok := icons[template.ID]; ok {
				template.Icon = template.ID + iconExtensions[icon.ContentType]
			}
			out = append(out, listedTemplate{Template: template, Catalogue: ref})
		}
	}
	return out, nil
}

// iconExtensions name a logo's file by what it is, for the Icon field.
var iconExtensions = map[string]string{"image/svg+xml": ".svg", "image/png": ".png", "image/webp": ".webp"}

// resolveTemplate finds a template by the catalogue it is in and the team
// installing it. An empty catalogue is the built-in one. A catalogue of
// another team is not found, the same as one that does not exist.
func (s *Server) resolveTemplate(ctx context.Context, teamID, catalogueID, templateID string) (templates.Template, error) {
	if catalogueID == "" {
		tpl, ok := templates.Lookup(templateID)
		if !ok {
			return templates.Template{}, errdoc.NotFound("template", templateID)
		}
		return tpl, nil
	}
	row, err := s.db.GetTemplateCatalogue(ctx, teamID, catalogueID)
	if errors.Is(err, store.ErrNotFound) {
		return templates.Template{}, errdoc.NotFound("template catalogue", catalogueID)
	}
	if err != nil {
		return templates.Template{}, err
	}
	catalogue, err := s.readCatalogue(ctx, row)
	if err != nil {
		return templates.Template{}, errdoc.TemplateCatalogueRefreshFailed(row.Name, err.Error())
	}
	for _, template := range catalogue.Templates {
		if template.ID == templateID {
			// Checked again, here, on the way in: the list it came from
			// was checked when it was read, and this is the one place a
			// template becomes apps.
			if problems := templates.Validate(template); len(problems) > 0 {
				return templates.Template{}, errdoc.TemplateNotInstallable(templateID, row.Name, strings.Join(problems, "; "))
			}
			return template, nil
		}
	}
	for _, problem := range catalogue.Problems {
		if problem.ID == templateID {
			return templates.Template{}, errdoc.TemplateNotInstallable(templateID, row.Name, strings.Join(problem.Errors, "; "))
		}
	}
	return templates.Template{}, errdoc.NotFound("template", templateID)
}

// --- handlers ---

// handleListTeamTemplates answers the catalogue as a team sees it: its own
// catalogues' templates, then the built-in ones.
func (s *Server) handleListTeamTemplates(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// Anybody in the team, including a member limited to some projects: a
	// template is installed into an environment, which is checked then.
	if _, _, err := s.authorizeTeamMember(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.teamTemplates(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, template := range templates.All() {
		out = append(out, listedTemplate{Template: template})
	}
	writeList(w, out)
}

func (s *Server) handleListTemplateCatalogues(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// Where the team's templates come from is the team's settings, which a
	// member limited to some projects is not shown; the templates themselves
	// they are, above.
	if _, err := s.authorizeTeam(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListTemplateCatalogues(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]catalogueView, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.catalogueView(r.Context(), row))
	}
	writeList(w, out)
}

type addCatalogueRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// AuthHeaderName and AuthHeaderValue are the header a private Git host
	// reads a token from. The value is sealed and never answered.
	AuthHeaderName  string `json:"auth_header_name,omitempty"`
	AuthHeaderValue string `json:"auth_header_value,omitempty"`
}

func (s *Server) handleAddTemplateCatalogue(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	// An administrator's: every template in it can be installed by anybody
	// in the team with one click, and the panel fetches it every day.
	user, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req addCatalogueRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxCatalogueName {
		writeError(w, r, errdoc.BadRequest(fmt.Sprintf(
			"A catalogue needs a name of up to %d characters: it is the badge on each of its templates.", maxCatalogueName)))
		return
	}
	address, err := remote.CheckURL(req.URL)
	if err != nil {
		writeError(w, r, errdoc.TemplateCatalogueAddress(capitalise(err.Error())+"."))
		return
	}
	header := remote.Header{Name: strings.TrimSpace(req.AuthHeaderName), Value: req.AuthHeaderValue}
	if err := remote.CheckHeader(header.Name, header.Value); err != nil {
		writeError(w, r, errdoc.TemplateCatalogueHeader(capitalise(err.Error())+"."))
		return
	}

	existing, err := s.db.ListTemplateCatalogues(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(existing) >= maxTemplateCatalogues {
		writeError(w, r, errdoc.TooManyTemplateCatalogues(maxTemplateCatalogues))
		return
	}
	for _, other := range existing {
		if strings.EqualFold(other.Name, name) || other.URL == address.String() {
			writeError(w, r, errdoc.Conflict(
				fmt.Sprintf("This team already has a catalogue called %s or at that address.", other.Name),
				"Pick another name, or refresh the one it has."))
			return
		}
	}

	// Downloaded and read before anything is kept: a catalogue that has
	// never worked is not one to install from.
	ctx, cancel := context.WithTimeout(r.Context(), catalogueDownloadTimeout)
	defer cancel()
	download, err := s.downloadCatalogue(ctx, address, header, nil)
	if err != nil {
		writeError(w, r, addFailure(address, err))
		return
	}

	catalogue := store.TemplateCatalogue{
		ID: store.NewID("tcat"), TeamID: teamID, Name: name, URL: address.String(),
		AuthHeaderName: header.Name, CreatedBy: user.ID,
	}
	sealed := ""
	if header.Value != "" {
		sealed, err = s.keyring.Seal([]byte(header.Value),
			store.TemplateCatalogueContext(teamID, catalogue.ID, catalogue.URL))
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	if err := s.db.CreateTemplateCatalogue(r.Context(), &catalogue, sealed,
		download.body, download.sha, download.icons); err != nil {
		writeError(w, r, err)
		return
	}
	s.catalogues.put(catalogue.ID, download.sha, download.catalogue)
	s.audit(r, teamID, "template_catalogue.added", "team", teamID, name)

	row, err := s.db.GetTemplateCatalogue(r.Context(), teamID, catalogue.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.catalogueView(r.Context(), row))
}

func (s *Server) handleRefreshTemplateCatalogue(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	row, err := s.db.GetTemplateCatalogue(r.Context(), teamID, chi.URLParam(r, "catalogueID"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, errdoc.NotFound("template catalogue", chi.URLParam(r, "catalogueID")))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "template_catalogue.refreshed", "team", teamID, row.Name)
	ctx, cancel := context.WithTimeout(r.Context(), catalogueDownloadTimeout)
	defer cancel()
	if err := s.refreshCatalogue(ctx, row); err != nil {
		writeError(w, r, err)
		return
	}
	row, err = s.db.GetTemplateCatalogue(r.Context(), teamID, row.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.catalogueView(r.Context(), row))
}

func (s *Server) handleDeleteTemplateCatalogue(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "catalogueID")
	row, err := s.db.GetTemplateCatalogue(r.Context(), teamID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, errdoc.NotFound("template catalogue", id))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Apps installed from it are ordinary apps and stay; an update for them
	// is no longer offered, because there is nowhere to look for one.
	if err := s.db.DeleteTemplateCatalogue(r.Context(), teamID, id); err != nil {
		writeError(w, r, err)
		return
	}
	s.catalogues.forget(id)
	s.audit(r, teamID, "template_catalogue.removed", "team", teamID, row.Name)
	writeOK(w)
}

// authorizeTemplateCatalogue checks the caller may see a catalogue named by
// its id alone, and answers a catalogue of a team they are not in exactly as
// one that does not exist — not with the other team's id, which is what
// authorizeTeamMember would name.
func (s *Server) authorizeTemplateCatalogue(r *http.Request, catalogueID string, required store.Role) (store.TemplateCatalogueRow, error) {
	row, err := s.db.GetTemplateCatalogueByID(r.Context(), catalogueID)
	if errors.Is(err, store.ErrNotFound) {
		return store.TemplateCatalogueRow{}, errdoc.NotFound("template catalogue", catalogueID)
	}
	if err != nil {
		return store.TemplateCatalogueRow{}, err
	}
	if _, _, err := s.authorizeTeamMember(r, row.TeamID, required); err != nil {
		return store.TemplateCatalogueRow{}, errdoc.NotFound("template catalogue", catalogueID)
	}
	return row, nil
}

// handleTemplateCatalogueIcon serves a logo a team's catalogue named, from
// the copy the panel fetched — never by sending the browser to its host. It
// answers GET /api/templates/{templateID}/icon?catalogue=, the same address
// as a built-in logo, for anybody in the catalogue's team.
func (s *Server) handleTemplateCatalogueIcon(w http.ResponseWriter, r *http.Request, catalogueID, templateID string) {
	if _, err := s.authorizeTemplateCatalogue(r, catalogueID, store.RoleViewer); err != nil {
		// The caller is an <img> tag, which cannot read an errdoc.
		http.NotFound(w, r)
		return
	}
	icon, err := s.db.GetTemplateCatalogueIcon(r.Context(), catalogueID, templateID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Checked again on the way out: the type is what the bytes are, and the
	// only three the panel serves.
	if contentType, ok := remote.Sniff(icon.Body); !ok || contentType != icon.ContentType {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", icon.ContentType)
	// Private to the person asking: it is the team's, and a shared cache
	// in front of the panel is not a member of it.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	// It is an SVG from a third party, so it is served as a picture and
	// never as a document: no script in it can run against this origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(icon.Body))
}

// --- downloading and refreshing ---

// catalogueDownload is a catalogue downloaded and read, with its logos.
type catalogueDownload struct {
	body      []byte
	sha       string
	catalogue remote.Catalogue
	icons     []store.TemplateIcon
}

// downloadCatalogue downloads a catalogue, reads it, and fetches the logos
// its templates name. previous are the logos it had, which a logo that cannot
// be fetched this time keeps.
func (s *Server) downloadCatalogue(ctx context.Context, address *url.URL, header remote.Header,
	previous map[string]store.TemplateIcon) (catalogueDownload, error) {
	body, err := s.catalogueFetcher.Fetch(ctx, address.String(), header, remote.MaxDownloadBytes)
	if err != nil {
		return catalogueDownload{}, err
	}
	catalogue, err := remote.Read(body)
	if err != nil {
		return catalogueDownload{}, err
	}
	sum := sha256.Sum256(body)
	download := catalogueDownload{body: body, sha: hex.EncodeToString(sum[:]), catalogue: catalogue}

	iconCtx, cancel := context.WithTimeout(ctx, iconPhaseTimeout)
	defer cancel()
	fetched := 0
	for _, template := range catalogue.Templates {
		if icon, ok := catalogue.Icons[template.ID]; ok {
			download.icons = append(download.icons, store.TemplateIcon{
				TemplateID: template.ID, ContentType: icon.ContentType, Body: icon.Body})
			continue
		}
		ref, ok := catalogue.IconRefs[template.ID]
		if !ok {
			continue
		}
		if fetched < maxIconFetches && iconCtx.Err() == nil {
			fetched++
			if icon, ok := s.fetchIcon(iconCtx, address, ref, header); ok {
				icon.TemplateID = template.ID
				download.icons = append(download.icons, icon)
				continue
			}
		}
		if icon, ok := previous[template.ID]; ok {
			download.icons = append(download.icons, icon)
		}
	}
	return download, nil
}

// fetchIcon fetches one logo from the catalogue's own host and keeps it only
// when its bytes are a picture.
func (s *Server) fetchIcon(ctx context.Context, catalogue *url.URL, ref string, header remote.Header) (store.TemplateIcon, bool) {
	address, err := remote.ResolveIconURL(catalogue, ref)
	if err != nil {
		s.log.Debug("a catalogue's logo is not fetched", "reason", err.Error())
		return store.TemplateIcon{}, false
	}
	body, err := s.catalogueFetcher.Fetch(ctx, address.String(), header, remote.MaxIconBytes)
	if err != nil {
		s.log.Debug("a catalogue's logo could not be fetched", "host", address.Host, "error", err)
		return store.TemplateIcon{}, false
	}
	contentType, ok := remote.Sniff(body)
	if !ok {
		return store.TemplateIcon{}, false
	}
	return store.TemplateIcon{ContentType: contentType, Body: body}, true
}

// refreshCatalogue downloads a catalogue again and keeps the new copy only if
// it reads. A failure is recorded on the catalogue and answered, and the copy
// it had is what its templates are still installed from.
func (s *Server) refreshCatalogue(ctx context.Context, row store.TemplateCatalogueRow) error {
	header := remote.Header{Name: row.AuthHeaderName}
	if row.SealedAuth != "" {
		value, err := s.keyring.Open(row.SealedAuth, store.TemplateCatalogueContext(row.TeamID, row.ID, row.URL))
		if err != nil {
			reason := "the header it is fetched with could not be opened; remove the catalogue and add it again"
			_ = s.db.RecordTemplateCatalogueFailure(ctx, row.ID, reason)
			return errdoc.TemplateCatalogueRefreshFailed(row.Name, reason)
		}
		header.Value = string(value)
	}
	address, err := url.Parse(row.URL)
	if err != nil {
		return errdoc.TemplateCatalogueRefreshFailed(row.Name, "its address cannot be read")
	}
	previous, err := s.db.ListTemplateCatalogueIcons(ctx, row.ID, true)
	if err != nil {
		return err
	}
	download, err := s.downloadCatalogue(ctx, address, header, previous)
	if err != nil {
		reason := describeCatalogueFailure(err)
		// Recorded with a context of its own: the request's may be the
		// thing that ran out.
		if recordErr := s.db.RecordTemplateCatalogueFailure(context.WithoutCancel(ctx), row.ID, reason); recordErr != nil {
			return recordErr
		}
		return errdoc.TemplateCatalogueRefreshFailed(row.Name, reason)
	}
	if err := s.db.SaveTemplateCatalogueCopy(ctx, row.ID, download.body, download.sha, download.icons); err != nil {
		return err
	}
	s.catalogues.put(row.ID, download.sha, download.catalogue)
	return nil
}

// addFailure is why a catalogue being added was not kept.
func addFailure(address *url.URL, err error) error {
	if errors.Is(err, remote.ErrUnreadable) {
		return errdoc.TemplateCatalogueUnreadable(address.String(), describeCatalogueFailure(err))
	}
	return errdoc.TemplateCatalogueFetchFailed(address.String(), describeCatalogueFailure(err))
}

// describeCatalogueFailure is a failure as the sentence stored on the
// catalogue and shown to the team.
func describeCatalogueFailure(err error) string {
	var blocked *netguard.Blocked
	if errors.As(err, &blocked) {
		return blocked.Error()
	}
	message := err.Error()
	// Long enough for what went wrong, short enough that a server answering
	// an essay does not put it on the page.
	if len(message) > 500 {
		message = message[:497] + "..."
	}
	return message
}

// --- the daily refresh ---

// catalogueRefreshSchedule is when a catalogue is refreshed: once a day, at a
// minute between two and six in the morning (UTC) picked from its id, so a
// panel with many catalogues does not fetch them all in the same minute, and
// a host serving many panels is not asked by all of them at once.
func catalogueRefreshSchedule(id string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	sum := hash.Sum32()
	return fmt.Sprintf("%d %d * * *", sum%60, 2+(sum/60)%4)
}

// catalogueDue says whether a catalogue is refreshed in this minute: at its
// minute of the day, or as soon as it is a day and an hour since it was last
// tried, which is a panel that was not running at its minute catching up. One
// tried within the hour — by hand, or by a catch-up — is left alone.
func catalogueDue(row store.TemplateCatalogueRow, now time.Time) bool {
	since := now.Sub(row.AttemptedAt)
	if since < time.Hour {
		return false
	}
	return cron.DueNow(catalogueRefreshSchedule(row.ID), now) || since >= 25*time.Hour
}

// RefreshTemplateCatalogues refreshes every team's catalogues that are due.
// The minute tick calls it beside itself; a pass that is still running when
// the next minute comes is not started twice.
func (s *Server) RefreshTemplateCatalogues(ctx context.Context, now time.Time) {
	if !s.catalogues.refreshing.CompareAndSwap(false, true) {
		return
	}
	defer s.catalogues.refreshing.Store(false)

	rows, err := s.db.ListAllTemplateCatalogues(ctx)
	if err != nil {
		s.log.Warn("could not list the template catalogues to refresh", "error", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if !catalogueDue(row, now) {
			continue
		}
		refreshCtx, cancel := context.WithTimeout(ctx, catalogueDownloadTimeout)
		err := s.refreshCatalogue(refreshCtx, row)
		cancel()
		if err != nil {
			s.log.Warn("a template catalogue could not be refreshed; its last good copy is kept",
				"catalogue", row.ID, "team", row.TeamID, "error", errdoc.From(err).Cause)
			continue
		}
		s.log.Info("refreshed a template catalogue", "catalogue", row.ID, "team", row.TeamID)
	}
}
