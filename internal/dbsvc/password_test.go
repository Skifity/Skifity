package dbsvc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"skifity/internal/store"
)

// Changing a database's password: never on a command line, in an order that
// leaves a working password whatever stops it, and the apps given the new
// connection string.

// liveEngines change their password with a job.
var liveEngines = []string{EnginePostgres, EngineMySQL, EngineMariaDB, EngineMongoDB, EngineRedis, EngineValkey}

func phasesOf(name string) []string {
	if keepsBoth(name) {
		return []string{phaseChange, phaseRevert, phaseDiscard}
	}
	return []string{phaseChange, phaseRevert}
}

// No job carries a password: both come from a Secret of the change's own, as
// the database's details come from its own.
func TestNoPasswordJobCarriesAPassword(t *testing.T) {
	for _, name := range liveEngines {
		for _, phase := range phasesOf(name) {
			job, err := BuildPasswordJob(PasswordJobSpec{
				Name: "orders-password-" + phase + "-abc", Namespace: "acme-shop-production", Engine: name,
				CredentialsSecret: "orders-credentials", PasswordsSecret: "orders-passwords-abc",
				Phase: phase, OperationID: "op_1",
			})
			if err != nil {
				t.Fatalf("%s %s: %v", name, phase, err)
			}
			raw, _ := json.Marshal(job)
			if strings.Contains(string(raw), plantedPassword) || strings.Contains(string(raw), newPassword) {
				t.Errorf("%s %s: a password is in the job", name, phase)
			}
			pod := job.Spec.Template.Spec
			if pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot ||
				pod.SecurityContext.SeccompProfile == nil || pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
				t.Errorf("%s %s: the pod would be refused by the restricted profile, or carries a token", name, phase)
			}
			for _, env := range pod.Containers[0].Env {
				if env.Value != "" || env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
					t.Errorf("%s %s: %s is not from a Secret", name, phase, env.Name)
				}
			}
			if image := pod.Containers[0].Image; image != ClientImage(name, "") {
				t.Errorf("%s %s runs %s", name, phase, image)
			}
		}
	}
	if _, err := PasswordScript(EngineMariaDB, phaseDiscard); err == nil {
		t.Error("MariaDB, which keeps one password, was given a phase that takes the old one away")
	}
	for _, name := range []string{EngineDragonfly, EngineClickHouse, EngineMemcached} {
		if _, err := PasswordScript(name, phaseChange); err == nil {
			t.Errorf("%s was given a job, and it does not change its password while it runs", name)
		}
	}
	secret := PasswordsSecret("orders-passwords-abc", "ns", plantedPassword, newPassword)
	if secret.StringData["current"] != plantedPassword || secret.StringData["next"] != newPassword {
		t.Errorf("the passwords' Secret holds %v", secret.StringData)
	}
}

// stubs are stand-ins for each engine's client that keep a list of the
// passwords the database accepts, change it as the client's statements say,
// and write down every command line they were started with.
const stubPrelude = `#!/bin/sh
state="$STATE"
printf 'ARGS %s\n' "$*" >> "$state/argv"
accepts() { grep -qxF "$1" "$state/accepted"; }
`

var passwordStubs = map[string]string{
	"psql": stubPrelude + `accepts "$PGPASSWORD" || exit 2
input="$(cat)"
printf '%s\n' "$input" >> "$state/stdin"
new="$(printf '%s' "$input" | sed -n "s/.*PASSWORD '\([^']*\)'.*/\1/p")"
[ -n "$new" ] && printf '%s\n' "$new" > "$state/accepted"
exit 0
`,
	"mysql": stubPrelude + `accepts "$MYSQL_PWD" || exit 1
input="$(cat)"
printf '%s\n' "$input" >> "$state/stdin"
new="$(printf '%s' "$input" | sed -n "s/.*IDENTIFIED BY '\([^']*\)'.*/\1/p" | head -n 1)"
if [ -n "$new" ]; then
  case "$input" in
    *RETAIN*) { printf '%s\n' "$new"; cat "$state/accepted"; } > "$state/next"; mv "$state/next" "$state/accepted" ;;
    *) { printf '%s\n' "$new"; tail -n +2 "$state/accepted"; } > "$state/next"; mv "$state/next" "$state/accepted" ;;
  esac
fi
case "$input" in
  *"DISCARD OLD PASSWORD"*) head -n 1 "$state/accepted" > "$state/next"; mv "$state/next" "$state/accepted" ;;
esac
exit 0
`,
	"redis-cli": stubPrelude + `if ! accepts "$REDISCLI_AUTH"; then echo NOAUTH; exit 0; fi
for last in "$@"; do :; done
if [ "$last" = ping ]; then echo PONG; exit 0; fi
input="$(cat)"
printf '%s\n' "$input" >> "$state/stdin"
rule="${input#ACL SETUSER default }"
case "$rule" in
  ">"*) printf '%s\n' "${rule#>}" >> "$state/accepted" ;;
  "<"*) grep -vxF "${rule#<}" "$state/accepted" > "$state/next"; mv "$state/next" "$state/accepted" ;;
esac
echo OK
`,
}

