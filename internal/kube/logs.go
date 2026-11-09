package kube

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/runsafe"
)

// LogEntry is one line of an app's log, with the instance that wrote it.
type LogEntry struct {
	// Instance is the name of the instance that wrote the line.
	Instance string
	// At is when, by the kubelet's clock. A line that came without a time has
	// the time of the line before it, so it stays beside it when instances
	// are merged.
	At   time.Time
	Line string
}

// MaxLogInstances is how many of an app's instances one read follows.
//
// Each is a stream the API server holds open for as long as somebody reads,
// so an app at forty instances has twenty read, and the read says how many it
// left out. Reading one instance of three without saying so is what this
// replaced.
const MaxLogInstances = 20

// maxLogLine is the longest line kept whole. A longer one is cut there: the
// scanner this replaced ended the whole read at the first such line.
const maxLogLine = 1 << 20

// defaultLogRelist is how often a followed log looks again for the app's
// instances, so that a new one joins and one that has gone is let go.
const defaultLogRelist = 10 * time.Second

// relistFailures is how many looks in a row may fail, with nothing left being
// read, before a followed log stops and says why.
const relistFailures = 3

// clockSlack is how far apart a server's clock and the panel's may be.
const clockSlack = 30 * time.Second

// UnknownInstance is an instance asked for by a name that none of the
// instances asked about has: another app's, another process's, or one that
// has gone.
type UnknownInstance struct {
	Name string
}

func (e *UnknownInstance) Error() string {
	return fmt.Sprintf("%s is not one of this app's instances", e.Name)
}

// LogStream is an app's log being read: one instance's, or every instance's
// merged in the order the lines were written.
type LogStream struct {
	entries chan LogEntry
	changed chan struct{}
	cancel  context.CancelFunc

	mu        sync.Mutex
	instances []string
	omitted   int
	err       error
}

// NewLogStream is a read that is already complete: these instances were read,
// omitted more were not, and these are their lines.
func NewLogStream(instances []string, omitted int, entries []LogEntry) *LogStream {
	s := newLogStream(func() {}, len(entries))
	s.instances, s.omitted = instances, omitted
	for _, entry := range entries {
		s.entries <- entry
	}
	close(s.entries)
	return s
}

func newLogStream(cancel context.CancelFunc, buffer int) *LogStream {
	return &LogStream{
		entries: make(chan LogEntry, buffer),
		changed: make(chan struct{}, 1),
		cancel:  cancel,
	}
}

// Entries delivers the lines. It is closed at the end of a read that is not
// followed, and when a followed one stops.
func (s *LogStream) Entries() <-chan LogEntry { return s.entries }

// Changed is signalled when the instances being read change: one started and
// joined, or one went.
func (s *LogStream) Changed() <-chan struct{} { return s.changed }

// Instances names the instances being read, and says how many more there are
// that are not.
func (s *LogStream) Instances() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.instances), s.omitted
}

