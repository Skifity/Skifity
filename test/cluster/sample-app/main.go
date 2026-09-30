// A sample application, for test/cluster/verify.sh.
//
// It answers four things and nothing else:
//
//	/         what version it is and which instance answered
//	/healthz  200, once it is ready
//	/data     the contents of the file at DATA_FILE, so a volume can be proved
//	          to survive a restart
//	/write    writes the query string to that file
//
// VERSION is set at build time by nothing at all — it is an environment
// variable, so the same image can be redeployed with a different answer, which
// is how the rollback check tells one deployment from another without a rebuild.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	host, _ := os.Hostname()
	version := env("VERSION", "1")
	dataFile := env("DATA_FILE", "/data/value")

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	http.HandleFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		value, err := os.ReadFile(dataFile)
		if err != nil {
			http.Error(w, "no data: "+err.Error(), http.StatusNotFound)
			return
		}
		_, _ = w.Write(value)
	})
	http.HandleFunc("/write", func(w http.ResponseWriter, r *http.Request) {
		if err := os.WriteFile(dataFile, []byte(r.URL.RawQuery), 0o600); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, "written")
	})
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "version=%s instance=%s\n", version, host)
	})

	// Timeouts, because a linter that insists on them is right even here, and
	// a sample that shows the careless version is a sample somebody copies.
	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
