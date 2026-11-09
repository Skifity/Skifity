package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeLogs gives every instance a log of its own, which the fake clientset
// cannot: it answers every pod with the same two words. It remembers what
// each was asked for.
type fakeLogs struct {
	mu sync.Mutex
	// written is each instance's log so far, as the kubelet sends it with
	// timestamps; follow is what it writes once somebody follows it.
	written map[string][]string
	follow  map[string][]string
	asked   map[string][]corev1.PodLogOptions
}

func (f *fakeLogs) open(ctx context.Context, _, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked == nil {
		f.asked = map[string][]corev1.PodLogOptions{}
	}
	f.asked[pod] = append(f.asked[pod], *options)
	lines := f.written[pod]
	if options.Follow {
		lines = append(slices.Clone(lines), f.follow[pod]...)
	}
	if options.SinceTime != nil {
		var kept []string
		for _, line := range lines {
			at, _ := splitTimestamp(line)
			if !at.Before(options.SinceTime.Time) {
				kept = append(kept, line)
			}
		}
		lines = kept
	}
	body := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if !options.Follow {
		return io.NopCloser(body), nil
	}
	return &openLog{ctx: ctx, body: body}, nil
}

func (f *fakeLogs) options(pod string) []corev1.PodLogOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked[pod])
}

// openLog is a followed log: what has been written, then nothing until the
// reader goes away, as a container that is still running.
type openLog struct {
	ctx  context.Context
	body io.Reader
}

func (o *openLog) Read(p []byte) (int, error) {
	n, err := o.body.Read(p)
	if errors.Is(err, io.EOF) && n == 0 {
		<-o.ctx.Done()
		return 0, io.EOF
	}
	return n, nil
}

func (o *openLog) Close() error { return nil }

func stamp(second int, line string) string {
	return time.Date(2026, 11, 9, 10, 0, second, 0, time.UTC).Format(time.RFC3339Nano) + " " + line
}

func logPod(name, app string, started, ready bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: map[string]string{"app.kubernetes.io/name": app}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}
	if started {
		pod.Status.Phase = corev1.PodRunning
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}
	}
	if ready {
		pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	}
	return pod
}

// collect reads entries until want of them have arrived, or fails.
func collect(t *testing.T, stream *LogStream, want int) []LogEntry {
	t.Helper()
	var got []LogEntry
	deadline := time.After(5 * time.Second)
	for len(got) < want {
		select {
		case entry, ok := <-stream.Entries():
			if !ok {
				t.Fatalf("the stream ended after %d of %d lines: %v", len(got), want, got)
			}
			got = append(got, entry)
		case <-deadline:
			t.Fatalf("only %d of %d lines arrived: %v", len(got), want, got)
		}
	}
	return got
}

func lineTexts(entries []LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Instance+": "+entry.Line)
	}
	return out
}

// An app at three instances showed a third of its output, and nothing said
// so. Every instance that has started is read now, its lines merged in the
// order they were written and each tagged with the instance that wrote it.
func TestEveryInstancesLogIsReadAndMerged(t *testing.T) {
	stopping := logPod("web-5d8f-ccccc", "web", true, false)
	stopping.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	clientset := fake.NewSimpleClientset(
		logPod("web-5d8f-aaaaa", "web", true, true),
		logPod("web-5d8f-bbbbb", "web", true, false),
		stopping,
		logPod("web-5d8f-ddddd", "web", false, false),
		logPod("api-77c9-xxxxx", "api", true, true),
	)
	logs := &fakeLogs{
		written: map[string][]string{
			"web-5d8f-aaaaa": {stamp(1, "a1"), stamp(3, "a2")},
			"web-5d8f-bbbbb": {stamp(2, "b1")},
			"web-5d8f-ccccc": {stamp(2, "c1")},
			"api-77c9-xxxxx": {stamp(2, "x1")},
		},
	}
	c := &Client{clientset: clientset, logOpener: logs.open}

	stream, err := c.AppLogs(t.Context(), "ns", "web", LogOptions{TailLines: 200, SinceSeconds: 3600})
	if err != nil {
		t.Fatalf("AppLogs: %v", err)
	}
	got := lineTexts(collect(t, stream, 3))
	want := []string{"web-5d8f-aaaaa: a1", "web-5d8f-bbbbb: b1", "web-5d8f-aaaaa: a2"}
	if !slices.Equal(got, want) {
		t.Fatalf("the merged log is %q, want %q", got, want)
	}
	if _, open := <-stream.Entries(); open {
		t.Fatal("a read that is not followed did not end")
	}
	instances, omitted := stream.Instances()
	if !slices.Equal(instances, []string{"web-5d8f-aaaaa", "web-5d8f-bbbbb"}) || omitted != 0 {
		t.Fatalf("the read names %v and %d left out", instances, omitted)
	}

	// The one being stopped, the one that has never started and another
	// app's were never opened.
	for _, pod := range []string{"web-5d8f-ccccc", "web-5d8f-ddddd", "api-77c9-xxxxx"} {
		if asked := logs.options(pod); len(asked) > 0 {
			t.Errorf("%s was read: %+v", pod, asked)
		}
	}
	asked := logs.options("web-5d8f-aaaaa")
	if len(asked) != 1 || !asked[0].Timestamps || asked[0].SinceSeconds == nil || *asked[0].SinceSeconds != 3600 ||
		asked[0].TailLines == nil || *asked[0].TailLines != 200 {
		t.Fatalf("the instance was asked for %+v", asked)
	}
}

