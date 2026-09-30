package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/version"
)

// Noticing that somebody changed what the panel applied.
//
// The README invites people to use kubectl, and nothing noticed when they did:
// an edited Deployment kept running as edited until the next deploy put it
// back, and the panel's page described something that was no longer there.
//
// What the panel applies is known exactly — it is rendered by the same code
// every time — so the comparison is between that and the live object, on the
// fields the panel sets and nowhere else. A field the panel never sets is
// somebody else's business: the API server's defaults, a controller's status,
// a label a tool added. Comparing those is how a drift check cries wolf on
// every object in the cluster.
//
// Server-side apply keeps a record of who set what, in managedFields, and that
// record settles the two questions a plain diff cannot:
//
//   - Who changed it. A field set with kubectl edit belongs to kubectl-edit
//     from then on, and the panel's own manager, "skifity", no longer owns it.
//   - Whether it was changed at all. A field the panel still owns and that
//     differs from what it would apply now is the panel's own change that has
//     not reached the cluster yet — a build variable waiting for a rebuild, a
//     sync that failed — and not somebody else's. It is never reported.
//
// One case is left: a field somebody removed. A removed field belongs to
// nobody, exactly like a field the panel is about to add for the first time.
// The difference is whether the live object is the one the panel last applied,
// and each applied object carries a fingerprint of itself to say so. Only when
// the fingerprint matches is a missing field somebody else's doing.

// AppliedAnnotation holds a fingerprint of an object exactly as the panel last
// applied it. It is metadata on the object itself, never on a pod template, so
// writing it restarts nothing.
var AppliedAnnotation = version.LabelKey("applied-hash")

// Prepare readies objects for applying: nils are dropped, each is converted to
// the generic form, and each carries the fingerprint of itself.
//
// Everything that applies an app's objects goes through here, and so does the
// drift check, which is what makes "the live object is the one the panel last
// applied" a comparison of two strings.
func Prepare(objects ...any) ([]*unstructured.Unstructured, error) {
	out := make([]*unstructured.Unstructured, 0, len(objects))
	for _, obj := range objects {
		if isNil(obj) {
			continue
		}
		u, err := ToUnstructured(obj)
		if err != nil {
			return nil, err
		}
		hash, err := AppliedHash(u)
		if err != nil {
			return nil, err
		}
		annotations := u.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[AppliedAnnotation] = hash
		u.SetAnnotations(annotations)
		out = append(out, u)
	}
	return out, nil
}

// AppliedHash fingerprints an object, leaving out its own fingerprint.
func AppliedHash(u *unstructured.Unstructured) (string, error) {
	c := u.DeepCopy()
	annotations := c.GetAnnotations()
	delete(annotations, AppliedAnnotation)
	c.SetAnnotations(annotations)
	data, err := json.Marshal(c.Object)
	if err != nil {
		return "", fmt.Errorf("fingerprint %s/%s: %w", u.GetKind(), u.GetName(), err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16]), nil
}

// The ways a live object can differ from the panel's.
const (
	// DriftChanged is a field with another value.
	DriftChanged = "changed"
	// DriftRemoved is a field, or an element of a list, that is not there.
	DriftRemoved = "removed"
	// DriftDeleted is an object that is not there at all.
	DriftDeleted = "deleted"
)

// DriftChange is one way a live object differs from what the panel applies.
type DriftChange struct {
	Kind string
	Name string
	// Path is where in the object, written the way kubectl explain reads:
	// spec.template.spec.containers[name=web].image. Empty for an object that
	// was deleted.
	Path   string
	Change string
	// Panel and Live are the two values, as text. Both are empty when Hidden.
	Panel string
	Live  string
	// Hidden is a value that is not shown, because it is a Secret's.
	Hidden bool
	// Manager is who changed it, as managedFields names them: kubectl-edit,
	// kubectl-patch, kubectl-client-side-apply. Empty when nobody can say.
	Manager   string
	ChangedAt time.Time
}

// DeletedObject is the drift of an object that is not in the cluster at all.
func DeletedObject(desired *unstructured.Unstructured) DriftChange {
	return DriftChange{Kind: desired.GetKind(), Name: desired.GetName(), Change: DriftDeleted}
}

