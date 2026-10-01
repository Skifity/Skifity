package dbsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"skifity/internal/api"
	"skifity/internal/crypto"
	"skifity/internal/dbsvc/engine"
	"skifity/internal/errdoc"
	"skifity/internal/events"
	"skifity/internal/kube"
	"skifity/internal/runsafe"
	"skifity/internal/store"
	"skifity/internal/version"
)

// Changing a database's password.
//
// # The order, and why
//
// A password is in three places: inside the database, in the panel's sealed
// credentials, and in the database's Secret, which the linked apps' variables
// were made from. They cannot all change at the same instant, so the order is
// chosen so that stopping at any point leaves a password that works and a
// panel that knows it:
//
//  1. The new credentials are sealed and stored beside the old ones
//     (credentials_next_enc) before anything else happens. From here on a
//     panel that restarts has not lost the only copy of a password the
//     database may already have.
//  2. The database is given the new password, by a job signing in with the
//     old one. Where the engine allows two at once — MySQL's RETAIN CURRENT
//     PASSWORD, and the password list of a Redis or Valkey ACL user — the old
//     one keeps working beside it, so nothing that is connected or about to
//     connect notices. The job checks the new one works before it ends, and
//     puts the old one back if it does not. A job that fails here has changed
//     nothing that matters: the stored copy is dropped, and the old password
//     is the only one there is.
//  3. The Secret is rewritten, then the new credentials become the panel's own.
//     If either fails, the job is run again the other way round, and the old
//     password is back where it was. Only if that fails too is the database
//     left with a password the rest does not have yet — and then the new
//     credentials are kept, sealed, so the next change finishes this one.
//  4. Every linked app is given the new connection string and rolled out; a
//     rollout waits until the new instances are ready.
//  5. Only then is the old password taken away, where there were two. If an
//     app could not be rolled out, it is not taken away at all.
//
// Dragonfly and ClickHouse read their password only as they start, so for
// them step 2 is the Secret and a restart, and the check is the restart's
// readiness probe, which signs in with the password the Secret gives it. A
// restart that does not come up is undone the same way.
//
// # Never on a command line, never in a log
//
// Both passwords reach the job through a Secret of its own and the
// container's environment. The scripts put them into statements with the
// shell's own printf and here-documents, which are not processes, and hand
// each client its password in its own variable (PGPASSWORD, MYSQL_PWD,
// REDISCLI_AUTH) or, for mongosh, process.env: the same as the backup jobs.
// What a failed job printed is scrubbed of both before it is recorded.

