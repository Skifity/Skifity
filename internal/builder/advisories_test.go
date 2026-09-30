package builder

import (
	"strings"
	"testing"
)

func advisoryIDs(found []Advisory) string {
	var ids []string
	for _, a := range found {
		ids = append(ids, a.Package+"@"+a.Version+":"+a.ID+"->"+a.FixedIn)
	}
	return strings.Join(ids, " ")
}

// The versions each advisory names, at their edges: the first affected, the
// last affected, and the fix.
func TestAnAffectedVersionIsFoundAndAFixedOneIsNot(t *testing.T) {
	cases := map[string]string{
		"11.1.3":  "",
		"11.1.4":  "next@11.1.4:CVE-2025-29927->12.3.5",
		"12.3.5":  "",
		"14.2.24": "next@14.2.24:CVE-2025-29927->14.2.25",
		"14.2.25": "",
		// Both at once: the middleware bypass and the React hole.
		"15.1.0": "next@15.1.0:CVE-2025-29927->15.2.3 next@15.1.0:CVE-2025-55182->15.1.9",
		"15.1.9": "next@15.1.9:CVE-2025-29927->15.2.3",
		"15.2.3": "next@15.2.3:CVE-2025-55182->15.2.6",
		"15.5.7": "",
		"16.0.6": "next@16.0.6:CVE-2025-55182->16.0.7",
		"16.0.7": "",
		// A canary is not checked, rather than guessed at.
		"15.6.0-canary.57": "",
	}
	for version, want := range cases {
		tree := Tree{Files: []string{"package.json"}, Contents: map[string]string{
			"package.json": `{"dependencies": {"next": "` + version + `"}}`,
		}}
		if got := advisoryIDs(FindAdvisories(tree)); got != want {
			t.Errorf("next %s: %q, want %q", version, got, want)
		}
	}
}

// A range in package.json says nothing about what is installed: that is the
// lockfile's to say.
func TestTheLockfileDecidesAndARangeAloneDoesNot(t *testing.T) {
	ranged := `{"dependencies": {"next": "^15.1.0", "react-server-dom-webpack": "~19.0.0"}}`
	if got := FindAdvisories(Tree{Contents: map[string]string{"package.json": ranged}}); len(got) != 0 {
		t.Fatalf("a range alone was taken for a version: %s", advisoryIDs(got))
	}

	locks := map[string]string{
		"package-lock.json": `{
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"next": "^15.1.0"}},
    "node_modules/next": {
      "version": "15.1.0",
      "resolved": "https://registry.npmjs.org/next/-/next-15.1.0.tgz"
    },
    "node_modules/next-auth": {"version": "4.24.0"},
    "node_modules/@next/env": {"version": "15.1.0"}
  }
}`,
		"pnpm-lock.yaml": `lockfileVersion: '9.0'
packages:
  '@next/env@15.1.0':
    resolution: {integrity: sha512-x}
  next-auth@4.24.0:
    resolution: {integrity: sha512-x}
  next@15.1.0(react-dom@19.0.0(react@19.0.0))(react@19.0.0):
    resolution: {integrity: sha512-x}
`,
		"yarn.lock": `# yarn lockfile v1

next-auth@^4.24.0:
  version "4.24.0"

next@^15.1.0:
  version "15.1.0"
  resolved "https://registry.yarnpkg.com/next/-/next-15.1.0.tgz"
`,
		"bun.lock": `{
  "lockfileVersion": 1,
  "packages": {
    "next": ["next@15.1.0", "", { "dependencies": {} }, "sha512-x"],
    "next-auth": ["next-auth@4.24.0", "", {}, "sha512-x"],
  }
}`,
	}
	for lock, content := range locks {
		tree := Tree{Contents: map[string]string{"package.json": ranged, lock: content}}
		got := advisoryIDs(FindAdvisories(tree))
		if got != "next@15.1.0:CVE-2025-29927->15.2.3 next@15.1.0:CVE-2025-55182->15.1.9" {
			t.Errorf("%s: %q", lock, got)
		}
	}

	// Yarn 2 and later.
	berry := `"next@npm:^15.5.0":
  version: 15.5.6
  resolution: "next@npm:15.5.6"
`
	if got := advisoryIDs(FindAdvisories(Tree{Contents: map[string]string{"yarn.lock": berry}})); got != "next@15.5.6:CVE-2025-55182->15.5.7" {
		t.Errorf("yarn berry: %q", got)
	}

	// A fixed version in the lockfile outranks a pinned one in package.json.
	fixed := Tree{Contents: map[string]string{
		"package.json":      `{"dependencies": {"next": "15.1.0"}}`,
		"package-lock.json": `{"packages": {"node_modules/next": {"version": "15.5.7"}}}`,
	}}
	if got := FindAdvisories(fixed); len(got) != 0 {
		t.Errorf("the lockfile's fixed version was overruled: %s", advisoryIDs(got))
	}
}

// The React packages themselves, which other frameworks use too.
func TestTheReactServerPackagesAreChecked(t *testing.T) {
	tree := Tree{Contents: map[string]string{"package-lock.json": `{"packages": {
		"node_modules/react-server-dom-webpack": {"version": "19.1.1"},
		"node_modules/react-server-dom-parcel": {"version": "19.1.2"}
	}}`}}
	if got := advisoryIDs(FindAdvisories(tree)); got != "react-server-dom-webpack@19.1.1:CVE-2025-55182->19.1.2" {
		t.Fatalf("%q", got)
	}
}
