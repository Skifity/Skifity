package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// An app's files: an nginx.conf, a Caddyfile, a script its image runs at
// start. They are one Secret per app, each file a key in it, each mounted
// read-only at its own path with subPath so the rest of the directory it
// lands in is still the image's.
//
// A Secret rather than a ConfigMap because a configuration file is where a
// password ends up as often as a variable is, and a ConfigMap is readable by
// anything allowed to read the namespace's configuration.
//
// A subPath mount does not see the Secret change. That is on purpose here:
// the pod template carries a hash of the contents (FilesHash), so a changed
// file is a rollout — the same as a changed variable — rather than a file
// that changes under a running process which read it once at start.

// FileMount is one of an app's files, as the pod sees it.
type FileMount struct {
	// Key is the file's key in the Secret. The path cannot be one: a key
	// may not contain a slash.
	Key        string
	Path       string
	Executable bool
}

// filesVolume is the pod volume the files come from. Two hyphens, which no
// volume a person names can have: those are slugs.
const filesVolume = "skifity--files"

// Limits on an app's files. A Secret holds at most 1 MiB, and the whole of it
// travels with every pod start, so this is configuration and not storage: a
// volume is for data.
const (
	MaxFiles         = 50
	MaxFileBytes     = 256 << 10
	MaxAllFilesBytes = 900 << 10
)

// FilesSecretName is the Secret holding an app's files.
func FilesSecretName(appName string) string { return ResourceName(appName, "files") }

// FileKey is the key a file has in its Secret: stable for a path, and a valid
// key whatever the path holds.
func FileKey(filePath string) string {
	sum := sha256.Sum256([]byte(filePath))
	base := path.Base(filePath)
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, base)
	if len(clean) > 40 {
		clean = clean[:40]
	}
	return hex.EncodeToString(sum[:])[:10] + "-" + strings.Trim(clean, ".")
}

// forbiddenFilePaths are paths the kubelet itself writes into every
// container. A file mounted over one breaks name resolution or the hostname,
// and the pod does not say why.
var forbiddenFilePaths = map[string]bool{
	"/etc/hosts": true, "/etc/hostname": true, "/etc/resolv.conf": true,
}

// forbiddenFileRoots are filesystems the container runtime provides. A mount
// under one either fails to start or hides what the process needs.
var forbiddenFileRoots = []string{"/proc", "/sys", "/dev"}

// ValidateFilePath says why a path cannot hold an app's file, or nil.
func ValidateFilePath(filePath string) error {
	switch {
	case filePath == "":
		return fmt.Errorf("a file needs a path")
	case !strings.HasPrefix(filePath, "/"):
		return fmt.Errorf("%q is not an absolute path: a file's path starts with /", filePath)
	case len(filePath) > 512:
		return fmt.Errorf("a file's path is at most 512 characters")
	case strings.ContainsAny(filePath, "\x00\n\r\\"):
		return fmt.Errorf("%q has a character a path cannot", filePath)
	case path.Clean(filePath) != filePath:
		return fmt.Errorf("%q is not written the plain way; did you mean %q?", filePath, path.Clean(filePath))
	case strings.HasSuffix(filePath, "/") || filePath == "/":
		return fmt.Errorf("%q is a directory, and a file's path names a file", filePath)
	case forbiddenFilePaths[filePath]:
		return fmt.Errorf("%s is written by Kubernetes into every container, so a file cannot go there", filePath)
	}
	for _, root := range forbiddenFileRoots {
		if filePath == root || strings.HasPrefix(filePath, root+"/") {
			return fmt.Errorf("%s is provided by the container runtime, so a file cannot go under it", root)
		}
	}
	return nil
}

// validateFiles checks an app's files against each other and its volumes.
func validateFiles(s AppSpec) error {
	if len(s.Files) > MaxFiles {
		return fmt.Errorf("an app can have at most %d files, and this one has %d", MaxFiles, len(s.Files))
	}
	seen := map[string]bool{}
	for _, f := range s.Files {
		if err := ValidateFilePath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("two files are both at %s", f.Path)
		}
		seen[f.Path] = true
		// A file at exactly a volume's path would replace the volume's
		// directory with a file; inside one is fine, and is how a
		// configuration file beside the data it configures is placed.
		for _, v := range s.Volumes {
			if f.Path == v.MountPath {
				return fmt.Errorf("%s is where the volume %s is mounted, so a file cannot be there too", f.Path, v.Name)
			}
		}
	}
	return nil
}

// FilesHash is a digest of an app's files, their paths, modes and contents,
// for the pod template: a changed file is a rollout.
func FilesHash(files []FileMount, contents map[string][]byte) string {
	sorted := append([]FileMount(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00%t\x00%d\x00", f.Path, f.Executable, len(contents[f.Key]))
		h.Write(contents[f.Key])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// BuildFilesSecret renders the Secret holding an app's files, or nil when it
// has none.
func BuildFilesSecret(s AppSpec, contents map[string][]byte) *corev1.Secret {
	if len(s.Files) == 0 {
		return nil
	}
	data := make(map[string][]byte, len(s.Files))
	for _, f := range s.Files {
		data[f.Key] = contents[f.Key]
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      FilesSecretName(s.filesOwner()),
			Namespace: s.Namespace,
			Labels:    s.Labels(),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
}

// filesOwner is the app whose Secret holds the files: a process's are its
// app's.
func (s AppSpec) filesOwner() string {
	if s.ProcessOf != "" {
		return s.ProcessOf
	}
	return s.Name
}

// fileMounts are the container mounts for an app's files.
func fileMounts(s AppSpec) []corev1.VolumeMount {
	out := make([]corev1.VolumeMount, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, corev1.VolumeMount{
			Name: filesVolume, MountPath: f.Path, SubPath: f.Key, ReadOnly: true,
		})
	}
	return out
}

// filesPodVolume is the pod volume the mounts read, or nil without files.
func filesPodVolume(s AppSpec) *corev1.Volume {
	if len(s.Files) == 0 {
		return nil
	}
	items := make([]corev1.KeyToPath, 0, len(s.Files))
	for _, f := range s.Files {
		// Readable by everyone in the container: the process's uid is the
		// image's and nobody here knows it. The mount is read-only whatever
		// the mode says.
		mode := int32(0o444)
		if f.Executable {
			mode = 0o555
		}
		items = append(items, corev1.KeyToPath{Key: f.Key, Path: f.Key, Mode: &mode})
	}
	return &corev1.Volume{
		Name: filesVolume,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: FilesSecretName(s.filesOwner()), Items: items},
		},
	}
}
