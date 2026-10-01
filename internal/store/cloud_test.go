package store

import (
	"testing"
)

// A connection a server was created with cannot be removed under it, and a
// team that goes takes both with it.
func TestACloudConnectionOutlivesItsServersAndNotItsTeam(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()

	team := Team{Name: "Acme", Slug: "acme"}
	if err := db.CreateTeam(ctx, &team); err != nil {
		t.Fatal(err)
	}
	connection := CloudProvider{ID: NewCloudProviderID(), TeamID: team.ID, Kind: "hetzner", Name: "eu", TokenHint: "0000"}
	if err := db.CreateCloudProvider(ctx, &connection, "sealed-not-a-real-envelope"); err != nil {
		t.Fatal(err)
	}
	server := Server{TeamID: team.ID, Name: "web-1", Host: "web-1"}
	if err := db.CreateServer(ctx, &server); err != nil {
		t.Fatal(err)
	}
	created := CloudServer{ServerID: server.ID, ProviderID: connection.ID, Location: "fsn1", ServerType: "cx22", Image: "ubuntu-24.04"}
	if err := db.CreateCloudServer(ctx, &created); err != nil {
		t.Fatal(err)
	}

	row, err := db.GetCloudProvider(ctx, team.ID, connection.ID)
	if err != nil || row.Servers != 1 || row.SealedToken != "sealed-not-a-real-envelope" {
		t.Fatalf("the connection reads %+v, %v", row, err)
	}
	if _, err := db.GetCloudProvider(ctx, "team_other", connection.ID); err == nil {
		t.Error("another team read the connection")
	}
	if err := db.DeleteCloudProvider(ctx, team.ID, connection.ID); err == nil {
		t.Error("a connection a server was created with was deleted under it")
	}

	read, err := db.GetCloudServer(ctx, server.ID)
	if err != nil || read.ProviderKind != "hetzner" || read.SSHAccess != SSHFromAnywhere {
		t.Fatalf("the created server reads %+v, %v", read, err)
	}

	// The server goes, and its record with it; the connection is free.
	if err := db.DeleteServer(ctx, server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetCloudServer(ctx, server.ID); err == nil {
		t.Error("the created server's record outlived the server")
	}

	// A team deleted with a connection and a created server goes in one.
	again := Server{TeamID: team.ID, Name: "web-2", Host: "web-2"}
	if err := db.CreateServer(ctx, &again); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateCloudServer(ctx, &CloudServer{ServerID: again.ID, ProviderID: connection.ID,
		Location: "fsn1", ServerType: "cx22", Image: "ubuntu-24.04"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, team.ID); err != nil {
		t.Fatalf("deleting the team: %v", err)
	}
	if listed, err := db.ListCloudServers(ctx); err != nil || len(listed) != 0 {
		t.Errorf("after the team: %d created servers, %v", len(listed), err)
	}
}
