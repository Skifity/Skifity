package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
)

// ExecWithInput runs a command in a running container, with input on its
// standard input, and waits for it to finish. Whatever the command printed is
// returned with the error, so a failure can say what went wrong inside.
//
// It is how the panel hands an uploaded folder to a build: the build namespace
// may not reach the panel, but the panel may reach into it (see
// builder.ReceiveCommand).
//
// WebSocket first, as kubectl does, and SPDY for an API server too old for it
// or a proxy in between that will not upgrade.
func (c *Client) ExecWithInput(ctx context.Context, namespace, pod, container string, command []string, input io.Reader) (string, error) {
	request := c.streams.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	websocket, err := remotecommand.NewWebSocketExecutor(c.config, "GET", request.URL().String())
	if err != nil {
		return "", fmt.Errorf("prepare the exec: %w", err)
	}
	spdy, err := remotecommand.NewSPDYExecutor(c.config, "POST", request.URL())
	if err != nil {
		return "", fmt.Errorf("prepare the exec: %w", err)
	}
	executor, err := remotecommand.NewFallbackExecutor(websocket, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return "", fmt.Errorf("prepare the exec: %w", err)
	}

	// Bounded, because it is only kept to explain a failure.
	output := &boundedBuffer{limit: 16 * 1024}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  input,
		Stdout: output,
		Stderr: output,
	})
	return strings.TrimSpace(output.String()), err
}

// boundedBuffer keeps the first bytes written to it and drops the rest.
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	// Report everything as written, so the stream is never cut for being
	// talkative.
	return len(p), nil
}