// The newest lines of the whole app, not of each instance: three instances
// at a tail of two are two lines, not six.
func TestTheTailIsTheWholeApps(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		logPod("web-1", "web", true, true),
		logPod("web-2", "web", true, true),
	)
	logs := &fakeLogs{written: map[string][]string{
		"web-1": {stamp(1, "one"), stamp(4, "four")},
		"web-2": {stamp(2, "two"), stamp(3, "three")},
	}}
	c := &Client{clientset: clientset, logOpener: logs.open}
	stream, err := c.AppLogs(t.Context(), "ns", "web", LogOptions{TailLines: 2})
	if err != nil {
		t.Fatalf("AppLogs: %v", err)
	}
	if got := lineTexts(collect(t, stream, 2)); !slices.Equal(got, []string{"web-2: three", "web-1: four"}) {
		t.Fatalf("the tail is %q", got)
	}
}

// Following, a new instance joins as soon as it is seen, a line is not sent
// twice where the first read and the stream overlap, and the instances being
// read are said again when they change.
func TestAFollowedLogPicksUpANewInstance(t *testing.T) {
	stopping := logPod("web-5d8f-ccccc", "web", true, false)
	stopping.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	clientset := fake.NewSimpleClientset(
		logPod("web-5d8f-aaaaa", "web", true, true),
		logPod("web-5d8f-bbbbb", "web", true, true),
		stopping,
		logPod("web-5d8f-ddddd", "web", false, false),
	)
	logs := &fakeLogs{
		written: map[string][]string{
			"web-5d8f-aaaaa": {stamp(1, "a1"), stamp(3, "a2")},
			"web-5d8f-bbbbb": {stamp(2, "b1")},
			"web-5d8f-ccccc": {stamp(2, "c1")},
		},
		follow: map[string][]string{
			"web-5d8f-aaaaa": {stamp(4, "a3")},
			"web-5d8f-bbbbb": {stamp(5, "b2")},
			"web-5d8f-eeeee": {stamp(6, "e1")},
		},
	}
	c := &Client{clientset: clientset, logOpener: logs.open, logRelist: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := c.AppLogs(ctx, "ns", "web", LogOptions{TailLines: 100, Follow: true})
	if err != nil {
		t.Fatalf("AppLogs: %v", err)
	}
	defer stream.Close()

	got := lineTexts(collect(t, stream, 5))
	if !slices.Equal(got[:3], []string{"web-5d8f-aaaaa: a1", "web-5d8f-bbbbb: b1", "web-5d8f-aaaaa: a2"}) {
		t.Fatalf("the first read is %q", got[:3])
	}
	live := got[3:]
	slices.Sort(live)
	if !slices.Equal(live, []string{"web-5d8f-aaaaa: a3", "web-5d8f-bbbbb: b2"}) {
		t.Fatalf("followed, the instances wrote %q", live)
	}
	// Carried on from the newest line read, not from the start again.
	if asked := logs.options("web-5d8f-aaaaa"); len(asked) != 2 || asked[1].SinceTime == nil ||
		!asked[1].SinceTime.Time.Equal(time.Date(2026, 11, 9, 10, 0, 3, 0, time.UTC)) {
		t.Fatalf("the instance was followed with %+v", asked)
	}

	if _, err := clientset.CoreV1().Pods("ns").Create(t.Context(),
		logPod("web-5d8f-eeeee", "web", true, false), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	joined := collect(t, stream, 1)
	if joined[0].Instance != "web-5d8f-eeeee" || joined[0].Line != "e1" {
		t.Fatalf("the new instance wrote %+v", joined[0])
	}
	select {
	case <-stream.Changed():
	case <-time.After(5 * time.Second):
		t.Fatal("the change in instances was not said")
	}
	instances, _ := stream.Instances()
	if !slices.Equal(instances, []string{"web-5d8f-aaaaa", "web-5d8f-bbbbb", "web-5d8f-eeeee"}) {
		t.Fatalf("the read names %v", instances)
	}
	for _, pod := range []string{"web-5d8f-ccccc", "web-5d8f-ddddd"} {
		if asked := logs.options(pod); len(asked) > 0 {
			t.Errorf("%s was read: %+v", pod, asked)
		}
	}

	// Leaving ends it, and every instance's stream with it.
	stream.Close()
	select {
	case <-drain(stream):
	case <-time.After(5 * time.Second):
		t.Fatal("closing a followed log did not end it")
	}
}

func drain(stream *LogStream) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream.Entries() {
		}
	}()
	return done
}

