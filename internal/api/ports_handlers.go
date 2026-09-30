package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// An app's public ports: connections that are not HTTP. See
// internal/kube/ports.go for how they are opened.

// portAnswer is a port and the addresses it is reached at.
type portAnswer struct {
	store.AppPort
	// Addresses are the team's servers' public addresses at this port. Empty
	// when the panel knows none: any server's address works.
	Addresses []string `json:"addresses"`
}

func (s *Server) handleListPorts(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleViewer)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ports, err := s.db.ListPorts(r.Context(), app.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	hosts := s.publicHosts(r, app)
	out := make([]portAnswer, 0, len(ports))
	for _, p := range ports {
		out = append(out, portAnswer{AppPort: p, Addresses: addressesAt(hosts, p.PublicPort)})
	}
	writeList(w, out)
}

// publicHosts are the addresses of the servers of the app's team, which is
// where a public port is reached. Another team's servers are not named.
func (s *Server) publicHosts(r *http.Request, app store.App) []string {
	teamID, err := s.db.TeamIDForApp(r.Context(), app.ID)
	if err != nil {
		return nil
	}
	servers, err := s.db.ListServers(r.Context(), teamID)
	if err != nil {
		return nil
	}
	var hosts []string
	for _, server := range servers {
		if server.ExternalIP != "" {
			hosts = append(hosts, server.ExternalIP)
		}
	}
	return hosts
}

func addressesAt(hosts []string, port int) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		out = append(out, host+":"+strconv.Itoa(port))
	}
	return out
}

type addPortRequest struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	// PublicPort is the port's own number when left out, which is what a
	// game client or an MQTT library expects to dial.
	PublicPort int `json:"public_port,omitempty"`
}

func (s *Server) handleAddPort(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req addPortRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, r, errdoc.BadRequest("The port is the one the app listens on, between 1 and 65535."))
		return
	}
	public := req.PublicPort
	if public == 0 {
		public = req.Port
	}
	if err := kube.ValidatePublicPort(public, protocol); err != nil {
		writeError(w, r, errdoc.BadRequest(capitalise(err.Error())+"."))
		return
	}
	port := store.AppPort{AppID: app.ID, Port: req.Port, Protocol: protocol, PublicPort: public}
	if err := s.db.AddPort(r.Context(), &port); err != nil {
		if errors.Is(err, store.ErrPortTaken) {
			writeError(w, r, errdoc.PortTaken(public, protocol))
			return
		}
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not open a port", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "port.opened", "app", app.ID, strconv.Itoa(public)+"/"+protocol)
	writeJSON(w, http.StatusCreated, portAnswer{AppPort: port, Addresses: addressesAt(s.publicHosts(r, app), public)})
}

func (s *Server) handleDeletePort(w http.ResponseWriter, r *http.Request) {
	app, _, err := s.authorizeApp(r, chi.URLParam(r, "appID"), store.RoleMember)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := chi.URLParam(r, "portID")
	label := id
	if ports, err := s.db.ListPorts(r.Context(), app.ID); err == nil {
		for _, p := range ports {
			if p.ID == id {
				label = strconv.Itoa(p.PublicPort) + "/" + p.Protocol
			}
		}
	}
	if err := s.db.DeletePort(r.Context(), app.ID, id); err != nil {
		writeError(w, r, err)
		return
	}
	if s.deployer != nil {
		if err := s.deployer.Sync(r.Context(), app.ID); err != nil {
			s.log.Warn("could not close a port", "app", app.ID, "error", err)
		}
	}
	teamID, _ := s.db.TeamIDForApp(r.Context(), app.ID)
	s.audit(r, teamID, "port.closed", "app", app.ID, label)
	writeOK(w)
}
