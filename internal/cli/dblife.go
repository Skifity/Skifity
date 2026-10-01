package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"skifity/internal/errdoc"
	"skifity/internal/store"
	"skifity/internal/version"
)

// A database's life, from the command line:
//
//	skifity db stop orders [--force]
//	skifity db start orders [--wait]
//	skifity db resize orders --memory-limit 2048 --storage 20
//	skifity db password orders [--password-stdin] [--show-password]
//	skifity db import orders ./dump.sql.gz [--format custom] [--wait]
//	pg_dump --format=custom shop | skifity db import orders -

// lifeOptions are the flags these share with the rest of `db`.
type lifeOptions struct {
	force, wait, passwordStdin, showPassword            *bool
	cpuRequest, cpuLimit, memRequest, memLimit, storage *int
	format                                              *string
}

func lifeFlags(flags *flag.FlagSet) lifeOptions {
	return lifeOptions{
		force: flags.Bool("force", false, "stop: stop it even though linked apps lose it"),
		wait:  flags.Bool("wait", false, "start, password, import: wait until it is done"),
		passwordStdin: flags.Bool("password-stdin", false,
			"password: read the new password from standard input rather than having one generated"),
		showPassword: flags.Bool("show-password", false,
			"password: print the new password once it is changed; it is audited, as reading it in the panel is"),
		cpuRequest: flags.Int("cpu-request", -1, "resize: the CPU to reserve, in millicores"),
		cpuLimit:   flags.Int("cpu-limit", -1, "resize: the CPU it may use, in millicores; 0 for no limit"),
		memRequest: flags.Int("memory-request", -1, "resize: the memory to reserve, in MB"),
		memLimit:   flags.Int("memory-limit", -1, "resize: the memory it may use, in MB"),
		storage:    flags.Int("storage", -1, "resize: the disk, in GB; it can only grow"),
		format:     flags.String("format", "", "import: sql, custom, archive, archive-gzip or rdb; detected when left out"),
	}
}

// dbStdin is where `db import -` and --password-stdin read from; a test
// replaces it.
var dbStdin io.Reader = os.Stdin

// operationPoll is how often a command waiting on an operation asks.
var operationPoll = 2 * time.Second

