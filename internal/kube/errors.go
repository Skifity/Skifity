package kube

import (
	"errors"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// IsNotFound reports whether an error means the object does not exist.
func IsNotFound(err error) bool { return apierrors.IsNotFound(err) }

// IsAlreadyExists reports whether an error means the object is already there.
func IsAlreadyExists(err error) bool { return apierrors.IsAlreadyExists(err) }

// IsForbidden reports whether the panel's ServiceAccount lacks permission.
func IsForbidden(err error) bool { return apierrors.IsForbidden(err) }

// IsUnreachable reports whether the Kubernetes API could not be contacted at
// all, as opposed to answering with a rejection. The two need different
// messages: one is "the cluster is down", the other is "you asked for something
// invalid".
func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) {
		return true
	}
	var dnsErr interface{ Timeout() bool }
	if errors.As(err, &dnsErr) && dnsErr.Timeout() {
		return true
	}
	msg := err.Error()
	for _, needle := range []string{
		"connection refused",
		"no such host",
		"i/o timeout",
		"context deadline exceeded",
		"EOF",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// isNilPointer reports whether a non-nil interface holds a nil pointer.
func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}
