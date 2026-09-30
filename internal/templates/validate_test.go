package templates

import (
	"strings"
	"testing"
)

// The checks are run over the built-in catalogue by the tests beside this one,
// which pass when a check finds nothing. A check that has stopped finding
// anything passes them too. So each is also shown a template that breaks the
// rule it is for, and has to say so — the built-in catalogue is held to them
// in a test, and a team's catalogue is held to them when it is loaded, and a
// check that went quiet would wave both through.

// good is a template every check accepts, for the cases below to break one
// thing at a time.
func good() Template {
	return Template{
		ID: "notes", Name: "Notes", Description: "Somewhere to write things down.",
		Category: "productivity", Website: "https://notes.example.org",
		Services: []Service{{
			Name: "notes", Image: "example/notes:2.4.1", Port: 8080, Public: true,
			HealthPath: "/healthz", Variables: map[string]string{"TZ": "UTC"},
			Volumes: []VolumeSpec{{Name: "data", MountPath: "/var/lib/notes", SizeGB: 1}},
			Files:   []FileSpec{{Path: "/etc/notes/config.yml", Content: "listen: 8080\n"}},
		}},
		Databases: []DatabaseSpec{{
			Name: "notes-db", Engine: "postgres", StorageGB: 5, LinkTo: []string{"notes"},
			VarName: "DATABASE_URL", Vars: DatabaseVars{Host: "DB_HOST", Password: "DB_PASSWORD"},
		}},
		Inputs: []Input{{Key: "SECRET_KEY", Label: "Secret key", Secret: true, Generate: true}},
	}
}

func TestAGoodTemplatePassesEveryCheck(t *testing.T) {
	if problems := Validate(good()); len(problems) != 0 {
		t.Fatalf("the example every case below starts from is refused: %q", problems)
	}
}