// Err is why a followed read stopped by itself, once Entries is closed.
func (s *LogStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close stops the read.
func (s *LogStream) Close() { s.cancel() }

func (s *LogStream) setInstances(names []string, omitted int) {
	s.mu.Lock()
	same := slices.Equal(s.instances, names) && s.omitted == omitted
	s.instances, s.omitted = names, omitted
	s.mu.Unlock()
	if !same {
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
}

// logOpener opens one instance's log. A test gives each instance a log of its
// own; the fake clientset answers every pod with the same two words.
type logOpener func(ctx context.Context, namespace, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error)

func (c *Client) openLog(ctx context.Context, namespace, pod string, options *corev1.PodLogOptions) (io.ReadCloser, error) {
	if c.logOpener != nil {
		return c.logOpener(ctx, namespace, pod, options)
	}
	return c.StreamClientset().CoreV1().Pods(namespace).GetLogs(pod, options).Stream(ctx)
}

func (c *Client) logRelistEvery() time.Duration {
	if c.logRelist > 0 {
		return c.logRelist
	}
	return defaultLogRelist
}

// logTargets chooses the instances a read starts with, and says how many
// readable ones it left out.
func logTargets(pods []corev1.Pod, opts LogOptions) ([]corev1.Pod, int, error) {
	if opts.Instance != "" {
		pod, ok := findInstance(pods, opts.Instance)
		if !ok {
			return nil, 0, &UnknownInstance{Name: opts.Instance}
		}
		if !opts.Previous && !hasStarted(pod) {
			return nil, 0, &NotStarted{Instance: pod.Name, Reason: waitingFor(pod)}
		}
		return []corev1.Pod{pod}, 0, nil
	}
	// The container before the last restart is one instance's: the one that
	// restarted most, which is the one somebody asking for it means.
	if opts.Previous {
		return []corev1.Pod{pickLogPod(pods, true)}, 0, nil
	}
	readable := readableInstances(pods, nil)
	if len(readable) == 0 {
		// An instance that has never started has no log, and the API
		// server's answer for it came out of the panel as something gone
		// wrong. What it is waiting for is the answer.
		target := pickLogPod(pods, false)
		return nil, 0, &NotStarted{Instance: target.Name, Reason: waitingFor(target)}
	}
	if len(readable) > MaxLogInstances {
		return readable[:MaxLogInstances], len(readable) - MaxLogInstances, nil
	}
	return readable, 0, nil
}

// readableInstances are the instances with a log worth reading, in the order
// they are chosen: those already being read, then ready ones, then by name.
//
// One being stopped is left out, unless it is already being read — its last
// lines are often why it stopped — or it is all there is.
func readableInstances(pods []corev1.Pod, reading map[string]bool) []corev1.Pod {
	var live, stopping []corev1.Pod
	for _, pod := range pods {
		switch {
		case !hasStarted(pod):
		case pod.DeletionTimestamp == nil, reading[pod.Name]:
			live = append(live, pod)
		default:
			stopping = append(stopping, pod)
		}
	}
	if len(live) == 0 {
		live = stopping
	}
	slices.SortStableFunc(live, func(a, b corev1.Pod) int {
		if reading[a.Name] != reading[b.Name] {
			if reading[a.Name] {
				return -1
			}
			return 1
		}
		if podReady(a) != podReady(b) {
			if podReady(a) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return live
}

// findInstance finds an instance by its name, or by the part after its last
// hyphen — what the panel and the CLI print beside each line — when exactly
// one instance ends in it.
//
// Only among pods: the instances of the app or process being read, so a
// name from another app, or the web's while a worker is asked for, is not
// found.
func findInstance(pods []corev1.Pod, name string) (corev1.Pod, bool) {
	var found []corev1.Pod
	for _, pod := range pods {
		if pod.Name == name {
			return pod, true
		}
		if strings.HasSuffix(pod.Name, "-"+name) {
			found = append(found, pod)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return corev1.Pod{}, false
}

// readLogs reads what each instance has written so far, all at once, and
// merges it in the order it was written, keeping the newest TailLines of the
// whole. It returns the instances it could read and, for each, the time of
// the newest line it read, which is where following one carries on from.
//
// It fails only when no instance could be read: one that could not is left
// out of the instances it names.
func (c *Client) readLogs(ctx context.Context, namespace string, pods []corev1.Pod, opts LogOptions) (
	[]LogEntry, []string, map[string]time.Time, error,
) {
	type result struct {
		entries []LogEntry
		err     error
	}
	results := make([]result, len(pods))
	var wg sync.WaitGroup
	for i, pod := range pods {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer runsafe.Recover(nil, "reading an instance's log", func(err error) { results[i].err = err })
			options := &corev1.PodLogOptions{Previous: opts.Previous, Timestamps: true}
			if opts.TailLines > 0 {
				tail := opts.TailLines
				options.TailLines = &tail
			}
			if opts.SinceSeconds > 0 {
				since := opts.SinceSeconds
				options.SinceSeconds = &since
			}
			stream, err := c.openLog(ctx, namespace, pod.Name, options)
			if err != nil {
				if opts.Previous {
					// The API server answers 400 when there is no earlier
					// container, which is the ordinary case for an app that
					// has not crashed.
					results[i].err = fmt.Errorf("%s has not restarted, so there is no earlier log to read", pod.Name)
				} else {
					results[i].err = fmt.Errorf("read logs from %s: %w", pod.Name, err)
				}
				return
			}
			defer stream.Close()
			// A read that breaks halfway keeps what it had: the lines are
			// still true, and the next read has the rest.
			_ = readLogLines(stream, pod.Name, func(entry LogEntry) bool {
				results[i].entries = append(results[i].entries, entry)
				return true
			})
		}()
	}
	wg.Wait()

	var (
		all      []LogEntry
		read     []string
		newest   = map[string]time.Time{}
		firstErr error
	)
	for i, pod := range pods {
		if results[i].err != nil {
			if firstErr == nil {
				firstErr = results[i].err
			}
			continue
		}
		read = append(read, pod.Name)
		if n := len(results[i].entries); n > 0 {
			newest[pod.Name] = results[i].entries[n-1].At
		}
		all = append(all, results[i].entries...)
	}
	if len(read) == 0 {
		return nil, nil, nil, firstErr
	}
	slices.Sort(read)
	// Stable, so lines one instance wrote in the same instant keep the order
	// it wrote them in.
	slices.SortStableFunc(all, func(a, b LogEntry) int { return a.At.Compare(b.At) })
	if opts.TailLines > 0 && int64(len(all)) > opts.TailLines {
		all = all[int64(len(all))-opts.TailLines:]
	}
	return all, read, newest, nil
}

// readLogLines reads a log the kubelet wrote with its timestamps, a line at a
// time, and hands each on with its time taken off the front. It stops when
// emit returns false, or at the end of the log.
func readLogLines(r io.Reader, instance string, emit func(LogEntry) bool) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var last time.Time
	for {
		raw, err := readLogLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		at, line := splitTimestamp(raw)
		if at.IsZero() {
			at = last
		}
		last = at
		if !emit(LogEntry{Instance: instance, At: at, Line: line}) {
			return nil
		}
	}
}

// readLogLine reads one line without its ending, keeping at most maxLogLine
// bytes of it.
func readLogLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		chunk, more, err := reader.ReadLine()
		if err != nil {
			// The log ended in the middle of a line too long for the buffer:
			// what was read of it is the line.
			if len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
		if room := maxLogLine - len(line); room > 0 {
			line = append(line, chunk[:min(len(chunk), room)]...)
		}
		if !more {
			return string(line), nil
		}
	}
}

// splitTimestamp takes the kubelet's RFC 3339 time off the front of a line.
// A line without one comes back whole, with no time.
func splitTimestamp(raw string) (time.Time, string) {
	stamp, rest, found := strings.Cut(raw, " ")
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, raw
	}
	if !found {
		rest = ""
	}
	return at, rest
}

// follower keeps an app's log open: a stream from every instance being read,
// and a look every few seconds for instances that started or went.
type follower struct {
	client    *Client
	namespace string
	list      func(context.Context) ([]corev1.Pod, error)
	opts      LogOptions
	// only is the one instance being read, by its full name, when one was
	// asked for.
	only   string
	stream *LogStream
	ctx    context.Context

	readers map[string]*logReader
	// after is, for each instance, the time of the newest line sent from
	// it: following it again carries on from there.
	after map[string]time.Time
	// from is where to start an instance the first read had nothing from.
	from     map[string]time.Time
	ended    chan readerEnd
	wg       sync.WaitGroup
	next     int
	failures int
	stopOnce sync.Once
}

type logReader struct {
	id     int
	cancel context.CancelFunc
}

type readerEnd struct {
	instance string
	id       int
	after    time.Time
}

// followLogs sends backlog, then every line the instances write from here
// on, and keeps the set of instances current. begun is when the backlog was
// asked for.
func (c *Client) followLogs(ctx context.Context, namespace string, list func(context.Context) ([]corev1.Pod, error),
	opts LogOptions, only string, backlog []LogEntry, read []string, omitted int, after map[string]time.Time,
	begun time.Time,
) *LogStream {
	ctx, cancel := context.WithCancel(ctx)
	stream := newLogStream(cancel, 256)
	stream.instances, stream.omitted = read, omitted

	// An instance the first read had nothing from is followed from a little
	// before it was asked: its node's clock and the panel's are not the same
	// clock, and a line written just after, stamped just before, would be
	// lost. Nothing there can be sent twice — had there been anything, the
	// first read would have had it — but nothing older than the read asked
	// for is wanted either.
	from := map[string]time.Time{}
	start := begun.Add(-clockSlack)
	if opts.SinceSeconds > 0 {
		start = latestOf(start, begun.Add(-time.Duration(opts.SinceSeconds)*time.Second))
	}
	for _, name := range read {
		if _, ok := after[name]; !ok {
			from[name] = start
		}
	}
	f := &follower{
		client: c, namespace: namespace, list: list, opts: opts, only: only,
		stream: stream, ctx: ctx,
		readers: map[string]*logReader{}, after: after, from: from, ended: make(chan readerEnd),
	}
	go func() {
		defer runsafe.Recover(nil, "following an app's log", func(err error) { f.stop(err) })
		f.stop(f.run(backlog, read))
	}()
	return stream
}

// stop ends every read and closes the stream, once.
func (f *follower) stop(err error) {
	f.stopOnce.Do(func() {
		f.stream.cancel()
		f.wg.Wait()
		f.stream.mu.Lock()
		f.stream.err = err
		f.stream.mu.Unlock()
		close(f.stream.entries)
	})
}

func (f *follower) run(backlog []LogEntry, read []string) error {
	for _, entry := range backlog {
		if !send(f.ctx, f.stream.entries, entry) {
			return nil
		}
	}
	for _, name := range read {
		f.start(name)
	}

	ticker := time.NewTicker(f.client.logRelistEvery())
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return nil
		case end := <-f.ended:
			// The instance's container stopped, or it was let go. It is not
			// opened again at once: the next look does that if it is still
			// there, which keeps a container that keeps crashing from being
			// reopened in a tight loop.
			if reader, ok := f.readers[end.instance]; ok && reader.id == end.id {
				reader.cancel()
				delete(f.readers, end.instance)
			}
			if end.after.After(f.after[end.instance]) {
				f.after[end.instance] = end.after
			}
		case <-ticker.C:
			if err := f.relist(); err != nil {
				return err
			}
		}
	}
}

// relist looks at the app's instances again: one that started joins, and one
// that has gone is let go.
func (f *follower) relist() error {
	pods, err := f.list(f.ctx)
	if err != nil {
		f.failures++
		if f.failures >= relistFailures && len(f.readers) == 0 {
			return err
		}
		return nil
	}
	f.failures = 0

	var want []string
	omitted := 0
	if f.only != "" {
		for _, pod := range pods {
			if pod.Name == f.only && hasStarted(pod) {
				want = []string{pod.Name}
			}
		}
	} else {
		current, _ := f.stream.Instances()
		reading := map[string]bool{}
		for _, name := range current {
			reading[name] = true
		}
		readable := readableInstances(pods, reading)
		if len(readable) > MaxLogInstances {
			omitted = len(readable) - MaxLogInstances
			readable = readable[:MaxLogInstances]
		}
		for _, pod := range readable {
			want = append(want, pod.Name)
		}
	}

	wanted := map[string]bool{}
	for _, name := range want {
		wanted[name] = true
	}
	for name, reader := range f.readers {
		if !wanted[name] {
			reader.cancel()
			delete(f.readers, name)
		}
	}
	present := map[string]bool{}
	for _, pod := range pods {
		present[pod.Name] = true
	}
	for name := range f.after {
		if !present[name] {
			delete(f.after, name)
		}
	}
	for name := range f.from {
		if !present[name] {
			delete(f.from, name)
		}
	}
	for _, name := range want {
		if _, reading := f.readers[name]; !reading {
			f.start(name)
		}
	}
	slices.Sort(want)
	f.stream.setInstances(want, omitted)
	return nil
}

// start follows one instance's log from where it was left.
func (f *follower) start(instance string) {
	ctx, cancel := context.WithCancel(f.ctx)
	f.next++
	id := f.next
	f.readers[instance] = &logReader{id: id, cancel: cancel}
	after, from := f.after[instance], f.from[instance]
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		defer runsafe.Recover(nil, "following an instance's log", nil)
		end := readerEnd{instance: instance, id: id, after: after}
		defer func() {
			select {
			case f.ended <- end:
			case <-f.ctx.Done():
			}
		}()
		end.after = f.follow(ctx, instance, after, from)
	}()
}

