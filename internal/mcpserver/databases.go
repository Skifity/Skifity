package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// Managed databases and backups.
//
// Two routes are deliberately not reached from here.
//
// GET /api/databases/{id}/credentials answers the password. An assistant has
// no use for it that linking does not serve better — link_database hands the
// app its connection string as a secret variable — and a password that has
// been in an assistant's context is in its provider's logs, in a transcript
// somebody pastes into an issue, and in whatever the next prompt injection
// asks it to repeat. None of that can be taken back. The panel already
// refuses the route to a token with scopes; this refuses it to every token.
// Somebody who needs the password opens the panel, or `skifity db connect`,
// where they read it themselves.
//
// POST /api/databases/{id}/restore/{backup} and the volume equivalent replace
// everything in a database or on a disk with an older copy. There is no undo,
// and the panel asks for the confirmation in words, from a person, on purpose
// (docs/backups.md). A tool marked destructive only asks when the client
// honours the hint, and the assistant deciding to restore may be acting on a
// log line or a file somebody else wrote. So an assistant can find the backup
// and say which one; the person restores it.

type databaseSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instances int    `json:"instances"`
	StorageGB int    `json:"storage_gb"`
}

func summariseDatabase(database store.Database) databaseSummary {
	return databaseSummary{
		ID: database.ID, Name: database.Name, Engine: database.Engine, Version: database.EngineVersion,
		Status: database.Status, Detail: database.StatusDetail,
		Instances: database.Instances, StorageGB: database.StorageGB,
	}
}

type listDatabasesOutput struct {
	Databases []databaseSummary `json:"databases"`
}

type createDatabaseInput struct {
	EnvironmentID string `json:"environment_id" jsonschema:"where to create it, as returned by list_projects; only apps in the same environment can use it"`
	Name          string `json:"name" jsonschema:"what to call it, such as db or cache; an app in the same environment cannot have the same name"`
	Engine        string `json:"engine" jsonschema:"postgres, redis, or mysql (which is MariaDB)"`
	Version       string `json:"version,omitempty" jsonschema:"the major version, such as 16 for postgres; left out means the current stable one"`
	StorageGB     int    `json:"storage_gb,omitempty" jsonschema:"the disk, in GB: at least 5, which is also what it gets when left out"`
	Instances     int    `json:"instances,omitempty" jsonschema:"PostgreSQL only: an odd number, 3 to survive losing a server. Redis and MySQL run one"`
}

type createDatabaseOutput struct {
	Database databaseSummary `json:"database"`
	Note     string          `json:"note"`
}

type linkDatabaseInput struct {
	DatabaseID string `json:"database_id" jsonschema:"the database, as returned by list_databases"`
	AppID      string `json:"app_id" jsonschema:"the app that will use it, in the same environment"`
	Variable   string `json:"variable,omitempty" jsonschema:"the variable the app reads the connection string from; left out means DATABASE_URL for postgres, REDIS_URL for redis and MYSQL_URL for mysql"`
}

type linkDatabaseOutput struct {
	DatabaseID string `json:"database_id"`
	AppID      string `json:"app_id"`
	Variable   string `json:"variable"`
	Note       string `json:"note"`
}

type databaseIDInput struct {
	DatabaseID string `json:"database_id" jsonschema:"the database, as returned by list_databases"`
}

type databaseStatusOutput struct {
	Database databaseSummary `json:"database"`
	Links    []databaseLink  `json:"linked_apps"`
	Note     string          `json:"note"`
}

type databaseLink struct {
	AppID    string `json:"app_id"`
	Variable string `json:"variable"`
}

type backupTargetInput struct {
	DatabaseID string `json:"database_id,omitempty" jsonschema:"a database's backups: the database, as returned by list_databases"`
	AppID      string `json:"app_id,omitempty" jsonschema:"a volume's backups: the app the volume belongs to"`
	VolumeID   string `json:"volume_id,omitempty" jsonschema:"a volume's backups: which of the app's volumes. list_backups with only app_id lists them"`
}

type backupSummary struct {
	ID          string `json:"id"`
	Of          string `json:"of"`
	TargetID    string `json:"target_id"`
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	SizeBytes   int64  `json:"size_bytes"`
	Taken       string `json:"taken"`
	Finished    string `json:"finished,omitempty"`
	Error       string `json:"error,omitempty"`
	Encrypted   bool   `json:"encrypted"`
	Verified    string `json:"verified,omitempty"`
	VerifyError string `json:"verify_error,omitempty"`
}

type volumeSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeGB    int    `json:"size_gb"`
}

type listBackupsOutput struct {
	Backups []backupSummary `json:"backups"`
	// Volumes are the app's, when the backups asked for were an app's: the
	// ids run_backup needs.
	Volumes []volumeSummary `json:"volumes,omitempty"`
	Note    string          `json:"note"`
}

type runBackupOutput struct {
	Backup backupSummary `json:"backup"`
	Note   string        `json:"note"`
}

// backupsListed is how many of each target's backups are listed: the newest,
// which are the ones anybody asks about.
const backupsListed = 20

func (s *Server) registerDatabases() {
	addTool(s, &mcp.Tool{
		Name:        "list_databases",
		Annotations: reads("List databases"),
		Description: "List the managed databases in an environment, with their ids, engines and state. Use it to find a database_id, and to see whether an app's database already exists before creating another.",
	}, s.listDatabases)

	addTool(s, &mcp.Tool{
		Name:        "create_database",
		Annotations: changes("Create a database", false, false),
		InputSchema: inputSchema[createDatabaseInput](func(p map[string]*jsonschema.Schema) {
			oneOf(p["engine"], "postgres", "redis", "mysql")
		}),
		Description: "Create a managed PostgreSQL, Redis or MySQL (MariaDB) database in an environment. It answers at once and takes a minute or two to be ready. " +
			"Then give it to an app with link_database: that is how the app gets its connection string, since nobody, you included, is shown the password. " +
			"deploy_folder and install_template make the databases they need themselves, so check list_databases before adding one.",
	}, s.createDatabase)

	addTool(s, &mcp.Tool{
		Name:        "link_database",
		Annotations: changes("Link a database to an app", true, true),
		Description: "Give an app a database's connection string, password included, as a secret variable, and roll the app out so it has it. " +
			"Neither you nor anybody else is shown the value; the app reads it at startup. Both have to be in the same environment. " +
			"It replaces a variable of the same name the app already had. Linking before the database is running is fine; the app may fail to connect until it is.",
	}, s.linkDatabase)

	addTool(s, &mcp.Tool{
		Name:        "get_database_status",
		Annotations: reads("Get a database's status"),
		Description: "Describe a database: whether it is running, why not if it is not, its size, and which apps are linked to it under which variable. " +
			"It never returns the password or the connection string; an app gets those with link_database.",
	}, s.getDatabaseStatus)

	addTool(s, &mcp.Tool{
		Name:        "list_backups",
		Annotations: reads("List backups"),
		Description: "List the newest backups of a database (database_id), or of an app's volumes (app_id, and optionally volume_id), with their state and size. " +
			"Given only app_id it also lists the app's volumes, with the ids run_backup needs. " +
			"Restoring is not a tool: it replaces everything with the older copy and cannot be undone, so the person does it in the panel. Tell them which backup, and when it was taken.",
	}, s.listBackups)

	addTool(s, &mcp.Tool{
		Name: "run_backup",
		// Destructive, because a backup counts against its schedule's
		// retention: once there are more than it keeps, taking one deletes
		// the oldest. Run a few in a row and the copy from before the
		// problem is the one that goes.
		Annotations: changes("Back up now", true, false),
		Description: "Back up a database (database_id) or one of an app's volumes (app_id and volume_id) now; before a risky migration is the time. " +
			"It returns at once and runs in the background; list_backups shows when it has finished. It needs backup storage set up in the panel's settings. " +
			"Each backup counts against the schedule's retention, so the oldest may be deleted to make room: take one when there is a reason to, not repeatedly.",
	}, s.runBackup)
}