// CompareObject reports how a live object differs from the one the panel
// would apply, on the fields the panel sets. ignore lists paths, in the form
// DriftChange.Path uses, that are not compared on this object.
func CompareObject(desired, live *unstructured.Unstructured, ignore ...string) []DriftChange {
	wanted, _ := AppliedHash(desired)
	exact := wanted != "" && live.GetAnnotations()[AppliedAnnotation] == wanted
	owners := managedOwners(live)
	panelApplied := owners.lastApply()

	var diffs []difference
	walkMap(desired.Object, live.Object, nil, &diffs)

	var out []DriftChange
	for _, diff := range diffs {
		readable := diff.path.String()
		if ignored(readable, ignore) || grownClaim(desired.GetKind(), readable, diff) {
			continue
		}
		change := DriftChange{Kind: desired.GetKind(), Name: desired.GetName(), Path: readable, Change: DriftChanged}
		if !diff.found {
			change.Change = DriftRemoved
		}

		other, panelTouches := owners.attribute(diff.path)
		switch {
		case other != nil:
			// Somebody else holds the field now: that is the change, and
			// they are who made it.
			change.Manager, change.ChangedAt = other.manager, other.time
		case panelTouches:
			// Still the panel's: a change of its own that has not been applied
			// yet, or one it made itself, such as a restore scaling the app
			// down. Neither is drift.
			continue
		case exact:
			// Nobody holds it and the object is the one the panel applied, so
			// somebody removed it. Whoever wrote to the object since is the
			// best answer to who.
			if latest := owners.latestOtherSince(panelApplied); latest != nil {
				change.Manager, change.ChangedAt = latest.manager, latest.time
			}
		default:
			// Nobody holds it and the object is not the one the panel last
			// applied: the panel has something new to apply. Not drift.
			continue
		}

		if isSecretData(desired.GetKind(), diff.path) {
			change.Hidden = true
		} else {
			change.Panel = display(diff.desired)
			if diff.found {
				change.Live = display(diff.live)
			}
		}
		out = append(out, change)
	}
	return out
}

// --- walking the object ---

// pathElem is one step into an object: a field of a map, or the element of a
// list that has the given values in its key fields.
type pathElem struct {
	field string
	key   map[string]any
}

type fieldPath []pathElem

func (p fieldPath) with(e pathElem) fieldPath {
	out := make(fieldPath, len(p), len(p)+1)
	copy(out, p)
	return append(out, e)
}

// String writes a path the way a person reads it:
// metadata.labels["app.kubernetes.io/name"], spec.template.spec.containers[name=web].image.
func (p fieldPath) String() string {
	var b strings.Builder
	for i, e := range p {
		if e.key != nil {
			names := make([]string, 0, len(e.key))
			for name := range e.key {
				names = append(names, name)
			}
			sort.Strings(names)
			parts := make([]string, 0, len(names))
			for _, name := range names {
				parts = append(parts, name+"="+display(e.key[name]))
			}
			b.WriteString("[" + strings.Join(parts, ",") + "]")
			continue
		}
		if strings.ContainsAny(e.field, "./") {
			b.WriteString(`["` + e.field + `"]`)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(e.field)
	}
	return b.String()
}

// difference is one leaf, or one whole value, that does not match.
type difference struct {
	path    fieldPath
	desired any
	live    any
	// found is false when the live object has nothing at the path.
	found bool
}

// skipped are the parts of an object that are identity or bookkeeping rather
// than anything the panel decides.
var skipped = map[string]bool{
	"apiVersion":                 true,
	"kind":                       true,
	"status":                     true,
	"metadata.name":              true,
	"metadata.namespace":         true,
	"metadata.managedFields":     true,
	"metadata.creationTimestamp": true,
	"metadata.resourceVersion":   true,
	"metadata.uid":               true,
	"metadata.generation":        true,
	"metadata.selfLink":          true,
	// The fingerprint is how the comparison knows what it is comparing, and
	// is never a difference of its own.
	`metadata.annotations["` + version.LabelKey("applied-hash") + `"]`: true,
}

func walkMap(desired, live map[string]any, path fieldPath, out *[]difference) {
	keys := make([]string, 0, len(desired))
	for key := range desired {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		child := path.with(pathElem{field: key})
		if skipped[child.String()] || empty(desired[key]) {
			continue
		}
		value, found := live[key]
		walkValue(desired[key], value, found && value != nil, child, out)
	}
}

func walkValue(desired, live any, found bool, path fieldPath, out *[]difference) {
	switch d := desired.(type) {
	case map[string]any:
		l, ok := live.(map[string]any)
		if !found || !ok {
			*out = append(*out, difference{path: path, desired: desired, live: live, found: found})
			return
		}
		walkMap(d, l, path, out)

	case []any:
		l, ok := live.([]any)
		keys := listKeys(path, d)
		if keys == nil {
			// An atomic list is one value: Kubernetes replaces it whole, so
			// it is compared whole. What the API server adds to an element —
			// a protocol nobody wrote — is not a difference.
			if !found || !ok || !subsetList(path, d, l) {
				*out = append(*out, difference{path: path, desired: desired, live: live, found: found && ok})
			}
			return
		}
		if !found || !ok {
			*out = append(*out, difference{path: path, desired: desired, live: live, found: false})
			return
		}
		for _, element := range d {
			m, _ := element.(map[string]any)
			key := elementKey(m, keys)
			match := findElement(l, keys, key)
			elementPath := path.with(pathElem{key: key})
			if match == nil {
				*out = append(*out, difference{path: elementPath, desired: element, found: false})
				continue
			}
			walkMap(m, match, elementPath, out)
		}

	default:
		if !found {
			*out = append(*out, difference{path: path, desired: desired, found: false})
			return
		}
		if !scalarEqual(path, desired, live) {
			*out = append(*out, difference{path: path, desired: desired, live: live, found: true})
		}
	}
}

// listKeys are the fields that identify an element of a list Kubernetes
// merges element by element, or nil for a list it treats as one value.
//
// These are the lists the panel writes, with the keys the API's own schema
// gives them. Everything else the panel writes — commands, ingress rules,
// network policy rules, tolerations, metrics — is atomic.
func listKeys(path fieldPath, elements []any) []string {
	if len(path) == 0 {
		return nil
	}
	for _, element := range elements {
		if _, ok := element.(map[string]any); !ok {
			return nil
		}
	}
	switch path[len(path)-1].field {
	case "containers", "initContainers", "ephemeralContainers", "env", "volumes", "imagePullSecrets":
		return []string{"name"}
	case "volumeMounts":
		return []string{"mountPath"}
	case "topologySpreadConstraints":
		return []string{"topologyKey", "whenUnsatisfiable"}
	case "ports":
		first, _ := elements[0].(map[string]any)
		if _, ok := first["containerPort"]; ok {
			return []string{"containerPort", "protocol"}
		}
		// A Service's own ports, at spec.ports.
		if _, ok := first["port"]; ok && len(path) == 2 && path[0].field == "spec" {
			return []string{"port", "protocol"}
		}
	}
	return nil
}

// elementKey is the values of an element's key fields. A port with no
// protocol is TCP, which is what the API server writes into it.
func elementKey(element map[string]any, keys []string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		value := element[key]
		if value == nil && key == "protocol" {
			value = "TCP"
		}
		out[key] = value
	}
	return out
}