// An instance is asked for by name only among the ones read: another app's,
// or the web's while a worker is asked for, is not one of them.
func TestAnInstanceIsFoundOnlyAmongTheOnesAskedAbout(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		logPod("web-5d8f-aaaaa", "web", true, true),
		logPod("web--worker-6c7b-wwwww", "web--worker", true, true),
		logPod("api-77c9-xxxxx", "api", true, true),
	)
	logs := &fakeLogs{written: map[string][]string{
		"web-5d8f-aaaaa":         {stamp(1, "web")},
		"web--worker-6c7b-wwwww": {stamp(1, "worker")},
	}}
	c := &Client{clientset: clientset, logOpener: logs.open}

	for _, tc := range []struct{ name, instance string }{
		{"web", "api-77c9-xxxxx"},
		{"web--worker", "web-5d8f-aaaaa"},
		{"web", "nothing"},
	} {
		_, err := c.AppLogs(t.Context(), "ns", tc.name, LogOptions{Instance: tc.instance})
		var unknown *UnknownInstance
		if !errors.As(err, &unknown) || unknown.Name != tc.instance {
			t.Errorf("%s asked for %s answered %v", tc.name, tc.instance, err)
		}
	}

	// By its full name, or by the end of it the panel prints beside a line.
	for _, instance := range []string{"web--worker-6c7b-wwwww", "wwwww"} {
		stream, err := c.AppLogs(t.Context(), "ns", "web--worker", LogOptions{Instance: instance})
		if err != nil {
			t.Fatalf("the worker asked for %s: %v", instance, err)
		}
		if got := lineTexts(collect(t, stream, 1)); got[0] != "web--worker-6c7b-wwwww: worker" {
			t.Fatalf("the worker's instance wrote %q", got)
		}
	}
}

// Forty instances are not forty streams: twenty are read, and the read says
// how many it left out rather than leaving them out quietly.
func TestAReadSaysWhichInstancesItLeftOut(t *testing.T) {
	var objects []*corev1.Pod
	logs := &fakeLogs{written: map[string][]string{}}
	for i := range 25 {
		name := fmt.Sprintf("web-%02d", i)
		objects = append(objects, logPod(name, "web", true, i >= 20))
		logs.written[name] = []string{stamp(i, name)}
	}
	clientset := fake.NewSimpleClientset()
	for _, pod := range objects {
		if _, err := clientset.CoreV1().Pods("ns").Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	c := &Client{clientset: clientset, logOpener: logs.open}
	stream, err := c.AppLogs(t.Context(), "ns", "web", LogOptions{})
	if err != nil {
		t.Fatalf("AppLogs: %v", err)
	}
	instances, omitted := stream.Instances()
	if len(instances) != MaxLogInstances || omitted != 5 {
		t.Fatalf("read %d and left out %d", len(instances), omitted)
	}
	// The ready ones are read first.
	for _, ready := range []string{"web-20", "web-24"} {
		if !slices.Contains(instances, ready) {
			t.Errorf("the ready %s was left out of %v", ready, instances)
		}
	}
}

// A line longer than the reader's buffer is cut, rather than ending the read
// of every line after it. A line with no time keeps its neighbour's.
func TestALongLineDoesNotEndTheRead(t *testing.T) {
	long := strings.Repeat("x", maxLogLine+500)
	body := stamp(1, "before") + "\n" + stamp(2, long) + "\nno time here\n" + stamp(3, "after") + "\n"
	var got []LogEntry
	if err := readLogLines(strings.NewReader(body), "web-1", func(entry LogEntry) bool {
		got = append(got, entry)
		return true
	}); err != nil {
		t.Fatalf("readLogLines: %v", err)
	}
	if len(got) != 4 || got[0].Line != "before" || got[3].Line != "after" {
		t.Fatalf("read %d lines", len(got))
	}
	// The cut is in the raw line, which still has its 21-byte time in front.
	if n := len(got[1].Line); n < maxLogLine-40 || n > maxLogLine {
		t.Errorf("the long line kept %d bytes", n)
	}
	if got[2].Line != "no time here" || !got[2].At.Equal(got[1].At) {
		t.Errorf("a line without a time came back as %+v", got[2])
	}
}
