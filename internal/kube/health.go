package kube

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// How an app's instances are checked.
//
// An HTTP check asks the app's health path and wants a 2xx or 3xx back; a TCP
// check only connects to its port; none renders no probe at all, for software
// that answers neither — Kubernetes then calls an instance ready as soon as
// its process has started, which is all a zero-downtime deploy can wait for.
const (
	HealthHTTP = "http"
	HealthTCP  = "tcp"
	HealthNone = "none"
)

// The bounds of an app's health check settings, in seconds.
//
// The start budget is how long a new instance may take before it is expected
// to answer. Two minutes is what every app had before it could be changed,
// and too little for a JVM warming up or an image that migrates its database
// before it listens. Half an hour is the ceiling: anything slower than that is
// not starting, it is stuck, and a deploy should say so the same day.
const (
	DefaultHealthStartSeconds = 120
	MinHealthStartSeconds     = 10
	MaxHealthStartSeconds     = 1800

	DefaultHealthTimeoutSeconds = 3
	MinHealthTimeoutSeconds     = 1
	MaxHealthTimeoutSeconds     = 60
)

// The probes' rhythm. The readiness probe is quick so traffic follows an
// instance's health closely; the liveness probe is slow and forgiving because
// what it does on failure is a restart.
const (
	readinessPeriod    = 3
	readinessThreshold = 2
	livenessPeriod     = 10
	livenessThreshold  = 6
	startupPeriod      = 3
)

// defaultProgressDeadline is Kubernetes' own: a rollout that makes no progress
// for ten minutes is marked as having failed.
const defaultProgressDeadline = 600

// ValidHealthCheck reports whether a value is one of the three checks.
func ValidHealthCheck(check string) bool {
	switch check {
	case HealthHTTP, HealthTCP, HealthNone:
		return true
	}
	return false
}

// HealthCheckFor is the check an app has when nobody chose one: an HTTP
// request when there is a path to ask, a TCP connect otherwise. It is what
// every app had before the check could be chosen.
func HealthCheckFor(path string) string {
	if path != "" {
		return HealthHTTP
	}
	return HealthTCP
}

// EffectiveHealthCheck is the check an app's settings render. An empty one is
// worked out from the path, and an HTTP check with no path to ask falls back
// to a connect: Kubernetes would otherwise ask "/", which may not exist, or
// may have side effects nobody meant a probe to trigger.
func EffectiveHealthCheck(check, path string) string {
	if check == "" {
		check = HealthCheckFor(path)
	}
	if check == HealthHTTP && path == "" {
		return HealthTCP
	}
	return check
}

func (s AppSpec) healthCheck() string { return EffectiveHealthCheck(s.HealthCheck, s.HealthPath) }

// HealthStart is the start budget in seconds, with the default for an app
// that has none recorded.
func (s AppSpec) HealthStart() int {
	if s.HealthStartSeconds <= 0 {
		return DefaultHealthStartSeconds
	}
	return s.HealthStartSeconds
}

// healthTimeout is the probe timeout in seconds, with the same default.
func (s AppSpec) healthTimeout() int32 {
	if s.HealthTimeoutSeconds <= 0 {
		return DefaultHealthTimeoutSeconds
	}
	return int32(s.HealthTimeoutSeconds)
}

// validateHealth refuses what the probes could not be written from.
func validateHealth(s AppSpec) error {
	if s.HealthCheck != "" && !ValidHealthCheck(s.HealthCheck) {
		return fmt.Errorf("the health check %q is not http, tcp or none", s.HealthCheck)
	}
	if s.HealthStartSeconds != 0 && (s.HealthStartSeconds < MinHealthStartSeconds || s.HealthStartSeconds > MaxHealthStartSeconds) {
		return fmt.Errorf("the time to start, %d seconds, is not between %d and %d",
			s.HealthStartSeconds, MinHealthStartSeconds, MaxHealthStartSeconds)
	}
	if s.HealthTimeoutSeconds != 0 && (s.HealthTimeoutSeconds < MinHealthTimeoutSeconds || s.HealthTimeoutSeconds > MaxHealthTimeoutSeconds) {
		return fmt.Errorf("the health check timeout, %d seconds, is not between %d and %d",
			s.HealthTimeoutSeconds, MinHealthTimeoutSeconds, MaxHealthTimeoutSeconds)
	}
	return nil
}

// probeHandler is what each probe asks: the health path, or a connect.
func probeHandler(s AppSpec) corev1.ProbeHandler {
	if s.healthCheck() == HealthHTTP {
		return corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: s.HealthPath,
				Port: intstr.FromString("http"),
			},
		}
	}
	// Without a health path, a TCP connect is the only check that does not risk
	// calling an endpoint with side effects.
	return corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")},
	}
}

func buildProbe(s AppSpec, period, failureThreshold int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     probeHandler(s),
		PeriodSeconds:    period,
		TimeoutSeconds:   s.healthTimeout(),
		FailureThreshold: failureThreshold,
	}
}

// setProbes writes the three probes onto an app's container, or none.
//
// The startup probe is what carries the start budget. Until it passes, the
// other two are held off, so a slow framework gets its time without the
// liveness probe having to be lenient for the rest of the instance's life.
// Its threshold is the budget divided by its period, rounded up, so the time
// it allows is never less than what somebody asked for.
func setProbes(container *corev1.Container, s AppSpec) {
	if s.healthCheck() == HealthNone {
		return
	}
	container.ReadinessProbe = buildProbe(s, readinessPeriod, readinessThreshold)
	container.LivenessProbe = buildProbe(s, livenessPeriod, livenessThreshold)
	container.StartupProbe = buildProbe(s, startupPeriod, startupThreshold(s.HealthStart()))
}

// startupThreshold is how many failed startup probes add up to a budget.
func startupThreshold(budget int) int32 {
	return int32((budget + startupPeriod - 1) / startupPeriod)
}

// progressDeadline is how long a rollout may go without progress before
// Kubernetes calls it failed, or nil for its own ten minutes.
//
// An instance still inside its start budget is not progress, so a budget near
// ten minutes would have the rollout declared failed while the app was doing
// exactly what it was allowed to. The deadline is the budget with room for
// pulling the image and scheduling on top, and it is written only when that is
// more than the default, so an app on the default renders what it always did.
func progressDeadline(s AppSpec) *int32 {
	if s.Port <= 0 || s.healthCheck() == HealthNone {
		return nil
	}
	deadline := s.HealthStart() + RolloutMargin
	if deadline <= defaultProgressDeadline {
		return nil
	}
	return ptr(int32(deadline))
}

// RolloutMargin is the time a rollout needs on top of an instance's start
// budget: pulling the image, scheduling, and the old instance's goodbye.
const RolloutMargin = 300
