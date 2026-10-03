package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"skifity/internal/cloud"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// A team's connections to a cloud provider, and creating a server with one.
//
// A connection is a team's, and its administrators add and remove it: it is a
// token to a project the team pays for. Ordering a machine and joining it to
// the cluster is a panel administrator's, like adding any server (see
// serverAdmin), because the machine is given the cluster's join token. The
// options a form offers are read with the team's token, so they are an
// administrator's too, as a Git connection's repositories are.

// cloudCheckTimeout bounds asking a provider something while a person waits.
const cloudCheckTimeout = 20 * time.Second

// cloudProviderView is a connection as the API shows it. The token is never
// in it; its last four characters are, to tell two apart.
type cloudProviderView struct {
	store.CloudProvider
	Title string `json:"title"`
}

func viewCloudProvider(row store.CloudProviderRow) cloudProviderView {
	return cloudProviderView{CloudProvider: row.CloudProvider, Title: cloud.Title(row.Kind)}
}

func (s *Server) handleListCloudProviders(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleViewer); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := s.db.ListCloudProviders(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]cloudProviderView, 0, len(rows))
	for _, row := range rows {
		out = append(out, viewCloudProvider(row))
	}
	writeList(w, out)
}

type addCloudProviderRequest struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

func (s *Server) handleAddCloudProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req addCloudProviderRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind == "" {
		req.Kind = cloud.KindHetzner
	}
	if !cloud.Supported(req.Kind) {
		writeError(w, r, errdoc.BadRequest("The panel can create servers at Hetzner Cloud (hetzner) and DigitalOcean (digitalocean)."))
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || len(token) > 512 || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		writeError(w, r, errdoc.BadRequest("Paste the project's API token, on its own, with no spaces in it."))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cloud.Title(req.Kind)
	}
	if len(name) > 64 {
		writeError(w, r, errdoc.BadRequest("A connection's name is 64 characters at most."))
		return
	}

	// Asked before it is saved, write permission included: a token that is
	// wrong, or can only read, is found now rather than at the first server.
	provider, err := s.openCloud(req.Kind, token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudCheckTimeout)
	defer cancel()
	if err := provider.Check(ctx); err != nil {
		writeError(w, r, err)
		return
	}

	connection := store.CloudProvider{
		ID: store.NewCloudProviderID(), TeamID: teamID, Kind: req.Kind, Name: name,
		TokenHint: token[max(0, len(token)-4):], CheckedAt: time.Now().UTC(), CreatedBy: user.ID,
	}
	sealed, err := s.keyring.Seal([]byte(token), store.CloudProviderContext(connection.ID))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.db.CreateCloudProvider(r.Context(), &connection, sealed); err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = errdoc.BadRequest("The team already has a cloud connection called " + name + ". Give this one another name.").
				WithStatus(http.StatusConflict)
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "cloud_provider.added", "cloud_provider", connection.ID, req.Kind+" "+name)
	writeJSON(w, http.StatusCreated, viewCloudProvider(store.CloudProviderRow{CloudProvider: connection}))
}

func (s *Server) handleDeleteCloudProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	row, err := s.cloudProvider(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A server the panel created with it can only have its machine deleted
	// through it. Keeping the connection is keeping that possible.
	if row.Servers > 0 {
		writeError(w, r, errdoc.CloudProviderInUse(row.Servers))
		return
	}
	if err := s.db.DeleteCloudProvider(r.Context(), teamID, row.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "cloud_provider.removed", "cloud_provider", row.ID, row.Kind+" "+row.Name)
	writeOK(w)
}

func (s *Server) handleTestCloudProvider(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	row, provider, err := s.openCloudProvider(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudCheckTimeout)
	defer cancel()
	if err := provider.Check(ctx); err != nil {
		writeError(w, r, err)
		return
	}
	row.CheckedAt = time.Now().UTC()
	if err := s.db.TouchCloudProviderChecked(r.Context(), row.ID, row.CheckedAt); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewCloudProvider(row))
}

// cloudOptions is what the create form offers.
type cloudOptions struct {
	cloud.Catalogue
	DefaultImage string `json:"default_image"`
}

func (s *Server) handleCloudProviderOptions(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	_, provider, err := s.openCloudProvider(r, teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudCheckTimeout)
	defer cancel()
	catalogue, err := provider.Catalogue(ctx)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cloudOptions{Catalogue: catalogue, DefaultImage: cloud.DefaultImage})
}