func findElement(list []any, keys []string, key map[string]any) map[string]any {
	for _, element := range list {
		m, ok := element.(map[string]any)
		if !ok {
			continue
		}
		if keysEqual(elementKey(m, keys), key) {
			return m
		}
	}
	return nil
}

func keysEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for name, value := range a {
		other, ok := b[name]
		if !ok || !scalarEqual(nil, value, other) {
			return false
		}
	}
	return true
}

// subsetList reports whether a live atomic list is the desired one: the same
// elements in the same order, each carrying at least what the panel wrote.
func subsetList(path fieldPath, desired, live []any) bool {
	if len(desired) != len(live) {
		return false
	}
	for i := range desired {
		if !subsetValue(path, desired[i], live[i]) {
			return false
		}
	}
	return true
}

func subsetValue(path fieldPath, desired, live any) bool {
	switch d := desired.(type) {
	case map[string]any:
		l, ok := live.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range d {
			if empty(value) {
				continue
			}
			if !subsetValue(path.with(pathElem{field: key}), value, l[key]) {
				return false
			}
		}
		return true
	case []any:
		l, ok := live.([]any)
		return ok && subsetList(path, d, l)
	default:
		return scalarEqual(path, desired, live)
	}
}

// scalarEqual compares two leaf values: numbers by value, whatever type the
// decoder gave them, and resource quantities as quantities.
func scalarEqual(path fieldPath, a, b any) bool {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return x == y
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok && x != y && underResources(path) {
			qa, errA := resource.ParseQuantity(x)
			qb, errB := resource.ParseQuantity(y)
			return errA == nil && errB == nil && qa.Cmp(qb) == 0
		}
	}
	return reflect.DeepEqual(a, b)
}

