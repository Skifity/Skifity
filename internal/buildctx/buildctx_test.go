// Package buildctx has no code. It holds the test that checks the container
// image could be built at all.
//
// Building the image needs a Docker daemon, and the environment this was
// written in has none, so `make image` was never run — and two things in it
// were wrong for as long as it existed. The frontend stage did not copy
// llms.txt, which the frontend build reads from the repository root, and
// `.dockerignore` excluded `docs`, which is a Go package embedded into the
// binary. Both fail the build immediately; neither could be seen from here.
//
// This cannot run Docker either. What it can do is read the Dockerfile and the
// ignore file and check that what the build needs is reachable, which is what
// both failures were about.
package buildctx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func root(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("locate the repository root: %v", err)
	}
	return path
}

// ignored reads .dockerignore into the patterns it excludes.
func ignored(t *testing.T, base string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(base, ".dockerignore"))
	if err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.TrimPrefix(line, "./"))
	}
	return out
}

// excludes reports whether .dockerignore keeps path out of the build context.
// Docker matches a pattern against the whole path, and a directory pattern
// takes everything under it.
func excludes(patterns []string, path string) string {
	for _, pattern := range patterns {
		if pattern == path || strings.HasPrefix(path, pattern+"/") {
			return pattern
		}
		if matched, _ := filepath.Match(pattern, path); matched {
			return pattern
		}
	}
	return ""
}

// embedDirective finds //go:embed lines and the paths they name.
var embedDirective = regexp.MustCompile(`(?m)^//go:embed\s+(.+)$`)

// TestEverythingEmbeddedSurvivesTheBuildContext: a //go:embed line is a
// compile-time requirement. If .dockerignore removes what it names, the image
// build fails on a file that is right there in the repository, and the message
// blames Go rather than the ignore file.
func TestEverythingEmbeddedSurvivesTheBuildContext(t *testing.T) {
	base := root(t)
	patterns := ignored(t, base)

	checked := 0
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		relative, _ := filepath.Rel(base, path)
		if strings.HasPrefix(relative, "web/node_modules") || strings.HasPrefix(relative, ".git/") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range embedDirective.FindAllStringSubmatch(string(body), -1) {
			// The file holding the directive has to be in the context too.
			if pattern := excludes(patterns, relative); pattern != "" {
				t.Errorf(".dockerignore excludes %s (pattern %q), and it holds a //go:embed",
					relative, pattern)
			}
			for _, spec := range strings.Fields(match[1]) {
				checked++
				target := filepath.ToSlash(filepath.Join(filepath.Dir(relative), spec))
				if pattern := excludes(patterns, target); pattern != "" {
					t.Errorf("%s embeds %s, and .dockerignore excludes it (pattern %q)",
						relative, spec, pattern)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	if checked < 3 {
		t.Fatalf("only %d embedded paths were found; this test is not reading the repository", checked)
	}
}

// TestTheImageBuildCopiesWhatTheFrontendBuildReads: the frontend build copies
// llms.txt in from the repository root so the panel can serve it. The Dockerfile
// builds the frontend from web/ alone, so unless it copies that file the build
// stops there — which is exactly what happened on the first CI run that ever
// reached this step.
func TestTheImageBuildCopiesWhatTheFrontendBuildReads(t *testing.T) {
	base := root(t)

	scripts, err := os.ReadFile(filepath.Join(base, "web", "package.json"))
	if err != nil {
		t.Fatalf("read web/package.json: %v", err)
	}
	// Every path the build scripts reach for outside web/.
	outside := regexp.MustCompile(`\.\./([A-Za-z0-9_.-]+)`).FindAllStringSubmatch(string(scripts), -1)
	if len(outside) == 0 {
		t.Skip("the frontend build no longer reads anything outside web/")
	}

	dockerfile, err := os.ReadFile(filepath.Join(base, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	patterns := ignored(t, base)

	for _, match := range outside {
		name := match[1]
		if !strings.Contains(string(dockerfile), "COPY "+name+" ") {
			t.Errorf("the frontend build reads ../%s and the Dockerfile never copies it in", name)
		}
		if pattern := excludes(patterns, name); pattern != "" {
			t.Errorf(".dockerignore excludes %s (pattern %q), which the frontend build reads", name, pattern)
		}
	}
}
