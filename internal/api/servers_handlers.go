package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	servers, err := s.db.ListServers(r.Context(), teamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeList(w, servers)
}

func (s *Server) handleAddServer(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	user, err := s.authorizeTeam(r, teamID, store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req AddServerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TeamID = teamID
	req.CreatedBy = user.ID
	// Validated before the capability check below. The request is checked
	// whether or not provisioning is configured, so somebody fixing one
	// problem does not then discover the other on the next attempt — and
	// because a check that only runs on a configured panel is a check that
	// cannot be tested without one.
	if err := validateAddServer(&req); err != nil {
		writeError(w, r, err)
		return
	}
	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server provisioning", "the panel's cluster connection"))
		return
	}

	op, err := s.provisioner.AddServer(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The host is audited; the password and key never are.
	s.audit(r, teamID, "server.add_started", "server", op.TargetID, req.Host)
	writeJSON(w, http.StatusAccepted, op)
}

func validateAddServer(req *AddServerRequest) error {
	req.Host = strings.TrimSpace(req.Host)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	req.Name = strings.TrimSpace(req.Name)

	if req.Host == "" {
		return errdoc.BadRequest("Enter the server's IP address or hostname.")
	}
	if strings.ContainsAny(req.Host, " \t\n\r;|&$`") {
		// The host ends up in commands and in SSH config; refuse anything that
		// could change their meaning.
		return errdoc.BadRequest("That host contains characters that are not allowed.")
	}
	if req.SSHUser == "" {
		req.SSHUser = "root"
	}
	// The user names a home directory and is passed to chown in a script that
	// runs as root on the server being added. Every value in those scripts is
	// quoted now, and this is the other half: a Unix account name has a shape,
	// and anything outside it was never going to sign in anyway.
	if !validUnixName(req.SSHUser) {
		return errdoc.BadRequest("That SSH user is not a valid account name.")
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if req.SSHPort < 1 || req.SSHPort > 65535 {
		return errdoc.BadRequest("The SSH port must be between 1 and 65535.")
	}
	if req.Password == "" && req.PrivateKey == "" {
		return errdoc.BadRequest("Enter either a password or a private key so Skifity can sign in once to set the server up.")
	}
	if req.Name == "" {
		req.Name = req.Host
	}
	return nil
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Include the latest operation so a reopened page can resume its progress
	// view without a second request.
	type serverDetail struct {
		store.Server
		Operation *store.Operation `json:"operation,omitempty"`
	}
	detail := serverDetail{Server: server}
	if op, err := s.db.LatestOperation(r.Context(), "server", server.ID); err == nil {
		detail.Operation = &op
	}
	writeJSON(w, http.StatusOK, detail)
}

type updateServerRequest struct {
	Name     *string `json:"name,omitempty"`
	Location *string `json:"location,omitempty"`
	Size     *string `json:"size,omitempty"`
}

func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateServerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		server.Name = strings.TrimSpace(*req.Name)
	}
	if err := s.db.UpdateServer(r.Context(), &server); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, server.TeamID, "server.updated", "server", server.ID, server.Name)
	writeJSON(w, http.StatusOK, server)
}

func (s *Server) handleRemoveServer(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A node the panel adopted rather than installed cannot be removed this
	// way: there is no key to it, so the drain-and-uninstall the removal does
	// would fail at the connection with a message about SSH.
	if server.Adopted {
		writeError(w, r, errdoc.ServerNotOurs(server.Name, "Removing the server"))
		return
	}
	// Removing a control plane node can break etcd quorum, which takes the
	// whole cluster down. Refuse before touching anything.
	//
	// Counted from the cluster, not from the panel's own table. Those are
	// different numbers: a panel installed by install.sh runs in a cluster it
	// has no server row for, and on a panel with more than one team the rows
	// are split between them. The old count was low by at least one and scoped
	// to one team, which refused removals that were safe and — when it reached
	// zero — permitted the one that ends the cluster.
	if server.Role == "control-plane" {
		if s.cluster == nil {
			writeError(w, r, errdoc.ControlPlaneUnverifiable())
			return
		}
		count, err := s.cluster.ControlPlaneCount(r.Context())
		if err != nil {
			// Not the underlying error: "the cluster is unreachable" is true
			// and does not say why it stops this particular action.
			writeError(w, r, errdoc.ControlPlaneUnverifiable())
			return
		}
		switch remaining := count - 1; {
		case remaining <= 0:
			// The end of the cluster, and of the panel reporting it.
			writeError(w, r, errdoc.LastControlPlane())
			return
		case remaining < 3:
			writeError(w, r, errdoc.QuorumRisk(remaining))
			return
		}
	}

	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server management", "the panel's cluster connection"))
		return
	}
	op, err := s.provisioner.RemoveServer(r.Context(), server.ID, queryBool(r, "wipe"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, server.TeamID, "server.remove_started", "server", server.ID, server.Host)
	writeJSON(w, http.StatusAccepted, op)
}

