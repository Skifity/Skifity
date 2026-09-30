package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// An app's files: configuration its containers read at a path.

type listFilesInput struct {
	AppID string `json:"app_id" jsonschema:"the app's id"`
	Path  string `json:"path,omitempty" jsonschema:"one file's path, to read its content; left out lists every file without content"`
}

type fileSummary struct {
	Path       string `json:"path"`
	Size       int    `json:"size"`
	Secret     bool   `json:"secret"`
	Executable bool   `json:"executable"`
	Content    string `json:"content,omitempty"`
}

type listFilesOutput struct {
	Files []fileSummary `json:"files"`
	Note  string        `json:"note"`
}

type setFileInput struct {
	AppID   string `json:"app_id" jsonschema:"the app's id"`
	Path    string `json:"path" jsonschema:"where the file appears in the container, absolute, such as /etc/nginx/nginx.conf"`
	Content string `json:"content" jsonschema:"the whole file, as text; it replaces what is there"`
	// Pointers, for the reason set_variable's is one: a model that leaves
	// these out has said nothing, and as plain booleans that became "not a
	// secret" and "not executable" — a credentials file shown again, and an
	// entrypoint script the image can no longer run.
	IsSecret   *bool `json:"is_secret,omitempty" jsonschema:"never show the content again, for a file holding credentials; leave it out to keep what the file was and let the panel decide for a new one"`
	Executable *bool `json:"executable,omitempty" jsonschema:"mount it executable, for a script the image runs; leave it out to keep what the file was"`
}

type setFileOutput struct {
	File fileSummary `json:"file"`
	Note string      `json:"note"`
}

func (s *Server) registerFiles() {
	addTool(s, &mcp.Tool{
		Name:        "list_files",
		Annotations: reads("List files"),
		Description: "List the files an app reads at a path — configuration mounted into every instance — or, given a path, read that file's content. " +
			"A secret file's content is never returned: it can be replaced with set_file but not read, so do not ask the person to paste it.",
	}, s.listFiles)

	addTool(s, &mcp.Tool{
		Name:        "set_file",
		Annotations: changes("Save a file", true, true),
		Description: "Save a text file into an app at an absolute path, or replace the one there: an nginx.conf, a Caddyfile, a settings file the image reads and has no variable for. " +
			"It is mounted read-only in every instance and rolled out without rebuilding. Up to 256 KiB. " +
			"Read the current one with list_files first and send the whole file, since this replaces it. " +
			"Not for code, which is deployed, or for data the app writes, which belongs on a volume.",
	}, s.setFile)
}

func (s *Server) files(ctx context.Context, appID string) ([]store.AppFile, error) {
	var response struct {
		Items []store.AppFile `json:"items"`
	}
	if err := s.client.Do(ctx, "GET", appPath(appID, "/files"), nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

// summariseFile is a file as an assistant sees it.
//
// A secret file's content is dropped here as well as by the panel, which
// never sends it. Something that has reached an assistant's context is in its
// provider's logs and can be repeated anywhere; there is no taking it back,
// so this does not rest on one check.
func summariseFile(file store.AppFile, withContent bool) fileSummary {
	summary := fileSummary{Path: file.Path, Size: file.Size, Secret: file.IsSecret, Executable: file.Executable}
	if withContent && !file.IsSecret {
		summary.Content = file.Content
	}
	return summary
}

func (s *Server) listFiles(ctx context.Context, _ *mcp.CallToolRequest, in listFilesInput) (*mcp.CallToolResult, listFilesOutput, error) {
	files, err := s.files(ctx, in.AppID)
	if err != nil {
		return errorResult(err), listFilesOutput{}, nil
	}

	// Every file's content at once can be most of a megabyte, and a list
	// that was cut short to fit is one somebody edits and saves back cut
	// short. So the list is paths, and a file is read whole or not at all.
	if in.Path == "" {
		out := listFilesOutput{
			Files: make([]fileSummary, 0, len(files)),
			Note:  "Pass a path to read that file's content. A secret file's content is never returned.",
		}
		for _, file := range files {
			out.Files = append(out.Files, summariseFile(file, false))
		}
		return textResult(fmt.Sprintf("%d file(s).", len(out.Files))), out, nil
	}

	for _, file := range files {
		if file.Path != in.Path {
			continue
		}
		out := listFilesOutput{Files: []fileSummary{summariseFile(file, true)}}
		if file.IsSecret {
			out.Note = "This file is a secret, so its content is not returned. set_file can replace it."
			return textResult(file.Path + " is a secret file; its content is not shown."), out, nil
		}
		out.Note = "This is the whole file. set_file with the same path replaces it."
		return textResult(file.Content), out, nil
	}
	return errorResult(errdoc.NotFound("file", in.Path).
		WithFix("list_files without a path lists the files this app has.")), listFilesOutput{}, nil
}

func (s *Server) setFile(ctx context.Context, _ *mcp.CallToolRequest, in setFileInput) (*mcp.CallToolResult, setFileOutput, error) {
	body := map[string]any{"path": in.Path, "content": in.Content}
	if in.IsSecret != nil {
		body["is_secret"] = *in.IsSecret
	}
	// The panel keeps a secret file secret when is_secret is left out, and
	// treats a missing executable as false. So what the file was is looked
	// up here, or replacing an entrypoint script's content would quietly
	// make it one the container cannot run.
	executable := false
	if in.Executable != nil {
		executable = *in.Executable
	} else {
		files, err := s.files(ctx, in.AppID)
		if err != nil {
			return errorResult(err), setFileOutput{}, nil
		}
		for _, file := range files {
			if file.Path == in.Path {
				executable = file.Executable
			}
		}
	}
	body["executable"] = executable

	var response struct {
		File store.AppFile `json:"file"`
	}
	if err := s.client.Do(ctx, "PUT", appPath(in.AppID, "/files"), body, &response); err != nil {
		return errorResult(err), setFileOutput{}, nil
	}
	out := setFileOutput{
		File: summariseFile(response.File, false),
		Note: "Saved, and the app is being rolled out with it; nothing is rebuilt.",
	}
	if response.File.IsSecret {
		out.Note += " It is a secret file, so its content will not be shown again."
	}
	return textResult(fmt.Sprintf("%s: %s", response.File.Path, out.Note)), out, nil
}