func underResources(path fieldPath) bool {
	for _, e := range path {
		if e.field == "resources" {
			return true
		}
	}
	return false
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func ignored(path string, ignore []string) bool {
	for _, prefix := range ignore {
		if path == prefix || strings.HasPrefix(path, prefix+".") || strings.HasPrefix(path, prefix+"[") {
			return true
		}
	}
	return false
}

// grownClaim is a volume somebody made bigger. A claim can grow and never
// shrink, so putting the panel's size back would be refused; and a bigger
// volume harms nothing. It is left alone rather than reported as something to
// undo.
func grownClaim(kind, path string, diff difference) bool {
	if kind != "PersistentVolumeClaim" || path != "spec.resources.requests.storage" || !diff.found {
		return false
	}
	desired, errA := resource.ParseQuantity(fmt.Sprint(diff.desired))
	live, errB := resource.ParseQuantity(fmt.Sprint(diff.live))
	return errA == nil && errB == nil && live.Cmp(desired) >= 0
}

// isSecretData is a value inside a Secret, which is shown to nobody.
func isSecretData(kind string, path fieldPath) bool {
	return kind == "Secret" && len(path) > 0 && (path[0].field == "data" || path[0].field == "stringData")
}

// display writes a value as text, short enough for a table cell.
func display(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s = x
	case bool:
		s = strconv.FormatBool(x)
	default:
		if n, ok := number(x); ok {
			s = strconv.FormatFloat(n, 'f', -1, 64)
			break
		}
		encoded, err := json.Marshal(x)
		if err != nil {
			s = fmt.Sprint(x)
		} else {
			s = string(encoded)
		}
	}
	const limit = 240
	if runes := []rune(s); len(runes) > limit {
		s = string(runes[:limit-1]) + "…"
	}
	return s
}

// --- who set what ---

// fieldOwner is one entry of an object's managedFields: a manager, and the
// fields it holds, as the tree the API server writes.
type fieldOwner struct {
	manager   string
	operation string
	time      time.Time
	tree      map[string]any
}

type owners []fieldOwner

func managedOwners(live *unstructured.Unstructured) owners {
	var out owners
	for _, entry := range live.GetManagedFields() {
		if entry.FieldsV1 == nil {
			continue
		}
		var tree map[string]any
		if err := json.Unmarshal(entry.FieldsV1.GetRawBytes(), &tree); err != nil {
			continue
		}
		owner := fieldOwner{manager: entry.Manager, operation: string(entry.Operation), tree: tree}
		if entry.Time != nil {
			owner.time = entry.Time.Time
		}
		out = append(out, owner)
	}
	return out
}

// isPanel is the panel's own manager: its server-side apply, and the updates
// it makes directly — a restart, a restore scaling the app down — which
// client-go files under the same name, taken from the user agent.
func (o fieldOwner) isPanel() bool { return o.manager == FieldManager }

// attribute finds who holds a path: another manager, if one does, and whether
// the panel does.
func (os owners) attribute(path fieldPath) (other *fieldOwner, panel bool) {
	for i := range os {
		if !os[i].touches(path) {
			continue
		}
		if os[i].isPanel() {
			panel = true
			continue
		}
		if other == nil || os[i].time.After(other.time) {
			other = &os[i]
		}
	}
	return other, panel
}

// lastApply is when the panel last applied the object.
func (os owners) lastApply() time.Time {
	var at time.Time
	for _, o := range os {
		if o.isPanel() && o.operation == "Apply" && o.time.After(at) {
			at = o.time
		}
	}
	return at
}

// latestOtherSince is the manager that wrote to the object most recently
// after the panel's own apply.
func (os owners) latestOtherSince(since time.Time) *fieldOwner {
	var latest *fieldOwner
	for i := range os {
		if os[i].isPanel() || os[i].time.Before(since) {
			continue
		}
		if latest == nil || os[i].time.After(latest.time) {
			latest = &os[i]
		}
	}
	return latest
}

// touches reports whether a manager holds the field at a path, something
// under it, or a whole value it is part of.
func (o fieldOwner) touches(path fieldPath) bool {
	node := o.tree
	for _, e := range path {
		next, ok := childNode(node, e)
		if !ok {
			return false
		}
		if len(next) == 0 {
			// A leaf: the manager holds this value whole, and everything in it.
			return true
		}
		node = next
	}
	return true
}

func childNode(node map[string]any, e pathElem) (map[string]any, bool) {
	if e.key == nil {
		child, ok := node["f:"+e.field].(map[string]any)
		return child, ok
	}
	for name, child := range node {
		raw, found := strings.CutPrefix(name, "k:")
		if !found {
			continue
		}
		var key map[string]any
		if err := json.Unmarshal([]byte(raw), &key); err != nil {
			continue
		}
		// The API server writes only the key fields it has, and a port with
		// no protocol is written with the one it defaulted.
		if keysEqual(withDefaults(key, e.key), e.key) {
			m, ok := child.(map[string]any)
			return m, ok
		}
	}
	return nil, false
}

// withDefaults is a key as the API server wrote it, with the protocol it
// defaults filled in when the key being looked for has one.
func withDefaults(key, like map[string]any) map[string]any {
	_, wanted := like["protocol"]
	_, written := key["protocol"]
	if !wanted || written {
		return key
	}
	out := maps.Clone(key)
	out["protocol"] = "TCP"
	return out
}