// passwordPattern is what a password may be made of: every character is one
// that needs no escaping in a connection string, a SQL string, a shell word
// or a YAML one. The panel's own passwords are letters and digits.
var passwordPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{16,128}$`)

// ValidPassword reports whether a password somebody chose can be used.
func ValidPassword(password string) bool { return passwordPattern.MatchString(password) }

// GeneratePassword makes a password: 32 letters and digits, from 24 random
// bytes.
func GeneratePassword() (string, error) {
	password, err := crypto.RandomToken(24)
	if err != nil {
		return "", err
	}
	// Connection strings and shell commands carry it, so it is held to
	// characters that never need escaping.
	return strings.NewReplacer("-", "x", "_", "y").Replace(password), nil
}

// passwordMethod is how an engine takes a new password.
type passwordMethod int

const (
	// passwordNone: nothing to change (Memcached).
	passwordNone passwordMethod = iota
	// passwordLive: changed inside the running server, by a job.
	passwordLive
	// passwordRestart: read only as the server starts.
	passwordRestart
)

func passwordMethodFor(name string) passwordMethod {
	kind, ok := engine.Lookup(name)
	switch {
	case !ok || !kind.Password:
		return passwordNone
	case kind.PasswordRestarts:
		return passwordRestart
	default:
		return passwordLive
	}
}

// keepsBoth reports an engine that can take the new password while the old
// one still works, so the change and taking the old one away are two steps
// with the apps' rollout between them.
func keepsBoth(name string) bool {
	return name == EngineMySQL || name == EngineRedis || name == EngineValkey
}

// The phases a password job runs in.
const (
	// phaseChange gives the database the new password, signing in with the
	// old; where two can be kept, both then work.
	phaseChange = "change"
	// phaseRevert puts the old password back as the only one, after a later
	// step failed.
	phaseRevert = "revert"
	// phaseDiscard takes the old password away, where two were kept.
	phaseDiscard = "discard"
)

// PasswordJobSpec describes one job of a password change.
type PasswordJobSpec struct {
	Name      string
	Namespace string
	Engine    string
	Version   string
	// CredentialsSecret is the database's own, which the job reads the host,
	// port, user and database from.
	CredentialsSecret string
	// PasswordsSecret holds the password in use ("current") and the one being
	// given ("next"): the database's own Secret has only one of them at any
	// moment, and which one changes halfway through.
	PasswordsSecret string
	Phase           string
	OperationID     string
}

// BuildPasswordJob renders the job that runs one phase of a password change,
// with the database's own client tools.
func BuildPasswordJob(s PasswordJobSpec) (*batchv1.Job, error) {
	if s.Name == "" || s.Namespace == "" || s.CredentialsSecret == "" || s.PasswordsSecret == "" {
		return nil, fmt.Errorf("a password job needs a name, a namespace and both Secrets")
	}
	script, err := PasswordScript(s.Engine, s.Phase)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{
		"app.kubernetes.io/name":         s.Name,
		"app.kubernetes.io/managed-by":   version.Binary,
		"app.kubernetes.io/component":    "database-password",
		version.LabelKey("operation-id"): s.OperationID,
	}
	backoff := int32(0)
	deadline := int64(10 * 60)
	ttl := int32(3600)
	return &batchv1.Job{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptr(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr(true),
						RunAsUser:      ptr(ClientUser(s.Engine)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:    "password",
						Image:   ClientImage(s.Engine, s.Version),
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						Env: []corev1.EnvVar{
							secretEnv("DB_HOST", s.CredentialsSecret, "host"),
							secretEnv("DB_PORT", s.CredentialsSecret, "port"),
							secretEnv("DB_USER", s.CredentialsSecret, "username"),
							secretEnv("DB_NAME", s.CredentialsSecret, "database"),
							secretEnv("OLD_PASSWORD", s.PasswordsSecret, "current"),
							secretEnv("NEW_PASSWORD", s.PasswordsSecret, "next"),
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}, nil
}

// PasswordsSecret renders the Secret a password change's jobs read both
// passwords from. It lives as long as the change does.
func PasswordsSecret(name, namespace, current, next string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": version.Binary,
				"app.kubernetes.io/component":  "database-password",
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"current": current, "next": next},
	}
}

// passwordPrelude holds both passwords, and the user, to the characters the
// statements below can carry without quoting, as the panel did before it
// started. A value that is not is refused before anything is sent.
const passwordPrelude = `set -eu
for value in "$OLD_PASSWORD" "$NEW_PASSWORD"; do
  case "$value" in
    ''|*[!A-Za-z0-9._~-]*)
      echo "A password holds a character the change will not pass on; nothing was changed." >&2
      exit 1 ;;
  esac
done
case "$DB_USER" in
  ''|*[!A-Za-z0-9_]*)
    echo "The database's user is not a name this change can use; nothing was changed." >&2
    exit 1 ;;
esac
`

// PasswordScript is the shell a password job runs for an engine and a phase.
func PasswordScript(name, phase string) (string, error) {
	var functions, body string
	switch name {
	case EnginePostgres:
		functions = postgresPasswordFunctions
	case EngineMySQL:
		functions = mysqlPasswordFunctions("mysql", true)
	case EngineMariaDB:
		functions = mysqlPasswordFunctions("mariadb", false)
	case EngineMongoDB:
		// mongosh reads its script from a file the here-document writes; it
		// holds no password, which it reads from process.env.
		return passwordPrelude + mongoPasswordScript(phase), nil
	case EngineRedis:
		functions = redisPasswordFunctions("redis-cli")
	case EngineValkey:
		functions = redisPasswordFunctions("valkey-cli")
	default:
		return "", fmt.Errorf("%q does not change its password with a job", name)
	}

	both := keepsBoth(name)
	switch {
	case phase == phaseChange && both:
		body = `if works "$NEW_PASSWORD"; then
  echo "==> The database already takes the new password"
  exit 0
fi
add_password
if ! works "$NEW_PASSWORD"; then
  remove_new || true
  echo "The database took the new password and then refused it, so it was taken away again. The old password still works." >&2
  exit 1
fi
echo "==> The database takes the new password, and the old one until it is taken away"
`
	case phase == phaseChange:
		body = `if works "$NEW_PASSWORD"; then
  echo "==> The database already takes the new password"
  exit 0