// machineName is a name a provider, a hostname and a Kubernetes node all
// accept: RFC 1123's label, lower case.
var machineName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (s *Server) handleCreateCloudServer(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err == nil {
		err = serverAdmin(user)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req CreateCloudServerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TeamID, req.CreatedBy = teamID, user.ID
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	req.Location = strings.TrimSpace(req.Location)
	req.ServerType = strings.TrimSpace(req.ServerType)
	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		req.Image = cloud.DefaultImage
	}
	if req.SSHAccess == "" {
		req.SSHAccess = store.SSHFromAnywhere
	}
	switch {
	case !machineName.MatchString(req.Name):
		err = errdoc.BadRequest("A server's name is its hostname: up to 63 lower-case letters, digits and hyphens, starting and ending with a letter or a digit.")
	case req.Location == "" || req.ServerType == "":
		err = errdoc.BadRequest("Pick a location and a server type.")
	case !slices.Contains(cloud.SupportedImages, req.Image):
		err = errdoc.BadRequest("The image is one of " + strings.Join(cloud.SupportedImages, ", ") + ".")
	case req.SSHAccess != store.SSHFromAnywhere && req.SSHAccess != store.SSHFromCluster:
		err = errdoc.BadRequest("SSH access is anywhere or cluster.")
	case req.SSHAccess == store.SSHFromCluster && s.cluster == nil:
		// The cluster's addresses are what port 22 would be open to, and a
		// panel that is not connected to a cluster is not inside one.
		err = errdoc.BadRequest("SSH can be limited to the cluster's servers only when the panel runs in the cluster; leave it open to anywhere.")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	row, err := s.db.GetCloudProvider(r.Context(), teamID, req.ProviderID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = errdoc.NotFound("cloud connection", req.ProviderID)
		}
		writeError(w, r, err)
		return
	}
	if err := s.nameIsFree(r, req.Name); err != nil {
		writeError(w, r, err)
		return
	}

	// Checked against what the provider sells now, so a type that is not sold
	// in that location is a sentence here rather than an order that fails.
	// Before the capability check below, like adding a server's validation:
	// one problem at a time is two attempts where one would do.
	provider, err := s.openStoredCloud(row)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudCheckTimeout)
	defer cancel()
	catalogue, err := provider.Catalogue(ctx)
	if err != nil {
		writeError(w, r, err)
		return
	}
	arch, err := checkCloudOrder(catalogue, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.Arch = arch
	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server provisioning", "the panel's cluster connection"))
		return
	}

	op, err := s.provisioner.CreateCloudServer(r.Context(), req)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = errdoc.CloudNameTaken(req.Name)
		}
		writeError(w, r, err)
		return
	}
	s.audit(r, teamID, "server.create_started", "server", op.TargetID,
		row.Kind+" "+req.Location+" "+req.ServerType+" "+req.Name)
	writeJSON(w, http.StatusAccepted, op)
}

// checkCloudOrder holds an order to the catalogue and returns the machine's
// architecture.
func checkCloudOrder(catalogue cloud.Catalogue, req CreateCloudServerRequest) (string, error) {
	if !slices.ContainsFunc(catalogue.Locations, func(l cloud.Location) bool { return l.Name == req.Location }) {
		return "", errdoc.BadRequest("The provider has no location called " + req.Location + ".")
	}
	i := slices.IndexFunc(catalogue.ServerTypes, func(t cloud.ServerType) bool { return t.Name == req.ServerType })
	if i < 0 {
		return "", errdoc.BadRequest("The provider does not sell a server type called " + req.ServerType + ".")
	}
	serverType := catalogue.ServerTypes[i]
	if !serverType.AvailableIn(req.Location) {
		return "", errdoc.BadRequest(req.ServerType + " cannot be ordered in " + req.Location + " right now. Pick another type or location.")
	}
	if !slices.ContainsFunc(catalogue.Images, func(image cloud.Image) bool {
		return image.Name == req.Image && image.Arch == serverType.Arch
	}) {
		return "", errdoc.BadRequest(req.Image + " is not offered for " + serverType.Arch + " servers.")
	}
	return serverType.Arch, nil
}

// nameIsFree refuses a name the cluster already has, as a server or a node.
func (s *Server) nameIsFree(r *http.Request, name string) error {
	servers, err := s.db.ListAllServers(r.Context())
	if err != nil {
		return err
	}
	for _, server := range servers {
		if strings.EqualFold(server.Name, name) || strings.EqualFold(server.NodeName, name) {
			return errdoc.CloudNameTaken(name)
		}
	}
	if s.cluster != nil {
		if summary, err := s.cluster.Summary(r.Context()); err == nil {
			for _, node := range summary.Nodes {
				if strings.EqualFold(node.Name, name) {
					return errdoc.CloudNameTaken(name)
				}
			}
		}
	}
	return nil
}

// cloudProvider reads the connection in the path, which must be the team's.
func (s *Server) cloudProvider(r *http.Request, teamID string) (store.CloudProviderRow, error) {
	id := chi.URLParam(r, "providerID")
	row, err := s.db.GetCloudProvider(r.Context(), teamID, id)
	if errors.Is(err, store.ErrNotFound) {
		return row, errdoc.NotFound("cloud connection", id)
	}
	return row, err
}

// openCloudProvider opens the connection in the path with its stored token.
func (s *Server) openCloudProvider(r *http.Request, teamID string) (store.CloudProviderRow, cloud.Provider, error) {
	row, err := s.cloudProvider(r, teamID)
	if err != nil {
		return row, nil, err
	}
	provider, err := s.openStoredCloud(row)
	return row, provider, err
}

// openStoredCloud unseals a connection's token and opens it.
func (s *Server) openStoredCloud(row store.CloudProviderRow) (cloud.Provider, error) {
	token, err := s.keyring.Open(row.SealedToken, store.CloudProviderContext(row.ID))
	if err != nil {
		return nil, err
	}
	return s.openCloud(row.Kind, string(token))
}
