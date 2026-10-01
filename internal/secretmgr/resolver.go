package secretmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"skifity/internal/crypto"
	"skifity/internal/errdoc"
	"skifity/internal/store"
)

// provider is one kind of secret manager, signed in.
type provider interface {
	// test proves the credentials work, and reads no secret.
	test(ctx context.Context) error
	// fetch reads one secret.
	fetch(ctx context.Context, path string) (secret, error)
}

func newProvider(client *http.Client, kind string, settings, credentials map[string]string) (provider, error) {
	c := caller{client: sameHost(client), label: KindLabel(kind)}
	switch kind {
	case KindVault:
		return newVault(c, settings, credentials), nil
	case KindInfisical:
		return newInfisical(c, settings, credentials), nil
	case KindDoppler:
		return newDoppler(c, credentials), nil
	case KindAWS:
		return newAWS(c, settings, credentials), nil
	}
	return nil, fmt.Errorf("%q is not a kind of secret manager", kind)
}

// Resolver reads the values of variables that are references, and tests
// connections.
type Resolver struct {
	db      *store.DB
	keyring *crypto.Keyring
	// Client makes every request. The panel's is netguard's; a test gives
	// one that may reach the loopback address its fake manager listens on.
	Client *http.Client
}

// New builds a Resolver whose requests go through internal/netguard.
func New(db *store.DB, keyring *crypto.Keyring) *Resolver {
	return &Resolver{db: db, keyring: keyring, Client: guarded()}
}

// SealCredentials seals a connection's credentials for storing, under the
// connection's own id.
func SealCredentials(keyring *crypto.Keyring, connectionID string, credentials map[string]string) (string, error) {
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return "", fmt.Errorf("encode the credentials: %w", err)
	}
	return keyring.Seal(encoded, store.SecretConnectionContext(connectionID))
}

// OpenCredentials opens what SealCredentials sealed.
func OpenCredentials(keyring *crypto.Keyring, row store.SecretConnectionRow) (map[string]string, error) {
	plaintext, err := keyring.Open(row.SealedCredentials, store.SecretConnectionContext(row.ID))
	if err != nil {
		return nil, fmt.Errorf("open the credentials of the secret manager %s: %w", row.Name, err)
	}
	credentials := map[string]string{}
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return nil, fmt.Errorf("read the credentials of the secret manager %s: %w", row.Name, err)
	}
	return credentials, nil
}

// Test signs in to a manager with settings and credentials that may not be
// stored yet, and reads nothing. The answer is nil or an *Error.
func (r *Resolver) Test(ctx context.Context, kind string, settings, credentials map[string]string) error {
	p, err := newProvider(r.Client, kind, settings, credentials)
	if err != nil {
		return err
	}
	return p.test(ctx)
}

// TestConnection is Test for a stored connection.
func (r *Resolver) TestConnection(ctx context.Context, row store.SecretConnectionRow) error {
	credentials, err := OpenCredentials(r.keyring, row)
	if err != nil {
		return err
	}
	return r.Test(ctx, row.Kind, row.Settings, credentials)
}

// Problem is what a person is shown for a connection that failed a test.
func Problem(name, kind string, err error) *errdoc.Problem {
	var failed *Error
	if !errors.As(err, &failed) {
		return errdoc.From(err)
	}
	label := KindLabel(kind)
	switch failed.Reason {
	case Unreachable:
		return errdoc.SecretManagerUnreachable(name, label, failed.Detail)
	case Denied:
		return errdoc.SecretManagerDenied(name, label, failed.Detail)
	case NotFound:
		return errdoc.SecretNotFound(name, label, failed.Detail)
	}
	return errdoc.SecretManagerBadAnswer(name, label, failed.Detail)
}

// Wanted is one variable whose value is read from a secret manager.
type Wanted struct {
	Variable  string
	Reference store.SecretReference
}

