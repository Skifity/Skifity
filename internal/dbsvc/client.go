package dbsvc

import (
	"strconv"
	"strings"

	"skifity/internal/dbsvc/engine"
)

// ToolsImage is the small image with a shell, tar and gzip that a job runs
// when it needs nothing more: the client for an engine the panel does not
// know, and the copy of a volume. It was alpine:3, a tag that moves, so a
// backup on one day ran different tools from the backup the day before with
// nothing changed here. Pinned by version and index digest like the builders'
// images (builder/images.go), read from Docker Hub on 2026-10-02; move the
// two together.
const ToolsImage = "alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6"

// ClientImage is the image a job that talks to a database runs its client
// tools from: the database's own image, pinned as the database is, so the
// client is the server's own version. The backup jobs, the password change
// and an import all run it.
//
// PostgreSQL is the exception. The operator's images carry no pg_dump, so the
// client is the official image of the same major: at least the server's, as
// pg_dump requires, and not newer, because pg_dump 17's output sets
// transaction_timeout, which a PostgreSQL 16 refuses on the way back in.
func ClientImage(name, version string) string {
	e, ok := engine.Lookup(name)
	if !ok {
		return ToolsImage
	}
	if version == "" {
		version = e.DefaultVersion
	}
	if name == engine.Postgres {
		major, _, _ := strings.Cut(version, ".")
		if _, err := strconv.Atoi(major); err != nil {
			major = e.DefaultVersion
		}
		return "postgres:" + major + "-alpine"
	}
	image, err := e.Image(version)
	if err != nil {
		image, _ = e.Image(e.DefaultVersion)
	}
	return image
}

// ClientUser is the account ClientImage's tools run as.
//
// Every environment's namespace enforces the restricted Pod Security profile,
// which refuses a pod that could run as root. These images all default to root
// and drop privileges in their entrypoint, which a job never reaches, so the
// uid is named instead.
func ClientUser(name string) int64 {
	switch name {
	case EnginePostgres:
		return 70 // postgres, in the Alpine image
	case EngineMySQL, EngineMariaDB, EngineMongoDB, EngineRedis, EngineValkey:
		return 999 // mysql, mongodb, redis and valkey, each in its own image
	default:
		return 65532
	}
}
