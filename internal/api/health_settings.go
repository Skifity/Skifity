package api

import (
	"strings"

	"skifity/internal/errdoc"
	"skifity/internal/kube"
	"skifity/internal/store"
)

// applyHealthSettings changes an app's health check to what a request asked
// for, and refuses what the probes could not be written from. Each nil is a
// setting the request did not mention, which stays as it is.
//
// pathChanged says the request set the health path. Before the check could be
// chosen, the path was the choice — a path meant an HTTP check, none meant a
// connect — and a client that only ever sends the path still gets exactly
// that: the check follows the path unless the request names one, or the app
// has its check switched off, which a new path does not switch back on.
func applyHealthSettings(app *store.App, check *string, start, timeout *int, pathChanged bool) error {
	switch {
	case check != nil:
		wanted := strings.ToLower(strings.TrimSpace(*check))
		if !kube.ValidHealthCheck(wanted) {
			return errdoc.HealthCheckUnknown(*check)
		}
		app.HealthCheck = wanted
	case pathChanged && app.HealthCheck != kube.HealthNone:
		app.HealthCheck = kube.HealthCheckFor(app.HealthPath)
	case app.HealthCheck == "":
		app.HealthCheck = kube.HealthCheckFor(app.HealthPath)
	}
	if app.HealthCheck == kube.HealthHTTP && app.HealthPath == "" {
		return errdoc.HealthPathRequired()
	}

	if start != nil {
		if *start < kube.MinHealthStartSeconds || *start > kube.MaxHealthStartSeconds {
			return errdoc.HealthStartOutOfRange(*start, kube.MinHealthStartSeconds, kube.MaxHealthStartSeconds)
		}
		app.HealthStartSeconds = *start
	}
	if timeout != nil {
		if *timeout < kube.MinHealthTimeoutSeconds || *timeout > kube.MaxHealthTimeoutSeconds {
			return errdoc.HealthTimeoutOutOfRange(*timeout, kube.MinHealthTimeoutSeconds, kube.MaxHealthTimeoutSeconds)
		}
		app.HealthTimeoutSeconds = *timeout
	}
	return nil
}
