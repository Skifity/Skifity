package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"text/tabwriter"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// cmdFiles lists, saves, prints and removes an app's files: configuration its
// containers read at a path.
//
//	skifity files                                      what the app has
//	skifity files set /etc/nginx/nginx.conf ./nginx.conf
//	skifity files set /app/secrets.yml ./secrets.yml --secret
//	skifity files set /docker-entrypoint.d/10-init.sh ./init.sh --executable
//	skifity files cat /etc/nginx/nginx.conf
//	skifity files rm /etc/nginx/nginx.conf
//
// The content is read from a local file, or from standard input when that is
// "-", so a file never has to be pasted into a shell's history.
func cmdFiles(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("files", flag.ContinueOnError)
	flags.SetOutput(out)
	appID := flags.String("app", "", "the app id")
	secret := flags.Bool("secret", false, "never show the content again once saved")
	executable := flags.Bool("executable", false, "mount the file executable, for a script the image runs")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	action := "list"
	if len(positional) > 0 {
		action, positional = positional[0], positional[1:]
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	client := NewClient(cfg)
	app, err := resolveApp(ctx, client, cfg, *appID)
	if err != nil {
		return err
	}
	path := "/api/apps/" + app + "/files"

	var listed struct {
		Items []store.AppFile `json:"items"`
	}
	find := func(filePath string) (store.AppFile, error) {
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return store.AppFile{}, err
		}
		i := slices.IndexFunc(listed.Items, func(f store.AppFile) bool { return f.Path == filePath })
		if i < 0 {
			return store.AppFile{}, errdoc.BadRequest(fmt.Sprintf(
				"This app has no file at %s. `%s files` lists the ones it has.", filePath, version.Binary))
		}
		return listed.Items[i], nil
	}

	switch action {
	case "list", "ls":
		if err := client.Do(ctx, "GET", path, nil, &listed); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, listed.Items)
		}
		if len(listed.Items) == 0 {
			fmt.Fprintf(out, "No files. Add one with `%s files set /etc/app/config.yml ./config.yml`.\n", version.Binary)
			return nil
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "PATH\tSIZE\tSECRET\tEXECUTABLE")
		for _, f := range listed.Items {
			fmt.Fprintf(table, "%s\t%d\t%s\t%s\n", f.Path, f.Size, yesNo(f.IsSecret), yesNo(f.Executable))
		}
		return table.Flush()

	case "set":
		if len(positional) != 2 {
			return errdoc.BadRequest(fmt.Sprintf(
				"Give the path in the container and the local file to read: `%s files set /etc/nginx/nginx.conf ./nginx.conf`, or - for standard input.",
				version.Binary))
		}
		var content []byte
		if positional[1] == "-" {
			content, err = io.ReadAll(filesStdin)
		} else {
			content, err = os.ReadFile(positional[1])
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", positional[1], err)
		}
		body := map[string]any{"path": positional[0], "content": string(content), "executable": *executable}
		// Said only when asked: silence keeps a secret file secret.
		if *secret {
			body["is_secret"] = true
		}
		var saved struct {
			File store.AppFile `json:"file"`
		}
		if err := client.Do(ctx, "PUT", path, body, &saved); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, saved.File)
		}
		fmt.Fprintf(out, "%s is saved (%d bytes), and the app restarts to read it.\n", saved.File.Path, saved.File.Size)
		return nil

	case "cat", "show":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the file: `%s files cat /etc/nginx/nginx.conf`.", version.Binary))
		}
		f, err := find(positional[0])
		if err != nil {
			return err
		}
		if f.IsSecret {
			return errdoc.BadRequest(fmt.Sprintf(
				"%s is secret, so its content is never shown again. Save a new one to replace it.", f.Path))
		}
		_, err = io.WriteString(out, f.Content)
		return err

	case "rm", "remove":
		if len(positional) != 1 {
			return errdoc.BadRequest(fmt.Sprintf("Name the file: `%s files rm /etc/nginx/nginx.conf`.", version.Binary))
		}
		f, err := find(positional[0])
		if err != nil {
			return err
		}
		if err := client.Do(ctx, "DELETE", path+"/"+f.ID, nil, nil); err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(out, map[string]string{"removed": f.Path})
		}
		fmt.Fprintf(out, "%s is removed, and the app restarts without it.\n", f.Path)
		return nil
	}
	return errdoc.BadRequest(fmt.Sprintf("files takes ls, set, cat or rm, not %q.", action))
}

// filesStdin is where `files set PATH -` reads from; a test replaces it.
var filesStdin io.Reader = os.Stdin

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