fi
set_password "$OLD_PASSWORD" "$NEW_PASSWORD"
if ! works "$NEW_PASSWORD"; then
  set_password "$NEW_PASSWORD" "$OLD_PASSWORD" > /dev/null 2>&1 || true
  echo "The database took the new password and then refused it, so the old one was put back." >&2
  exit 1
fi
echo "==> The database takes the new password"
`
	case phase == phaseRevert && both:
		body = `remove_new
works "$OLD_PASSWORD" || { echo "The old password does not work after it was put back." >&2; exit 1; }
echo "==> The old password is the only one again"
`
	case phase == phaseRevert:
		body = `if works "$OLD_PASSWORD" && ! works "$NEW_PASSWORD"; then
  echo "==> The database takes the old password"
  exit 0
fi
set_password "$NEW_PASSWORD" "$OLD_PASSWORD"
works "$OLD_PASSWORD" || { echo "The old password does not work after it was put back." >&2; exit 1; }
echo "==> The old password is back"
`
	case phase == phaseDiscard && both:
		body = `discard_old
works "$NEW_PASSWORD" || { echo "The new password stopped working when the old one was taken away." >&2; exit 1; }
echo "==> The old password no longer works"
`
	default:
		return "", fmt.Errorf("%s has no %s phase", name, phase)
	}
	return passwordPrelude + functions + body, nil
}

// postgresPasswordFunctions signs in as the database's owner, which may
// change its own password. ALTER ROLE CURRENT_USER keeps the user's name out
// of the statement altogether.
const postgresPasswordFunctions = `export PGHOST="$DB_HOST" PGPORT="$DB_PORT" PGUSER="$DB_USER" PGDATABASE="$DB_NAME" PGCONNECT_TIMEOUT=10
works() {
  PGPASSWORD="$1" psql --no-psqlrc --quiet --tuples-only --command='SELECT 1' > /dev/null 2>&1
}
# $1 signs in, and $2 becomes the password.
set_password() {
  PGPASSWORD="$1" psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 <<EOF
ALTER ROLE CURRENT_USER PASSWORD '$2';
EOF
}
`

// mysqlPasswordFunctions signs in as root, whose password is the app user's
// (the image made both from the Secret), and changes both. root@localhost is
// left alone: nothing but the image's own first start signs in on the
// socket, and MariaDB's authenticates it by the socket rather than by a
// password.
//
// MySQL keeps the old password as a secondary one (RETAIN CURRENT PASSWORD,
// since 8.0.14) until DISCARD OLD PASSWORD; MariaDB cannot, and changes it
// outright.
func mysqlPasswordFunctions(client string, retain bool) string {
	out := `works() {
  MYSQL_PWD="$1" ` + client + ` --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" --connect-timeout=10 --execute='SELECT 1' > /dev/null 2>&1
}
as_root() {
  MYSQL_PWD="$1" ` + client + ` --host="$DB_HOST" --port="$DB_PORT" --user=root --connect-timeout=10
}
# $1 signs in, and $2 becomes the password.
set_password() {
  as_root "$1" <<EOF
ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY '$2', '$DB_USER'@'%' IDENTIFIED BY '$2';
EOF
}
`
	if retain {
		out += `add_password() {
  as_root "$OLD_PASSWORD" <<EOF
ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY '$NEW_PASSWORD' RETAIN CURRENT PASSWORD, '$DB_USER'@'%' IDENTIFIED BY '$NEW_PASSWORD' RETAIN CURRENT PASSWORD;
EOF
}
# The old password is the secondary one after add_password, so it still signs
# in; it becomes the primary again and the secondary goes.
remove_new() {
  as_root "$OLD_PASSWORD" <<EOF
ALTER USER IF EXISTS 'root'@'%' IDENTIFIED BY '$OLD_PASSWORD', '$DB_USER'@'%' IDENTIFIED BY '$OLD_PASSWORD';
ALTER USER IF EXISTS 'root'@'%' DISCARD OLD PASSWORD, '$DB_USER'@'%' DISCARD OLD PASSWORD;
EOF
}
discard_old() {
  as_root "$NEW_PASSWORD" <<EOF
ALTER USER IF EXISTS 'root'@'%' DISCARD OLD PASSWORD, '$DB_USER'@'%' DISCARD OLD PASSWORD;
EOF
}
`
	}
	return out
}

// redisPasswordFunctions changes the password list of the default user, the
// one requirepass sets: ">" adds a password and "<" removes one, so for a
// while both work. The rule reaches the server on the client's standard
// input, written by the shell's own printf. A restart reads the Secret's
// password into requirepass again, which is the new one by then.
func redisPasswordFunctions(client string) string {
	return `works() {
  [ "$(REDISCLI_AUTH="$1" ` + client + ` -h "$DB_HOST" -p "$DB_PORT" ping 2>/dev/null)" = PONG ]
}
# $1 signs in, and $2 is the rule for the default user.
acl() {
  [ "$(printf 'ACL SETUSER default %s\n' "$2" | REDISCLI_AUTH="$1" ` + client + ` -h "$DB_HOST" -p "$DB_PORT" 2>&1)" = OK ]
}
add_password() {
  acl "$OLD_PASSWORD" ">$NEW_PASSWORD" || { echo "The database refused the new password." >&2; exit 1; }
}
remove_new() {
  acl "$OLD_PASSWORD" "<$NEW_PASSWORD" || { echo "The database refused to take the new password away." >&2; exit 1; }
}
discard_old() {
  acl "$NEW_PASSWORD" "<$OLD_PASSWORD" || { echo "The database refused to take the old password away." >&2; exit 1; }
}
`
}

// mongoPasswordScript changes the root user's password, in the admin
// database where the image created it, with mongosh signed in as that user.
// The script is written by a quoted here-document, so nothing in it is
// expanded, and it holds no password: it reads both from process.env.
func mongoPasswordScript(phase string) string {
	var body string
	switch phase {
	case phaseRevert:
		body = `if (works(env.OLD_PASSWORD) && !works(env.NEW_PASSWORD)) {
  print('==> The database takes the old password');
  quit(0);
}
admin(env.NEW_PASSWORD).changeUserPassword(env.DB_USER, env.OLD_PASSWORD);
if (!works(env.OLD_PASSWORD)) {
  print('The old password does not work after it was put back.');
  quit(1);
}
print('==> The old password is back');
`
	default:
		body = `if (works(env.NEW_PASSWORD)) {
  print('==> The database already takes the new password');
  quit(0);
}
const signedIn = admin(env.OLD_PASSWORD);
signedIn.changeUserPassword(env.DB_USER, env.NEW_PASSWORD);
if (!works(env.NEW_PASSWORD)) {
  try { signedIn.changeUserPassword(env.DB_USER, env.OLD_PASSWORD); } catch (e) {}
  print('The database took the new password and then refused it, so the old one was put back.');
  quit(1);
}
print('==> The database takes the new password');
`
	}
	return `script="${TMPDIR:-/tmp}/password.js"