// Resolve reads every wanted value from the team's connections, for
// variables of one project, and answers them by variable.
//
// A secret several variables read keys of is fetched once. The first that
// cannot be read fails the whole call with a problem naming the variable,
// the connection and why: an app is never handed a configuration with a hole
// where a secret should be.
//
// A connection that is not the team's is not found. The API never stores
// such a reference, and this is where that is made true rather than assumed.
// The same goes for a connection's limits: a reference outside them is
// refused here, before the manager is asked anything, whenever it was set —
// so narrowing a connection takes effect at the next deploy, sync or refresh
// of whatever was pointed at it before.
func (r *Resolver) Resolve(ctx context.Context, teamID, projectID string, wanted []Wanted) (map[string]string, error) {
	out := make(map[string]string, len(wanted))
	if len(wanted) == 0 {
		return out, nil
	}
	sorted := append([]Wanted(nil), wanted...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Variable < sorted[j].Variable })

	cache := cacheFrom(ctx)
	providers := map[string]provider{}
	rows := map[string]store.SecretConnectionRow{}
	for _, w := range sorted {
		ref := w.Reference
		row, ok := rows[ref.ConnectionID]
		if !ok {
			found, err := r.db.GetSecretConnection(ctx, ref.ConnectionID)
			if err != nil || found.TeamID != teamID {
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return nil, err
				}
				return nil, unresolved(w, store.SecretConnectionRow{}, failure(NotFound,
					"that connection no longer exists"))
			}
			rows[ref.ConnectionID], row = found, found
		}
		if err := r.CheckLimits(ctx, row, projectID, w); err != nil {
			return nil, err
		}
		p, ok := providers[row.ID]
		if !ok {
			credentials, err := OpenCredentials(r.keyring, row)
			if err != nil {
				return nil, err
			}
			if p, err = newProvider(r.Client, row.Kind, row.Settings, credentials); err != nil {
				return nil, err
			}
			providers[row.ID] = p
		}
		s, err := cache.fetch(ctx, row, ref.Path, p)
		if err != nil {
			return nil, unresolved(w, row, err)
		}
		value, err := pick(s, ref.Path, ref.Key)
		if err != nil {
			return nil, unresolved(w, row, err)
		}
		out[w.Variable] = value
	}
	return out, nil
}

// Check reads one reference of a project's variable now, so a typo in a path
// is found when it is written rather than at the next deploy. The value is
// dropped.
func (r *Resolver) Check(ctx context.Context, teamID, projectID, variable string, ref store.SecretReference) error {
	_, err := r.Resolve(ctx, teamID, projectID, []Wanted{{Variable: variable, Reference: ref}})
	return err
}

// CheckLimits answers the problem for a reference its connection's limits
// refuse, or nil. It asks the manager nothing: a path outside the limits is
// never read, not even to say that nothing is there.
func (r *Resolver) CheckLimits(ctx context.Context, row store.SecretConnectionRow, projectID string, w Wanted) error {
	limit := Refuses(row.SecretConnection, projectID, w.Reference.Path)
	if limit == "" {
		return nil
	}
	ref := w.Reference
	ref.Connection = row.Name
	var detail string
	if limit == LimitProject {
		// Other projects are counted, not named: a member limited to some
		// projects is not told what else the team has.
		others := "1 other project"
		if n := len(row.AllowedProjectIDs); n != 1 {
			others = fmt.Sprintf("%d other projects", n)
		}
		detail = fmt.Sprintf("is not for the project %s: an administrator limited it to %s", r.projectName(ctx, projectID), others)
	} else {
		detail = "reads only what is at or under " + DescribePaths(row.AllowedPaths)
	}
	return errdoc.ReferenceNotAllowed(w.Variable, ref.String(), row.Name, detail, string(limit))
}

// projectName is a project's name for a sentence, or its id when it cannot be
// read.
func (r *Resolver) projectName(ctx context.Context, projectID string) string {
	if projectID == "" {
		return "(none)"
	}
	if project, err := r.db.GetProject(ctx, projectID); err == nil {
		return project.Name
	}
	return projectID
}

// unresolved is the problem for a variable that could not be read.
func unresolved(w Wanted, row store.SecretConnectionRow, err error) *errdoc.Problem {
	ref := w.Reference
	name, kind := row.Name, KindLabel(row.Kind)
	if name == "" {
		name, kind = ref.ConnectionID, "unknown"
	}
	ref.Connection = name
	reason, detail := BadAnswer, err.Error()
	var failed *Error
	if errors.As(err, &failed) {
		reason, detail = failed.Reason, failed.Detail
	}
	problem := errdoc.ReferenceUnresolved(w.Variable, ref.String(), name, kind, detail, string(reason))
	if reason == NotFound {
		// Something to correct in the reference or in the manager, not a
		// manager that is down.
		return problem.WithStatus(http.StatusUnprocessableEntity)
	}
	return problem
}

// --- a cache for one refresh ---

type cacheKey struct{}

// Cache keeps what was fetched for as long as a context carrying it lives, so
// a refresh that visits twenty apps reading the same secret asks for it once.
type Cache struct {
	mu      sync.Mutex
	entries map[string]cached
}

type cached struct {
	secret secret
	err    error
}

// WithCache is a context whose resolutions share what they fetch.
func WithCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheKey{}, &Cache{entries: map[string]cached{}})
}

func cacheFrom(ctx context.Context) *Cache {
	if c, ok := ctx.Value(cacheKey{}).(*Cache); ok {
		return c
	}
	return &Cache{entries: map[string]cached{}}
}

func (c *Cache) fetch(ctx context.Context, row store.SecretConnectionRow, path string, p provider) (secret, error) {
	key := row.ID + "\x00" + path
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok {
		return entry.secret, entry.err
	}
	s, err := p.fetch(ctx, path)
	c.mu.Lock()
	c.entries[key] = cached{secret: s, err: err}
	c.mu.Unlock()
	return s, err
}