func (s *Server) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, in environmentInput) (*mcp.CallToolResult, listDatabasesOutput, error) {
	environment := in.EnvironmentID
	if environment == "" {
		resolved, err := s.defaultEnvironment(ctx)
		if err != nil {
			return errorResult(err), listDatabasesOutput{}, nil
		}
		environment = resolved
	}
	var response struct {
		Items []store.Database `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", "/api/environments/"+url.PathEscape(environment)+"/databases", nil, &response); err != nil {
		return errorResult(err), listDatabasesOutput{}, nil
	}
	out := listDatabasesOutput{Databases: make([]databaseSummary, 0, len(response.Items))}
	for _, database := range response.Items {
		out.Databases = append(out.Databases, summariseDatabase(database))
	}
	return textResult(fmt.Sprintf("%d database(s).", len(out.Databases))), out, nil
}

func (s *Server) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in createDatabaseInput) (*mcp.CallToolResult, createDatabaseOutput, error) {
	body := map[string]any{"name": in.Name, "engine": in.Engine}
	if in.Version != "" {
		body["version"] = in.Version
	}
	if in.StorageGB > 0 {
		body["storage_gb"] = in.StorageGB
	}
	if in.Instances > 0 {
		body["instances"] = in.Instances
	}
	var database store.Database
	if err := s.client.Do(ctx, "POST", "/api/environments/"+url.PathEscape(in.EnvironmentID)+"/databases", body, &database); err != nil {
		return errorResult(err), createDatabaseOutput{}, nil
	}
	out := createDatabaseOutput{
		Database: summariseDatabase(database),
		Note: "It is being created, which takes a minute or two. Give it to an app with link_database now, " +
			"and call get_database_status to see when it is running.",
	}
	return textResult(fmt.Sprintf("Creating the %s database %s (%s). %s", database.Engine, database.Name, database.ID, out.Note)), out, nil
}

func (s *Server) linkDatabase(ctx context.Context, _ *mcp.CallToolRequest, in linkDatabaseInput) (*mcp.CallToolResult, linkDatabaseOutput, error) {
	body := map[string]any{"app_id": in.AppID}
	if in.Variable != "" {
		body["var_name"] = in.Variable
	}
	if err := s.client.Do(ctx, "POST", databasePath(in.DatabaseID, "/link"), body, nil); err != nil {
		return errorResult(err), linkDatabaseOutput{}, nil
	}

	// The panel answers only that it worked. Which name the variable got,
	// when none was asked for, is the engine's default, and the panel's own
	// record says it rather than a copy of that rule here.
	variable := in.Variable
	var detail struct {
		Links []store.DatabaseLink `json:"links"`
	}
	if err := s.client.Do(ctx, "GET", databasePath(in.DatabaseID, ""), nil, &detail); err == nil {
		for _, link := range detail.Links {
			if link.AppID == in.AppID {
				variable = link.VarName
			}
		}
	}
	if variable == "" {
		variable = "the engine's default variable"
	}

	out := linkDatabaseOutput{
		DatabaseID: in.DatabaseID, AppID: in.AppID, Variable: variable,
		Note: "The app is being rolled out with the connection string as a secret variable. " +
			"Its value is never shown, including by list_variables. If the app reads a different name, link again with that variable.",
	}
	return textResult(fmt.Sprintf("Linked: the app reads the database from %s. %s", variable, out.Note)), out, nil
}

func (s *Server) getDatabaseStatus(ctx context.Context, _ *mcp.CallToolRequest, in databaseIDInput) (*mcp.CallToolResult, databaseStatusOutput, error) {
	var response struct {
		Database store.Database       `json:"database"`
		Links    []store.DatabaseLink `json:"links"`
	}
	if err := s.client.Do(ctx, "GET", databasePath(in.DatabaseID, ""), nil, &response); err != nil {
		return errorResult(err), databaseStatusOutput{}, nil
	}
	out := databaseStatusOutput{Database: summariseDatabase(response.Database), Links: []databaseLink{}}
	for _, link := range response.Links {
		out.Links = append(out.Links, databaseLink{AppID: link.AppID, Variable: link.VarName})
	}
	switch response.Database.Status {
	case "running":
		out.Note = "It is running and accepting connections."
	case "creating", "starting":
		out.Note = "It is still starting. Check again in a minute."
	default:
		out.Note = "It is not running. The detail says why; relay it to the person."
	}
	text := fmt.Sprintf("%s is %s. %d app(s) linked.", response.Database.Name, response.Database.Status, len(out.Links))
	if response.Database.StatusDetail != "" {
		text += " " + response.Database.StatusDetail
	}
	return textResult(text), out, nil
}

// backupRoute is where a target's backups are, or why the input names none.
func backupRoute(in backupTargetInput, volumeOptional bool) (string, error) {
	switch {
	case in.DatabaseID != "" && in.AppID == "" && in.VolumeID == "":
		return databasePath(in.DatabaseID, "/backups"), nil
	case in.DatabaseID == "" && in.AppID != "" && in.VolumeID != "":
		return appPath(in.AppID, "/volumes/"+url.PathEscape(in.VolumeID)+"/backups"), nil
	case in.DatabaseID == "" && in.AppID != "" && volumeOptional:
		return "", nil
	}
	return "", errdoc.BadRequest("Give database_id for a database's backups, or app_id and volume_id for a volume's. " +
		"list_backups with only an app_id lists the app's volumes.")
}

func (s *Server) listBackups(ctx context.Context, _ *mcp.CallToolRequest, in backupTargetInput) (*mcp.CallToolResult, listBackupsOutput, error) {
	route, err := backupRoute(in, true)
	if err != nil {
		return errorResult(err), listBackupsOutput{}, nil
	}
	out := listBackupsOutput{
		Backups: []backupSummary{},
		Note: "Newest first. A backup is only worth restoring once it has succeeded; verified says when it was last read back whole. " +
			"Restoring is done by the person, in the panel.",
	}

	routes := []string{route}
	if route == "" {
		// Every volume of the app, which is also how the assistant learns
		// the volume ids.
		var volumes struct {
			Items []store.Volume `json:"items"`
		}
		if err := s.client.Do(ctx, "GET", appPath(in.AppID, "/volumes"), nil, &volumes); err != nil {
			return errorResult(err), listBackupsOutput{}, nil
		}
		out.Volumes = []volumeSummary{}
		routes = nil
		for _, volume := range volumes.Items {
			out.Volumes = append(out.Volumes, volumeSummary{
				ID: volume.ID, Name: volume.Name, MountPath: volume.MountPath, SizeGB: volume.SizeGB,
			})
			routes = append(routes, appPath(in.AppID, "/volumes/"+url.PathEscape(volume.ID)+"/backups"))
		}
		if len(out.Volumes) == 0 {
			out.Note = "This app has no volumes, so there is nothing of it to back up. Its databases are backed up on their own: list_backups with a database_id."
		}
	}

	for _, route := range routes {
		var response struct {
			Items []store.Backup `json:"items"`
		}
		if err := s.client.Do(ctx, "GET", fmt.Sprintf("%s?limit=%d", route, backupsListed), nil, &response); err != nil {
			return errorResult(err), listBackupsOutput{}, nil
		}
		for _, backup := range response.Items {
			out.Backups = append(out.Backups, summariseBackup(backup))
		}
	}
	return textResult(fmt.Sprintf("%d backup(s).", len(out.Backups))), out, nil
}

func (s *Server) runBackup(ctx context.Context, _ *mcp.CallToolRequest, in backupTargetInput) (*mcp.CallToolResult, runBackupOutput, error) {
	route, err := backupRoute(in, false)
	if err != nil {
		return errorResult(err), runBackupOutput{}, nil
	}
	var backup store.Backup
	if err := s.client.Do(ctx, "POST", route, nil, &backup); err != nil {
		return errorResult(err), runBackupOutput{}, nil
	}
	out := runBackupOutput{
		Backup: summariseBackup(backup),
		Note:   "The backup is running in the background. Call list_backups to see it finish before relying on it.",
	}
	return textResult(fmt.Sprintf("Backup %s started. %s", backup.ID, out.Note)), out, nil
}

func summariseBackup(backup store.Backup) backupSummary {
	// The storage location is left out: it is a path in somebody's bucket,
	// and nothing an assistant can do takes it.
	return backupSummary{
		ID: backup.ID, Of: backup.TargetType, TargetID: backup.TargetID, Status: backup.Status,
		Kind: backup.Kind, SizeBytes: backup.SizeBytes, Taken: stamp(backup.CreatedAt),
		Finished: stamp(backup.FinishedAt), Error: backup.ErrorMessage, Encrypted: backup.Encrypted,
		Verified: stamp(backup.VerifiedAt), VerifyError: backup.VerifyError,
	}
}

// stamp writes a time the way get_deployment_history does, and nothing for
// one that has not happened.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}