func runPasswordScript(t *testing.T, name, phase, state string, accepted ...string) {
	t.Helper()
	script, err := PasswordScript(name, phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "accepted"), []byte(strings.Join(accepted, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(state, "bin")
	_ = os.MkdirAll(bin, 0o755)
	for client, body := range map[string]string{
		"psql": passwordStubs["psql"], "mysql": passwordStubs["mysql"], "mariadb": passwordStubs["mysql"],
		"redis-cli": passwordStubs["redis-cli"], "valkey-cli": passwordStubs["redis-cli"],
	} {
		if err := os.WriteFile(filepath.Join(bin, client), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "STATE="+state, "TMPDIR="+state,
		"OLD_PASSWORD="+plantedPassword, "NEW_PASSWORD="+newPassword,
		"DB_HOST=orders.acme", "DB_PORT=5432", "DB_USER=app", "DB_NAME=app")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, phase, err, output)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	return strings.Fields(string(raw))
}

// Each script, run with its client replaced by a stand-in: the passwords
// never reach a command line, the new one works afterwards, and the old one
// works beside it only where the engine can keep two — and only until it is
// taken away.
func TestThePasswordScriptsChangeItOffTheCommandLine(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	for _, name := range []string{EnginePostgres, EngineMySQL, EngineMariaDB, EngineRedis, EngineValkey} {
		state := t.TempDir()
		runPasswordScript(t, name, phaseChange, state, plantedPassword)
		accepted := readLines(t, filepath.Join(state, "accepted"))
		if !slices.Contains(accepted, newPassword) {
			t.Errorf("%s: after the change the database accepts %v", name, accepted)
		}
		if keepsBoth(name) != slices.Contains(accepted, plantedPassword) {
			t.Errorf("%s: after the change the database accepts %v; keeps both: %v", name, accepted, keepsBoth(name))
		}
		argv, _ := os.ReadFile(filepath.Join(state, "argv"))
		if strings.Contains(string(argv), plantedPassword) || strings.Contains(string(argv), newPassword) {
			t.Errorf("%s: a password reached a command line:\n%s", name, argv)
		}
		stdin, _ := os.ReadFile(filepath.Join(state, "stdin"))
		if !strings.Contains(string(stdin), newPassword) {
			t.Errorf("%s: the new password did not reach the client's input:\n%s", name, stdin)
		}

		// Run again, it sees the change is made and changes nothing: a
		// change interrupted after the database took it is finished.
		runPasswordScript(t, name, phaseChange, state, newPassword)
		if got := readLines(t, filepath.Join(state, "accepted")); !slices.Equal(got, []string{newPassword}) {
			t.Errorf("%s: a change run twice leaves %v", name, got)
		}

		if keepsBoth(name) {
			state := t.TempDir()
			runPasswordScript(t, name, phaseDiscard, state, newPassword, plantedPassword)
			if got := readLines(t, filepath.Join(state, "accepted")); !slices.Equal(got, []string{newPassword}) {
				t.Errorf("%s: after the old one is taken away the database accepts %v", name, got)
			}
			state = t.TempDir()
			runPasswordScript(t, name, phaseRevert, state, newPassword, plantedPassword)
			if got := readLines(t, filepath.Join(state, "accepted")); !slices.Equal(got, []string{plantedPassword}) {
				t.Errorf("%s: put back, the database accepts %v", name, got)
			}
		} else {
			state := t.TempDir()
			runPasswordScript(t, name, phaseRevert, state, newPassword)
			if got := readLines(t, filepath.Join(state, "accepted")); !slices.Equal(got, []string{plantedPassword}) {
				t.Errorf("%s: put back, the database accepts %v", name, got)
			}
		}
	}
}

// mongosh is handed a script that holds no password and reads both from its
// environment, by a here-document that expands nothing.
func TestTheMongoDBPasswordScriptReadsThePasswordsFromItsEnvironment(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell here")
	}
	for _, phase := range phasesOf(EngineMongoDB) {
		script, err := PasswordScript(EngineMongoDB, phase)
		if err != nil {
			t.Fatal(err)
		}
		state := t.TempDir()
		stub := "#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\" > \"$STATE/argv\"\nfor last in \"$@\"; do :; done\ncp \"$last\" \"$STATE/script.js\"\n"
		if err := os.WriteFile(filepath.Join(state, "mongosh"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+state+":"+os.Getenv("PATH"), "STATE="+state, "TMPDIR="+state,
			"OLD_PASSWORD="+plantedPassword, "NEW_PASSWORD="+newPassword, "DB_HOST=h", "DB_PORT=27017", "DB_USER=app", "DB_NAME=app")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", phase, err, output)
		}
		argv, _ := os.ReadFile(filepath.Join(state, "argv"))
		js, _ := os.ReadFile(filepath.Join(state, "script.js"))
		for _, secret := range []string{plantedPassword, newPassword} {
			if strings.Contains(string(argv), secret) || strings.Contains(string(js), secret) {
				t.Errorf("%s: a password is in mongosh's command line or its script", phase)
			}
		}
		if !strings.Contains(string(js), "env.NEW_PASSWORD") || !strings.Contains(string(js), "changeUserPassword") {
			t.Errorf("%s: the script does not change the password from the environment:\n%s", phase, js)
		}
	}
}

func (h *lifeHarness) waitForOperation(id string) store.Operation {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		op, err := h.db.GetOperation(h.t.Context(), id)
		if err != nil {
			h.t.Fatal(err)
		}
		if op.Status == store.OpSucceeded || op.Status == store.OpFailed {
			return op
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the operation is still %s", op.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *lifeHarness) passwordNow() string {
	h.t.Helper()
	credentials, err := h.m.Credentials(h.t.Context(), h.record.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	return credentials.Password
}

// secretsWith are the applied database Secrets that carry a password.
func (h *lifeHarness) secretsWith(password string) int {
	n := 0
	for _, secret := range h.appliedOf("Secret") {
		if secret.GetName() != "orders-credentials" {
			continue
		}
		if value, _, _ := unstructured.NestedString(secret.Object, "stringData", "password"); value == password {
			n++
		}
	}
	return n
}

func (h *lifeHarness) jobsApplied() []string {
	var names []string
	for _, job := range h.appliedOf("Job") {
		names = append(names, job.GetName())
	}
	return names
}

func stepOf(op store.Operation, key string) store.OperationStep {
	for _, step := range op.Steps {
		if step.Key == key {
			return step
		}
	}
	return store.OperationStep{}
}

// The whole change, where two passwords can be kept: the database takes the
// new one, then the Secret and the panel's copy, then the app gets its new
// connection string, and only then does the old password go.
func TestAPasswordChangeReachesTheDatabaseTheSecretAndTheApps(t *testing.T) {
	h := newLifeHarness(t, EngineMySQL, nil)
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, newPassword, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitForOperation(op.ID)
	if done.Status != store.OpSucceeded {
		t.Fatalf("the change ended %s: %s", done.Status, done.ErrorMsg)
	}
	if h.passwordNow() != newPassword {
		t.Error("the panel's copy is not the new password")
	}
	if record := h.reload(); record.CredentialsNextEnc != "" {
		t.Error("the waiting copy was left behind")
	}
	if h.secretsWith(newPassword) != 1 {
		t.Error("the database's Secret was not given the new password")
	}
	if synced := h.deployer.all(); !slices.Equal(synced, []string{h.app.ID}) {
		t.Errorf("the apps rolled out: %v", synced)
	}
	variables, _ := h.db.ListVariables(t.Context(), h.app.ID)
	if len(variables) != 1 {
		t.Fatalf("the app's variables: %+v", variables)
	}
	url, err := h.keyring.Open(variables[0].Sealed, "variable:"+h.app.ID+":"+variables[0].Key)
	if err != nil || !strings.Contains(string(url), newPassword) {
		t.Errorf("the app's connection string is %q (%v)", url, err)
	}
	jobs := strings.Join(h.jobsApplied(), " ")
	if !strings.Contains(jobs, "password-change") || !strings.Contains(jobs, "password-discard") {
		t.Errorf("the jobs run were %s", jobs)
	}
	if strings.Index(jobs, "password-change") > strings.Index(jobs, "password-discard") {
		t.Errorf("the old password was taken away before the change: %s", jobs)
	}
	if step := stepOf(done, "finish"); step.MessageKey != "oldPasswordGone" {
		t.Errorf("the last step says %+v", step)
	}
}

// A job that cannot change it changes nothing else: the panel keeps the old
// password, forgets the new one, and no app is touched.
func TestAPasswordTheDatabaseRefusedChangesNothingElse(t *testing.T) {
	h := newLifeHarness(t, EnginePostgres, nil)
	h.failJob = func(name string) bool { return strings.Contains(name, "password-change") }
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, "", "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitForOperation(op.ID)
	if done.Status != store.OpFailed || done.ErrorCode != "database.password_failed" {
		t.Fatalf("the change ended %s, %s", done.Status, done.ErrorCode)
	}
	if h.passwordNow() != plantedPassword || h.reload().CredentialsNextEnc != "" {
		t.Error("the old password is no longer the panel's, or the new one was kept")
	}
	if n := len(h.appliedOf("Secret")); n != 1 {
		t.Errorf("%d Secrets were applied; only the change's own should have been", n)
	}
	if synced := h.deployer.all(); len(synced) != 0 {
		t.Errorf("apps were rolled out: %v", synced)
	}
}

// The database took the new password and the Secret could not be written:
// the old password is put back in the database, and is the panel's still.
func TestAPasswordThatCouldNotBeSavedIsTakenBackOut(t *testing.T) {
	h := newLifeHarness(t, EngineMariaDB, nil)
	h.refuseApply = func(obj *unstructured.Unstructured) bool {
		password, _, _ := unstructured.NestedString(obj.Object, "stringData", "password")
		return obj.GetName() == "orders-credentials" && password == newPassword
	}
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, newPassword, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitForOperation(op.ID)
	if done.Status != store.OpFailed || done.ErrorCode != "database.password_failed" {
		t.Fatalf("the change ended %s, %s", done.Status, done.ErrorCode)
	}
	jobs := strings.Join(h.jobsApplied(), " ")
	if !strings.Contains(jobs, "password-change") || !strings.Contains(jobs, "password-revert") {
		t.Errorf("the old password was not put back: the jobs were %s", jobs)
	}
	if h.passwordNow() != plantedPassword || h.reload().CredentialsNextEnc != "" {
		t.Error("the panel does not hold the old password alone")
	}
	if h.secretsWith(plantedPassword) != 1 {
		t.Error("the Secret was not written back with the old password")
	}
	if synced := h.deployer.all(); len(synced) != 0 {
		t.Errorf("apps were rolled out: %v", synced)
	}
}

// Putting it back failing too is the one case the database may hold a
// password the rest does not: the new one is kept, sealed, and the next
// change, asked for no password, finishes this one with it.
func TestAPasswordThatCouldNeitherBeSavedNorPutBackIsKept(t *testing.T) {
	h := newLifeHarness(t, EnginePostgres, nil)
	h.refuseApply = func(obj *unstructured.Unstructured) bool { return obj.GetName() == "orders-credentials" }
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, newPassword, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if done := h.waitForOperation(op.ID); done.ErrorCode != "database.password_stranded" {
		t.Fatalf("the change ended %s", done.ErrorCode)
	}
	if h.reload().CredentialsNextEnc == "" {
		t.Fatal("the new password was not kept")
	}
	if _, err := h.m.ChangePassword(t.Context(), h.record.ID, "another-password-entirely", "usr_1"); codeOf(err) != "database.password_change_interrupted" {
		t.Fatalf("a different password over an unfinished change: %v", err)
	}
	h.refuseApply = nil
	op, err = h.m.ChangePassword(t.Context(), h.record.ID, "", "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	if done := h.waitForOperation(op.ID); done.Status != store.OpSucceeded {
		t.Fatalf("finishing the change ended %s: %s", done.Status, done.ErrorMsg)
	}
	if h.passwordNow() != newPassword {
		t.Error("finishing the change did not give the panel the password the database took")
	}
}

// An app that could not be rolled out still has the old connection string,
// so where the old password could be kept it is.
func TestTheOldPasswordStaysWhileAnAppStillUsesIt(t *testing.T) {
	h := newLifeHarness(t, EngineRedis, nil)
	h.deployer.fail[h.app.ID] = true
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, "", "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitForOperation(op.ID)
	if done.Status != store.OpSucceeded {
		t.Fatalf("the change ended %s: %s", done.Status, done.ErrorMsg)
	}
	if step := stepOf(done, "finish"); step.Status != store.StepSkipped || step.MessageKey != "oldPasswordKept" {
		t.Errorf("the last step is %+v", step)
	}
	if jobs := strings.Join(h.jobsApplied(), " "); strings.Contains(jobs, "discard") {
		t.Errorf("the old password was taken away: %s", jobs)
	}
	if step := stepOf(done, "sync"); step.Status != store.StepFailed {
		t.Errorf("the sync step is %+v", step)
	}
}

// An engine that reads its password only as it starts is given it through
// its Secret and restarted, and ready is the proof.
func TestDragonflyIsRestartedOntoItsNewPassword(t *testing.T) {
	h := newLifeHarness(t, EngineDragonfly, nil)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orders-0", Namespace: h.env.Namespace,
		Labels: map[string]string{"app.kubernetes.io/name": "orders"}}}
	if _, err := h.clientset.CoreV1().Pods(h.env.Namespace).Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	op, err := h.m.ChangePassword(t.Context(), h.record.ID, "", "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitForOperation(op.ID)
	if done.Status != store.OpSucceeded {
		t.Fatalf("the change ended %s: %s", done.Status, done.ErrorMsg)
	}
	if _, err := h.clientset.CoreV1().Pods(h.env.Namespace).Get(t.Context(), "orders-0", metav1.GetOptions{}); err == nil {
		t.Error("the instance was not restarted")
	}
	if len(h.jobsApplied()) != 0 {
		t.Errorf("Dragonfly was given jobs: %v", h.jobsApplied())
	}
	if h.passwordNow() == plantedPassword || h.secretsWith(h.passwordNow()) == 0 {
		t.Error("the new password is not the panel's and the Secret's")
	}
}

// What is refused before anything starts.
func TestAPasswordChangeIsRefusedWhereItCannotBeMade(t *testing.T) {
	cache := newLifeHarness(t, EngineMemcached, nil)
	if _, err := cache.m.ChangePassword(t.Context(), cache.record.ID, "", "usr_1"); codeOf(err) != "database.password_not_offered" {
		t.Errorf("Memcached: %v", err)
	}
	h := newLifeHarness(t, EnginePostgres, nil)
	for _, bad := range []string{"short", "has a space in it and is long", "quote'inside-a-long-password", strings.Repeat("x", 129)} {
		if _, err := h.m.ChangePassword(t.Context(), h.record.ID, bad, "usr_1"); codeOf(err) != "database.password_rejected" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := h.m.ChangePassword(t.Context(), h.record.ID, plantedPassword, "usr_1"); codeOf(err) != "request.invalid" {
		t.Errorf("the password it has: %v", err)
	}
	if err := h.db.SetDatabaseStatus(t.Context(), h.record.ID, StatusStopped, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.ChangePassword(t.Context(), h.record.ID, "", "usr_1"); codeOf(err) != "database.not_running" {
		t.Errorf("a stopped database: %v", err)
	}
	if !ValidPassword(newPassword) || ValidPassword("x") {
		t.Error("ValidPassword is wrong about the planted ones")
	}
	generated, err := GeneratePassword()
	if err != nil || !ValidPassword(generated) {
		t.Errorf("a generated password %q is not valid: %v", generated, err)
	}
}