cat > "$script" <<'JS'
const env = process.env;
const address = 'mongodb://' + env.DB_HOST + ':' + env.DB_PORT +
  '/?authSource=admin&directConnection=true&serverSelectionTimeoutMS=10000';
function admin(password) {
  const db = new Mongo(address).getDB('admin');
  const answer = db.auth(env.DB_USER, password);
  if (answer && answer.ok !== 1) {
    throw new Error('the database refused to sign in');
  }
  return db;
}
function works(password) {
  try {
    admin(password).runCommand({ ping: 1 });
    return true;
  } catch (e) {
    return false;
  }
}
` + body + `JS
exec mongosh --nodb --quiet --file "$script"
`
}

// ChangePassword gives a database a new password: the one asked for, or a
// generated one when password is empty. It answers with the operation that
// does it; the steps are the order at the top of this file.
func (m *Manager) ChangePassword(ctx context.Context, databaseID, password, userID string) (store.Operation, error) {
	record, env, err := m.locate(ctx, databaseID)
	if err != nil {
		return store.Operation{}, err
	}
	kind, _ := engine.Lookup(record.Engine)
	method := passwordMethodFor(record.Engine)
	if method == passwordNone {
		return store.Operation{}, errdoc.DatabasePasswordNotOffered(record.Name, kind.Title)
	}
	if password != "" && !ValidPassword(password) {
		return store.Operation{}, errdoc.DatabasePasswordRejected()
	}
	if record.Status != "running" && record.Status != "degraded" {
		return store.Operation{}, errdoc.DatabaseNotRunning(record.Name, record.Status)
	}
	if running, err := m.db.OperationRunningFor(ctx, "database", record.ID); err != nil {
		return store.Operation{}, err
	} else if running {
		return store.Operation{}, errdoc.DatabaseBusy(record.Name)
	}
	current, err := m.Credentials(ctx, record.ID)
	if err != nil {
		return store.Operation{}, err
	}

	// A change a restart or a double failure stopped halfway is finished
	// before another is begun, with the password it was giving: the database
	// may have it already, and nothing else does.
	resuming := record.CredentialsNextEnc != ""
	var next api.DatabaseCredentials
	if resuming {
		if next, err = m.openNext(record); err != nil {
			return store.Operation{}, err
		}
		if password != "" && password != next.Password {
			return store.Operation{}, errdoc.DatabasePasswordChangeInterrupted(record.Name)
		}
	} else {
		if password == "" {
			if password, err = GeneratePassword(); err != nil {
				return store.Operation{}, err
			}
		}
		if password == current.Password {
			return store.Operation{}, errdoc.BadRequest("That is the password the database has now.")
		}
		spec := m.specFor(record, env)
		spec.Username, spec.DatabaseName, spec.Password = current.Username, current.Database, password
		next = credentialsFor(spec)
		sealed, err := m.sealCredentials(next, nextCredentialsContext(record.ID))
		if err != nil {
			return store.Operation{}, err
		}
		// Step 1: the new password is kept before anything is asked to take it.
		if err := m.db.BeginDatabaseCredentials(ctx, record.ID, sealed); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return store.Operation{}, errdoc.DatabasePasswordChangeRunning(record.Name)
			}
			return store.Operation{}, err
		}
	}

	teamID, err := m.db.TeamIDForDatabase(ctx, record.ID)
	if err != nil {
		return store.Operation{}, err
	}
	op := store.Operation{
		TeamID: teamID, Kind: "database.password", TargetType: "database", TargetID: record.ID,
		CreatedBy: userID,
	}
	if err := m.db.CreateOperation(ctx, &op, []string{"prepare", "change", "apply", "sync", "finish"}); err != nil {
		return store.Operation{}, err
	}
	change := passwordChange{
		op: op, record: record, env: env, method: method,
		current: current, next: next,
	}
	background := context.WithoutCancel(ctx)
	runsafe.Go(m.log, "changing the password of "+record.ID, func() {
		m.runPasswordChange(background, change)
	})
	return op, nil
}

// passwordChange is one change under way.
type passwordChange struct {
	op      store.Operation
	record  store.Database
	env     store.Environment
	method  passwordMethod
	current api.DatabaseCredentials
	next    api.DatabaseCredentials
}

func (c passwordChange) secretName() string {
	return JobName(c.record.Slug+"-passwords", c.op.ID)
}

func (c passwordChange) jobName(phase string) string {
	return JobName(c.record.Slug+"-password-"+phase, c.op.ID)
}

// scrub takes both passwords out of something a job printed, before it is
// recorded anywhere. The scripts never print one; a client's error message
// is not the scripts' to promise about.
func (c passwordChange) scrub(text string) string {
	for _, secret := range []string{c.current.Password, c.next.Password} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[password]")
		}
	}
	return text
}

func (m *Manager) runPasswordChange(ctx context.Context, c passwordChange) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	op, record := c.op, c.record

	fail := func(step string, problem *errdoc.Problem) {
		m.log.Error("the password change failed", "operation", op.ID, "database", record.ID,
			"step", step, "code", problem.Code)
		_ = m.db.SetStepStatus(ctx, op.ID, step, store.StepFailed,
			store.StepNote{Message: problem.Title, Key: "problem:" + problem.Code, Args: problem.Args.Title},
			problem.Text())
		_ = m.db.SetOperationStatus(ctx, op.ID, store.OpFailed, problem.Code, problem.Error())
		m.hub.Publish(events.OperationTopic(op.ID), "failed", problem)
		m.publish(ctx, record.ID)
	}
	// Given up on: the database has the old password, and only the old one.
	abandon := func(step string, problem *errdoc.Problem) {
		_ = m.db.AbandonDatabaseCredentials(ctx, record.ID)
		fail(step, problem)
	}
	defer runsafe.Recover(m.log, "password change "+op.ID, func(err error) {
		// Whatever state it was in, the new credentials stay stored: the next
		// change finishes this one.
		fail("change", errdoc.DatabasePasswordStranded(err.Error()))
	})

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpRunning, "", "")
	applier := m.cluster.Client().Applier()
	namespace := c.env.Namespace

	// --- prepare ---
	_ = m.db.SetStepStatus(ctx, op.ID, "prepare", store.StepRunning, store.StepNote{}, "")
	if c.method == passwordLive {
		secret := c.secretName()
		defer func() {
			if err := applier.Delete(ctx, "v1", "Secret", namespace, secret); err != nil {
				m.log.Warn("could not remove a password change's Secret", "operation", op.ID, "error", err)
			}
		}()
		if err := applier.Apply(ctx, PasswordsSecret(secret, namespace, c.current.Password, c.next.Password)); err != nil {
			abandon("prepare", errdoc.DatabasePasswordFailed(c.scrub(err.Error())))
			return
		}
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "prepare", store.StepSucceeded, store.StepNote{}, "")

	// --- change ---
	_ = m.db.SetStepStatus(ctx, op.ID, "change", store.StepRunning, store.StepNote{}, "")
	if c.method == passwordLive {
		if err := m.runPasswordJob(ctx, c, phaseChange); err != nil {
			// The job signs in, changes and checks, and puts the old
			// password back itself when the check fails.
			abandon("change", errdoc.DatabasePasswordFailed(c.scrub(err.Error())))
			return
		}
	} else if err := m.restartWith(ctx, c, c.next); err != nil {
		// The Secret is put back and the database restarted onto it.
		if undo := m.restartWith(ctx, c, c.current); undo != nil {
			fail("change", errdoc.DatabasePasswordStranded(c.scrub(err.Error())))
			return
		}
		abandon("change", errdoc.DatabasePasswordFailed(c.scrub(err.Error())))
		return
	}
	changed, changedArgs := errdoc.Sprintf("%s takes the new password", record.Name)
	_ = m.db.SetStepStatus(ctx, op.ID, "change", store.StepSucceeded,
		store.StepNote{Message: changed, Key: "passwordTaken", Args: changedArgs}, "")

	// --- apply: the Secret, then the panel's own copy ---
	_ = m.db.SetStepStatus(ctx, op.ID, "apply", store.StepRunning, store.StepNote{}, "")
	nextSpec := m.specFor(record, c.env)
	nextSpec.Username, nextSpec.DatabaseName, nextSpec.Password = c.next.Username, c.next.Database, c.next.Password
	err := applier.Apply(ctx, BuildSecret(nextSpec))
	if err == nil {
		var sealed string
		if sealed, err = m.sealCredentials(c.next, credentialsContext(record.ID)); err == nil {
			err = m.db.CommitDatabaseCredentials(ctx, record.ID, sealed)
		}
	}
	if err != nil {
		reason := c.scrub(err.Error())
		if undo := m.undoChange(ctx, c); undo != nil {
			fail("apply", errdoc.DatabasePasswordStranded(reason))
			return
		}
		abandon("apply", errdoc.DatabasePasswordFailed(reason))
		return
	}
	_ = m.db.SetStepStatus(ctx, op.ID, "apply", store.StepSucceeded, store.StepNote{}, "")
	m.publish(ctx, record.ID)

	// --- sync: every linked app gets the new connection string ---
	_ = m.db.SetStepStatus(ctx, op.ID, "sync", store.StepRunning, store.StepNote{}, "")
	synced, missed := m.resyncLinkedApps(ctx, record.ID)
	if missed > 0 {
		note, args := errdoc.Sprintf("%d app(s) given the new connection string; %d could not be rolled out", synced, missed)
		_ = m.db.SetStepStatus(ctx, op.ID, "sync", store.StepFailed,
			store.StepNote{Message: note, Key: "appsResyncedWithFailures", Args: args}, "")
	} else {
		note, args := errdoc.Sprintf("%d app(s) given the new connection string", synced)
		_ = m.db.SetStepStatus(ctx, op.ID, "sync", store.StepSucceeded,
			store.StepNote{Message: note, Key: "appsResynced", Args: args}, "")
	}

	// --- finish: the old password goes, where two were kept ---
	_ = m.db.SetStepStatus(ctx, op.ID, "finish", store.StepRunning, store.StepNote{}, "")
	switch {
	case c.method != passwordLive || !keepsBoth(record.Engine):
		_ = m.db.SetStepStatus(ctx, op.ID, "finish", store.StepSucceeded,
			store.StepNote{Message: "The old password no longer works", Key: "oldPasswordGone"}, "")
	case missed > 0:
		// An app that still has the old connection string still connects
		// with it. Taking it away now would stop that app, and the whole
		// point of the order is that nothing is stopped.
		_ = m.db.SetStepStatus(ctx, op.ID, "finish", store.StepSkipped,
			store.StepNote{Message: "The old password still works, for the apps that were not rolled out", Key: "oldPasswordKept"}, "")
	default:
		if err := m.runPasswordJob(ctx, c, phaseDiscard); err != nil {
			m.log.Warn("could not take the old database password away", "operation", op.ID, "error", c.scrub(err.Error()))
			_ = m.db.SetStepStatus(ctx, op.ID, "finish", store.StepSkipped,
				store.StepNote{Message: "The old password still works; change the password again to take it away", Key: "oldPasswordNotDiscarded"},
				c.scrub(err.Error()))
		} else {
			_ = m.db.SetStepStatus(ctx, op.ID, "finish", store.StepSucceeded,
				store.StepNote{Message: "The old password no longer works", Key: "oldPasswordGone"}, "")
		}
	}

	_ = m.db.SetOperationStatus(ctx, op.ID, store.OpSucceeded, "", "")
	m.hub.Publish(events.OperationTopic(op.ID), "operation", op)
	m.publish(ctx, record.ID)
	m.log.Info("database password changed", "database", record.ID, "apps", synced)
}

// undoChange puts the old password back after the database took the new one
// and a later step failed. Where two were kept only the new one is taken
// away; the old never stopped working.
func (m *Manager) undoChange(ctx context.Context, c passwordChange) error {
	oldSpec := m.specFor(c.record, c.env)
	oldSpec.Username, oldSpec.DatabaseName, oldSpec.Password = c.current.Username, c.current.Database, c.current.Password
	// The Secret may or may not have been rewritten; either way it says the
	// old password again.
	if err := m.cluster.Client().Applier().Apply(ctx, BuildSecret(oldSpec)); err != nil {
		return err
	}
	if c.method == passwordRestart {
		return m.restartWith(ctx, c, c.current)
	}
	return m.runPasswordJob(ctx, c, phaseRevert)
}

// restartWith gives a database that reads its password only as it starts the
// credentials in its Secret, and restarts it onto them. Its readiness probe
// signs in with that password (see pingProbe and BuildClickHouse's startup
// probe), so ready is the proof the password works.
func (m *Manager) restartWith(ctx context.Context, c passwordChange, credentials api.DatabaseCredentials) error {
	spec := m.specFor(c.record, c.env)
	spec.Username, spec.DatabaseName, spec.Password = credentials.Username, credentials.Database, credentials.Password
	if err := m.cluster.Client().Applier().Apply(ctx, BuildSecret(spec)); err != nil {
		return err
	}
	pods := m.cluster.Client().Clientset().CoreV1().Pods(c.env.Namespace)
	list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + c.record.Slug})
	if err != nil {
		return err
	}
	for _, pod := range list.Items {
		if err := pods.Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil && !kube.IsNotFound(err) {
			return err
		}
	}
	// A moment for the StatefulSet to notice its instance went, so the
	// readiness read next is the new instance's and not the old one's.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(m.every()):
	}
	return m.waitReady(ctx, c.record, Spec{Name: c.record.Slug, Namespace: c.env.Namespace, Engine: c.record.Engine})
}

// runPasswordJob runs one phase of a password change and waits for it.
func (m *Manager) runPasswordJob(ctx context.Context, c passwordChange, phase string) error {
	job, err := BuildPasswordJob(PasswordJobSpec{
		Name:              c.jobName(phase),
		Namespace:         c.env.Namespace,
		Engine:            c.record.Engine,
		Version:           c.record.EngineVersion,
		CredentialsSecret: kube.ResourceName(c.record.Slug, "credentials"),
		PasswordsSecret:   c.secretName(),
		Phase:             phase,
		OperationID:       c.op.ID,
	})
	if err != nil {
		return err
	}
	applier := m.cluster.Client().Applier()
	_ = applier.Delete(ctx, "batch/v1", "Job", c.env.Namespace, job.Name)
	if err := applier.Apply(ctx, job); err != nil {
		return err
	}
	return m.waitForJob(ctx, c.env.Namespace, job.Name, 12*time.Minute)
}

// waitForJob waits for a job and, when it failed, says what it printed last.
func (m *Manager) waitForJob(ctx context.Context, namespace, name string, timeout time.Duration) error {
	jobs := m.cluster.Client().Clientset().BatchV1().Jobs(namespace)
	deadline := time.Now().Add(timeout)
	for {
		job, err := jobs.Get(ctx, name, metav1.GetOptions{})
		if err != nil && !kube.IsNotFound(err) {
			return fmt.Errorf("read the job's status: %w", err)
		}
		if err == nil {
			if job.Status.Succeeded > 0 {
				return nil
			}
			if job.Status.Failed > 0 {
				return errors.New(m.jobOutput(ctx, namespace, name))
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the job did not finish within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.every()):
		}
	}
}

// jobOutput is the last lines a job's pod printed, which is where its own
// reason for failing is.
func (m *Manager) jobOutput(ctx context.Context, namespace, job string) string {
	pods, err := m.cluster.Client().Clientset().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + job,
	})
	if err != nil || len(pods.Items) == 0 {
		return "the job failed and its output could not be read"
	}
	tail := int64(20)
	stream, err := m.cluster.Client().StreamClientset().CoreV1().Pods(namespace).
		GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{TailLines: &tail}).Stream(ctx)
	if err != nil {
		return "the job failed and its output could not be read"
	}
	defer stream.Close()
	output, err := io.ReadAll(io.LimitReader(stream, 8<<10))
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "the job failed without printing anything"
	}
	return strings.TrimSpace(string(output))
}

// resyncLinkedApps gives every app linked to a database its connection
// string again, from the credentials the panel now holds, and rolls each out.
// It answers how many were and how many could not be.
func (m *Manager) resyncLinkedApps(ctx context.Context, databaseID string) (synced, missed int) {
	links, err := m.db.ListLinksForDatabase(ctx, databaseID)
	if err != nil {
		m.log.Warn("could not list the apps linked to a database", "database", databaseID, "error", err)
		return 0, 1
	}
	for _, link := range links {
		if err := m.relink(ctx, databaseID, link.AppID, link.VarName); err != nil {
			m.log.Warn("could not give an app the database's new connection string",
				"database", databaseID, "app", link.AppID, "error", err)
			missed++
			continue
		}
		synced++
	}
	return synced, missed
}

// relink is Link for an app already linked under a name: the variable is
// sealed again from the current credentials and the app rolled out. Unlike
// Link, a rollout that fails is a failure here, because a password change
// takes the old password away only from apps that were rolled out.
func (m *Manager) relink(ctx context.Context, databaseID, appID, varName string) error {
	credentials, err := m.Credentials(ctx, databaseID)
	if err != nil {
		return err
	}
	sealed, err := m.keyring.Seal([]byte(credentials.URL), "variable:"+appID+":"+varName)
	if err != nil {
		return err
	}
	variable := store.Variable{AppID: appID, Key: varName, IsSecret: true}
	if err := m.db.SetVariable(ctx, &variable, sealed); err != nil {
		return err
	}
	if m.deployer != nil {
		return m.deployer.Sync(ctx, appID)
	}
	return nil
}

// credentialsFor is what the panel stores and a linked app is given for a
// database rendered from spec.
func credentialsFor(spec Spec) api.DatabaseCredentials {
	return api.DatabaseCredentials{
		Engine: spec.Engine, Host: spec.ServiceHost(), Port: spec.Port(),
		Database: spec.DatabaseName, Username: spec.Username, Password: spec.Password,
		URL: spec.ConnectionURL(), NativeURL: spec.NativeURL(),
	}
}

func (m *Manager) sealCredentials(credentials api.DatabaseCredentials, context string) (string, error) {
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return "", err
	}
	return m.keyring.Seal(encoded, context)
}

// openNext reads the credentials a password change was giving a database.
func (m *Manager) openNext(record store.Database) (api.DatabaseCredentials, error) {
	plaintext, err := m.keyring.Open(record.CredentialsNextEnc, nextCredentialsContext(record.ID))
	if err != nil {
		return api.DatabaseCredentials{}, fmt.Errorf("read the new database credentials: %w", err)
	}
	var credentials api.DatabaseCredentials
	if err := json.Unmarshal(plaintext, &credentials); err != nil {
		return api.DatabaseCredentials{}, fmt.Errorf("read the new database credentials: %w", err)
	}
	credentials.Engine = record.Engine
	return credentials, nil
}

func nextCredentialsContext(databaseID string) string {
	return "database_credentials_next:" + databaseID
}

// JobName builds a unique, valid name for a job that belongs to an
// operation, from a prefix and the operation's id.
func JobName(prefix, operationID string) string {
	id := operationID
	if idx := strings.IndexByte(id, '_'); idx >= 0 {
		id = id[idx+1:]
	}
	if len(id) > 10 {
		id = id[:10]
	}
	return kube.ResourceName(prefix, id)
}
