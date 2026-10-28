package coding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RepositoryFile contains bytes, never a local filename or credentials. JSON
// encodes Content as base64 so binary files are not corrupted in transit.
type RepositoryFile struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Content []byte `json:"content"`
}

type Snapshot struct {
	Repository Repository       `json:"repository"`
	Commit     string           `json:"commit"`
	Tree       string           `json:"tree"`
	Files      []RepositoryFile `json:"files"`
}

type FileChange struct {
	RepositoryFile
	Delete bool `json:"delete"`
}

// Review is the immutable payload presented before approval. A change to any
// file, base revision, target branch or test state invalidates its digest.
type Review struct {
	TaskID     string       `json:"task_id"`
	Repository Repository   `json:"repository"`
	BaseCommit string       `json:"base_commit"`
	BaseTree   string       `json:"base_tree"`
	Branch     string       `json:"branch"`
	Summary    string       `json:"summary"`
	Changes    []FileChange `json:"changes"`
	Tests      string       `json:"tests"`
	CreatedAt  time.Time    `json:"created_at"`
}

var gitObjectID = regexp.MustCompile(`^[0-9a-f]{40}$`)

const (
	MaxSnapshotBytes = 32 << 20
	MaxChangeBytes   = 8 << 20
	MaxChangedFiles  = 200
)

func ValidFilePath(name string) bool {
	if name == "" || !utf8.ValidString(name) || len(name) > 1024 || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (r Review) Validate() error {
	if !taskID.MatchString(r.TaskID) || !ValidRepository(r.Repository.FullName) || r.Repository.ID <= 0 || r.Repository.InstallationID <= 0 || !gitObjectID.MatchString(r.BaseCommit) || !gitObjectID.MatchString(r.BaseTree) {
		return ErrArguments
	}
	if r.Branch != "codex/nudgy-"+r.TaskID || len(r.Changes) == 0 || len(r.Changes) > MaxChangedFiles || len(r.Summary) > 16000 || r.CreatedAt.IsZero() {
		return ErrArguments
	}
	seen := make(map[string]bool)
	size := 0
	for _, f := range r.Changes {
		if !ValidFilePath(f.Path) || seen[f.Path] || (f.Mode != "100644" && f.Mode != "100755") || (f.Delete && len(f.Content) != 0) {
			return ErrArguments
		}
		seen[f.Path] = true
		size += len(f.Content)
		if size > MaxChangeBytes {
			return errors.New("değişiklik inceleme boyutu sınırını aşıyor")
		}
	}
	// A file cannot simultaneously replace a directory containing another
	// changed file. Reject ambiguous manifests before GitHub sees them.
	for name := range seen {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return ErrArguments
			}
		}
	}
	if r.Tests != "not_run" {
		return errors.New("bu akışta otomatik test çalıştırılmaz")
	}
	return nil
}

func (r Review) Digest() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	r.Changes = append([]FileChange(nil), r.Changes...)
	sort.Slice(r.Changes, func(i, j int) bool { return r.Changes[i].Path < r.Changes[j].Path })
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