func runDatabaseLife(ctx context.Context, client *Client, action string, database store.Database,
	rest []string, o lifeOptions, asJSON bool, out io.Writer,
) error {
	path := "/api/databases/" + url.PathEscape(database.ID)
	switch action {
	case "stop":
		route := path + "/stop"
		if *o.force {
			route += "?force=true"
		}
		var stopped store.Database
		if err := client.Do(ctx, "POST", route, map[string]any{}, &stopped); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, stopped)
		}
		fmt.Fprintf(out, "%s is stopped. Its disk is kept; `%s db start %s` brings it back.\n",
			stopped.Name, version.Binary, stopped.Slug)
		return nil

	case "start":
		var started store.Database
		if err := client.Do(ctx, "POST", path+"/start", map[string]any{}, &started); err != nil {
			return err
		}
		if *o.wait {
			var err error
			if started, err = waitForDatabase(ctx, client, database.ID); err != nil {
				return err
			}
		}
		if asJSON {
			return writeJSON(out, started)
		}
		if started.Status == "running" {
			fmt.Fprintf(out, "%s is running.\n", started.Name)
		} else {
			fmt.Fprintf(out, "%s is starting. `%s db` shows when it is running.\n", started.Name, version.Binary)
		}
		return nil

	case "resize":
		body := map[string]int{}
		for key, value := range map[string]*int{
			"cpu_request_m": o.cpuRequest, "cpu_limit_m": o.cpuLimit,
			"mem_request_mb": o.memRequest, "mem_limit_mb": o.memLimit, "storage_gb": o.storage,
		} {
			if *value >= 0 {
				body[key] = *value
			}
		}
		if len(body) == 0 {
			return errdoc.BadRequest(fmt.Sprintf(
				"Say what to change: --cpu-request, --cpu-limit, --memory-request, --memory-limit or --storage, "+
					"for example `%s db resize %s --memory-limit 2048`.", version.Binary, database.Slug))
		}
		var resized store.Database
		if err := client.Do(ctx, "PATCH", path, body, &resized); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(out, resized)
		}
		limit := "no CPU limit"
		if resized.CPULimitM > 0 {
			limit = fmt.Sprintf("a limit of %dm", resized.CPULimitM)
		}
		fmt.Fprintf(out, "%s reserves %dm CPU (%s) and %d MB of memory (a limit of %d MB)", resized.Name,
			resized.CPURequestM, limit, resized.MemRequestMB, resized.MemLimitMB)
		if resized.StorageGB > 0 {
			fmt.Fprintf(out, ", on %d GB of disk", resized.StorageGB)
		}
		fmt.Fprintln(out, ". A change of CPU or memory restarts it.")
		return nil

	case "password":
		body := map[string]any{}
		if *o.passwordStdin {
			// From standard input and never from an argument, where the
			// shell's history and the process list would keep it.
			line, err := bufio.NewReader(dbStdin).ReadString('\n')
			if err != nil && err != io.EOF {
				return fmt.Errorf("read the password: %w", err)
			}
			password := strings.TrimRight(line, "\r\n")
			if password == "" {
				return errdoc.BadRequest("--password-stdin was given and standard input was empty.")
			}
			body["password"] = password
		}
		var op store.Operation
		if err := client.Do(ctx, "POST", path+"/password", body, &op); err != nil {
			return err
		}
		if !*o.wait && !*o.showPassword {
			if asJSON {
				return writeJSON(out, op)
			}
			fmt.Fprintf(out, "%s is being given a new password (%s). Its linked apps are given the new connection string as it goes; "+
				"the database's History in the panel shows each step.\n", database.Name, op.ID)
			return nil
		}
		finished, err := waitForOperation(ctx, client, op.ID)
		if err != nil {
			return err
		}
		result := map[string]any{"operation": finished}
		if *o.showPassword {
			var credentials databaseCredentials
			if err := client.Do(ctx, "GET", path+"/credentials", nil, &credentials); err != nil {
				return err
			}
			result["password"] = credentials.Password
			if !asJSON {
				fmt.Fprintf(out, "%s has a new password, and its linked apps have the new connection string.\n%s\n",
					database.Name, credentials.Password)
				return nil
			}
		}
		if asJSON {
			return writeJSON(out, result)
		}
		fmt.Fprintf(out, "%s has a new password, and its linked apps have the new connection string. "+
			"--show-password prints it.\n", database.Name)
		return nil

	case "import":
		if len(rest) != 1 {
			return errdoc.BadRequest(fmt.Sprintf(
				"Give the dump to import, or - for standard input: `%s db import %s ./dump.sql.gz`.",
				version.Binary, database.Slug))
		}
		var body io.Reader
		size := int64(-1)
		if rest[0] == "-" {
			body = dbStdin
		} else {
			file, err := os.Open(rest[0])
			if err != nil {
				return errdoc.BadRequest(fmt.Sprintf("%s could not be opened: %v.", rest[0], err))
			}
			defer file.Close()
			if info, err := file.Stat(); err == nil {
				size = info.Size()
			}
			body = file
		}
		route := path + "/import"
		if *o.format != "" {
			route += "?format=" + url.QueryEscape(*o.format)
		}
		var op store.Operation
		if err := client.UploadWith(ctx, "POST", route, "application/octet-stream", body, size, &op); err != nil {
			return err
		}
		if *o.wait {
			finished, err := waitForOperation(ctx, client, op.ID)
			if err != nil {
				return err
			}
			op = finished
		}
		if asJSON {
			return writeJSON(out, op)
		}
		if op.Status == store.OpSucceeded {
			fmt.Fprintf(out, "The dump is in %s. The backup taken just before it is in its Backups, to undo it.\n", database.Name)
		} else {
			fmt.Fprintf(out, "The dump was received (%s). A backup of %s is taken first, then the dump is loaded; "+
				"the database's History in the panel shows each step.\n", op.ID, database.Name)
		}
		return nil
	}
	return errdoc.BadRequest("Say stop, start, resize, password or import.")
}

// waitForOperation polls an operation until it has finished, and answers a
// failure as the problem the panel recorded.
func waitForOperation(ctx context.Context, client *Client, id string) (store.Operation, error) {
	for {
		var op store.Operation
		if err := client.Do(ctx, "GET", "/api/operations/"+url.PathEscape(id), nil, &op); err != nil {
			return store.Operation{}, err
		}
		switch op.Status {
		case store.OpSucceeded:
			return op, nil
		case store.OpFailed, store.OpCancelled:
			return op, operationProblem(op)
		}
		select {
		case <-ctx.Done():
			return store.Operation{}, ctx.Err()
		case <-time.After(operationPoll):
		}
	}
}

// operationProblem is the problem a failed operation recorded, rebuilt from
// its code and its words. The step that failed carries the fix, which the
// panel shows; the code is enough to look it up.
func operationProblem(op store.Operation) error {
	title, cause, _ := strings.Cut(op.ErrorMsg, ": ")
	if title == "" {
		title = "The operation did not finish"
	}
	problem := &errdoc.Problem{Code: op.ErrorCode, Title: title, Cause: cause, Severity: errdoc.SeverityError}
	for _, step := range op.Steps {
		if step.Status == store.StepFailed && step.Detail != "" {
			problem.Fix = step.Detail
		}
	}
	return problem
}

// waitForDatabase polls a database until it is running, or has failed.
func waitForDatabase(ctx context.Context, client *Client, id string) (store.Database, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	for {
		var answer struct {
			Database store.Database `json:"database"`
		}
		if err := client.Do(ctx, "GET", "/api/databases/"+url.PathEscape(id), nil, &answer); err != nil {
			return store.Database{}, err
		}
		switch answer.Database.Status {
		case "running", "degraded", "failed":
			return answer.Database, nil
		}
		select {
		case <-ctx.Done():
			return answer.Database, nil
		case <-time.After(operationPoll):
		}
	}
}