// follow reads one instance's log as it is written, from after — or from,
// when nothing has been sent from it yet — and returns the time of the newest
// line it sent.
func (f *follower) follow(ctx context.Context, instance string, after, from time.Time) time.Time {
	options := &corev1.PodLogOptions{Follow: true, Timestamps: true}
	switch {
	case !after.IsZero():
		options.SinceTime = &metav1.Time{Time: after}
	case !from.IsZero():
		options.SinceTime = &metav1.Time{Time: from}
	case f.opts.TailLines > 0:
		// An instance that started after the read began: everything in its
		// log is new, as far back as the read was asked to go.
		tail := f.opts.TailLines
		options.TailLines = &tail
	}
	stream, err := f.client.openLog(ctx, f.namespace, instance, options)
	if err != nil {
		return after
	}
	defer stream.Close()

	newest := after
	_ = readLogLines(stream, instance, func(entry LogEntry) bool {
		// Kubernetes counts from the start of the second, and the lines in
		// it up to after have been sent already.
		if !after.IsZero() && !entry.At.IsZero() && !entry.At.After(after) {
			return true
		}
		if !send(ctx, f.stream.entries, entry) {
			return false
		}
		if entry.At.After(newest) {
			newest = entry.At
		}
		return true
	})
	return newest
}

func send(ctx context.Context, entries chan<- LogEntry, entry LogEntry) bool {
	select {
	case entries <- entry:
		return true
	case <-ctx.Done():
		return false
	}
}

func latestOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