func TestEveryCheckRefusesWhatItIsFor(t *testing.T) {
	cases := []struct {
		name   string
		check  Check
		mutate func(*Template)
		says   string
	}{
		{"an id that is not a slug", CheckComplete, func(t *Template) { t.ID = "My Notes" }, "not already a slug"},
		{"no description", CheckComplete, func(t *Template) { t.Description = " " }, "has no description"},
		{"a website over http", CheckComplete, func(t *Template) { t.Website = "http://notes.example.org" }, "must be https"},
		{"nothing to install", CheckComplete, func(t *Template) { t.Services = nil }, "installs nothing"},
		{"a service name that is not a slug", CheckServices, func(t *Template) { t.Services[0].Name = "Notes App" }, "is not a slug"},
		{"a port out of range", CheckServices, func(t *Template) { t.Services[0].Port = 70000 }, "listens on 70000"},
		{"a public service with no port", CheckServices, func(t *Template) { t.Services[0].Port = 0 }, "listens on nothing"},
		{"nothing public", CheckServices, func(t *Template) { t.Services[0].Public = false }, "no public service"},
		{"a relative mount path", CheckServices, func(t *Template) { t.Services[0].Volumes[0].MountPath = "data" }, "not an absolute path"},
		{"more requested than allowed", CheckServices, func(t *Template) {
			t.Services[0].MemRequestMB, t.Services[0].MemLimitMB = 512, 256
		}, "more memory"},
		{"a variable a container cannot carry", CheckServices, func(t *Template) { t.Services[0].Variables["1BAD"] = "x" }, "cannot carry"},
		{"a port that is not a port", CheckServices, func(t *Template) {
			t.Services[0].Ports = []PortSpec{{Port: 25565, Protocol: "sctp"}}
		}, "notes/notes"},
		{"a public worker", CheckWorkers, func(t *Template) { t.Services[0].Name = "worker" }, "named for a worker"},
		{"an engine nobody runs", CheckDatabases, func(t *Template) { t.Databases[0].Engine = "oracle" }, "not an engine"},
		{"a database linked to nothing", CheckDatabases, func(t *Template) { t.Databases[0].LinkTo = nil }, "linked to nothing"},
		{"a database linked to a service that is not there", CheckDatabases, func(t *Template) {
			t.Databases[0].LinkTo = []string{"web"}
		}, "not a service in this template"},
		{"no storage", CheckDatabases, func(t *Template) { t.Databases[0].StorageGB = 0 }, "GB of storage"},
		{"latest", CheckVersions, func(t *Template) { t.Services[0].Image = "example/notes:latest" }, "whatever is newest"},
		{"no tag", CheckVersions, func(t *Template) { t.Services[0].Image = "example/notes" }, "no tag"},
		{"a branch", CheckVersions, func(t *Template) { t.Services[0].Image = "example/notes:main-stable" }, "whatever is newest"},
		{"a major version only", CheckVersions, func(t *Template) { t.Services[0].Image = "example/notes:5-alpine" }, "that major version"},
		{"a registry port is not a tag", CheckVersions, func(t *Template) { t.Services[0].Image = "registry:5000/notes" }, "no tag"},
		{"an input nobody fills in", CheckInputs, func(t *Template) { t.Inputs[0].Generate = false }, "does nothing"},
		{"an input both generated and defaulted", CheckInputs, func(t *Template) { t.Inputs[0].Default = "x" }, "both generated and defaulted"},
		{"a database wired by hand", CheckWiring, func(t *Template) { t.Services[0].Variables["DB_HOST"] = "mariadb" }, "does not exist"},
		{"a URL at a container that is not there", CheckWiring, func(t *Template) {
			t.Services[0].Variables["PAPERLESS_REDIS"] = "redis://redis:6379"
		}, "neither a service"},
		{"a value nobody expanded", CheckWiring, func(t *Template) { t.Services[0].Variables["TZ"] = "${TZ}" }, "never expanded"},
		{"a file where a volume is", CheckFiles, func(t *Template) { t.Services[0].Files[0].Path = "/var/lib/notes" }, "where the volume"},
		{"two files at one path", CheckFiles, func(t *Template) {
			t.Services[0].Files = append(t.Services[0].Files, t.Services[0].Files[0])
		}, "two files"},
		{"a piece under a name no container carries", CheckDatabasePieces, func(t *Template) {
			t.Databases[0].Vars.User = "db-user"
		}, "not a variable name"},
		{"two pieces under one name", CheckDatabasePieces, func(t *Template) {
			t.Databases[0].Vars.Password = "DB_HOST"
		}, "delivers two things"},
	}
	for _, c := range cases {
		template := good()
		c.mutate(&template)
		problems := c.check(template)
		if !contains(problems, c.says) {
			t.Errorf("%s: the check answered %q, which does not say %q", c.name, problems, c.says)
		}
		// And Validate, which is what a team's catalogue is read with, says
		// the same: a check left out of Checks would be tested here and never
		// run there.
		if !contains(Validate(template), c.says) {
			t.Errorf("%s: Validate does not run the check that catches it", c.name)
		}
	}
}

func TestTwoTemplatesCannotShareAnID(t *testing.T) {
	one, two := good(), good()
	two.Name = "Other notes"
	problems := DuplicateIDs([]Template{one, two})
	if !contains(problems, `share the id "notes"`) {
		t.Fatalf("two templates called notes answered %q", problems)
	}
}

// A field this version does not know is refused rather than dropped:
// `mount_pth` read loosely is a volume mounted nowhere.
func TestParseRefusesAFieldItDoesNotKnow(t *testing.T) {
	_, err := Parse([]byte("id: notes\nservices:\n- name: notes\n  volumes:\n  - name: data\n    mount_pth: /data\n"))
	if err == nil || !strings.Contains(err.Error(), "mount_pth") {
		t.Fatalf("a misspelt field was accepted: %v", err)
	}
	template, err := Parse([]byte("id: notes\nname: Notes\n"))
	if err != nil {
		t.Fatal(err)
	}
	if template.Services == nil || template.Databases == nil || template.Inputs == nil {
		t.Errorf("a template's lists are left nil, which the API answers as null: %+v", template)
	}
}

func contains(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}
