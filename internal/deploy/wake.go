package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/version"
)

// Waking an app that sleeps, so that its deploy proves something.
//
// An app that scales to zero and is asleep has a Deployment of no instances,
// and a rollout of no instances is finished the moment it starts. Every deploy
// of a sleeping app succeeded, whatever it deployed, and the first visitor
// found out whether it started. So a deploy wakes it the way a visitor would —
// through KEDA's interceptor, with one of the app's own hostnames — and keeps
// asking while the rollout is waited on, so that the add-on does not put it
// back to sleep before the new version has shown that it starts. The add-on
// puts it to sleep again once the quiet period after the deploy has passed.

// wakeWait is how long the add-on has to scale a sleeping app up once asked,
// and wakeEvery how often it is asked while the rollout is waited on.
var (
	wakeWait  = 2 * time.Minute
	wakeEvery = 5 * time.Second
)

// interceptorURL is the add-on's proxy, from inside the cluster.
var interceptorURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
	kube.KEDAInterceptorService, kube.KEDANamespace, kube.KEDAInterceptorPort)

// wakeForRollout wakes a sleeping app and keeps it awake until the function it
// returns is called. For an app that is awake, or does not sleep, it does
// nothing.
//
// An app that does not wake is not a failed deploy: the version is applied, and
// failing it would have the next sync put the previous one back. It is said in
// the deploy's log instead, plainly, because the deploy has then proved nothing.
func (d *Deployer) wakeForRollout(ctx context.Context, deploymentID string, spec kube.AppSpec) func() {
	if !kube.ScaleToZeroEnabled(spec) {
		return func() {}
	}
	client := d.cluster.Client()
	asleep, err := client.Asleep(ctx, spec.Namespace, spec.Name)
	if err != nil || !asleep {
		// A Deployment that cannot be read is the rollout wait's to report.
		return func() {}
	}
	d.appendLog(ctx, deploymentID, "The app is asleep, so it is woken to show that this version starts.")
	awake, stop := context.WithCancel(ctx)
	url, every := interceptorURL+wakePath(spec), wakeEvery
	host := spec.Domains[0].Hostname
	runsafe.Go(d.log, "keeping an app awake for its deploy", func() {
		keepAwake(awake, url, host, every)
	})
	if err := client.WaitForAwake(ctx, spec.Namespace, spec.Name, wakeWait); err != nil {
		stop()
		d.appendLog(ctx, deploymentID, "The app did not wake: "+err.Error()+
			". This version is applied and has not been seen to start; it starts on the app's next request. "+
			"Check that Scale to zero is running under Settings, then Components.")
		return func() {}
	}
	return stop
}

// wakePath is what is asked for: the app's health path, which is cheap to
// answer, or its root.
func wakePath(spec kube.AppSpec) string {
	if spec.HealthPath == "" || spec.HealthPath[0] != '/' {
		return "/"
	}
	return spec.HealthPath
}

// keepAwake asks the interceptor for url, as host, every so often until ctx
// ends. What the app answers does not matter: a request the add-on is holding
// is what keeps it awake.
func keepAwake(ctx context.Context, url, host string, every time.Duration) {
	client := &http.Client{Timeout: 30 * time.Second}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		// The interceptor routes by hostname, and this is the app's own.
		request.Host = host
		request.Header.Set("User-Agent", version.UserAgent())
		if response, err := client.Do(request); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