func (s *Server) handleRetryServer(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if server.Adopted {
		writeError(w, r, errdoc.ServerNotOurs(server.Name, "Retrying"))
		return
	}
	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server provisioning", "the panel's cluster connection"))
		return
	}
	op, err := s.provisioner.RetryServer(r.Context(), server.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, server.TeamID, "server.retry", "server", server.ID, server.Host)
	writeJSON(w, http.StatusAccepted, op)
}

func (s *Server) handlePromoteServer(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleAdmin)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if server.Adopted {
		writeError(w, r, errdoc.ServerNotOurs(server.Name, "Promoting the server"))
		return
	}
	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server provisioning", "the panel's cluster connection"))
		return
	}
	if server.Role == "control-plane" {
		writeError(w, r, errdoc.BadRequest("This server is already a control plane server."))
		return
	}
	servers, err := s.db.ListServers(r.Context(), server.TeamID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if len(servers) < 3 {
		writeError(w, r, errdoc.New("cluster.not_enough_servers", "High availability needs three servers").
			WithCause("This cluster has %d servers. Embedded etcd needs three control plane members to survive one of them failing.", len(servers)).
			WithImpact("Nothing was changed.").
			WithFix("Add servers until you have at least three, then promote three of them.").
			WithDocs("/docs/adding-servers#control-plane-servers").
			WithStatus(http.StatusConflict))
		return
	}
	op, err := s.provisioner.PromoteServer(r.Context(), server.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, server.TeamID, "server.promote_started", "server", server.ID, server.Host)
	writeJSON(w, http.StatusAccepted, op)
}

func (s *Server) handleServerMetrics(w http.ResponseWriter, r *http.Request) {
	server, _, err := s.authorizeServer(r, chi.URLParam(r, "serverID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeError(w, r, errdoc.ClusterUnreachable(nil))
		return
	}
	summary, err := s.cluster.Summary(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, node := range summary.Nodes {
		if node.Name == server.NodeName {
			writeJSON(w, http.StatusOK, node)
			return
		}
	}
	writeError(w, r, errdoc.New("server.not_in_cluster", "This server is not in the cluster yet").
		WithCause("No Kubernetes node matches %s.", server.Name).
		WithImpact("There are no live metrics to show.").
		WithFix("If the server is still being added, wait for it to finish. If it failed, open it and press Retry.").
		WithStatus(http.StatusNotFound))
}

func (s *Server) handleClusterSummary(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "teamID")
	if _, err := s.authorizeTeam(r, teamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	if s.cluster == nil {
		writeJSON(w, http.StatusOK, ClusterSummary{
			Reachable: false,
			Message:   "The panel is not connected to a cluster.",
		})
		return
	}
	summary, err := s.cluster.Summary(r.Context())
	if err != nil {
		// A cluster that is briefly unreachable is a status to show, not an
		// error page: the rest of the panel still works.
		writeJSON(w, http.StatusOK, ClusterSummary{Reachable: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.db.GetOperation(r.Context(), chi.URLParam(r, "operationID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := s.authorizeTeam(r, op.TeamID, store.RoleMember); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (s *Server) handleCancelOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.db.GetOperation(r.Context(), chi.URLParam(r, "operationID"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if _, err := s.authorizeTeam(r, op.TeamID, store.RoleAdmin); err != nil {
		writeError(w, r, err)
		return
	}
	if s.provisioner == nil {
		writeError(w, r, errdoc.NotConfigured("Server provisioning", "the panel's cluster connection"))
		return
	}
	if err := s.provisioner.Cancel(r.Context(), op.ID); err != nil {
		writeError(w, r, err)
		return
	}
	s.audit(r, op.TeamID, "operation.cancelled", op.TargetType, op.TargetID, op.Kind)
	writeOK(w)
}

// validUnixName reports whether a string is shaped like a Unix account.
//
// Deliberately stricter than what a system will accept: useradd allows a great
// deal, and none of it is anything somebody signs into a server with. What is
// left is what every distribution's own tooling produces.
func validUnixName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		case r == '-' && i > 0:
		case r == '.' && i > 0:
		default:
			return false
		}
	}
	// A name starting with a digit is legal and is also how a numeric uid gets
	// mistaken for one, so it goes with the rest.
	return !(name[0] >= '0' && name[0] <= '9')
}
