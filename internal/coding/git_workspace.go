package coding

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LocalGit runs only fixed host-owned Git commands. Repository scripts, hooks,
// filters, submodules and tests are never executed. No shell is exposed to AI.
type LocalGit struct{ binary string }

func NewLocalGit() (*LocalGit, error) {
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, errors.New("git kurulu değil")
	}
	return &LocalGit{binary: binary}, nil
}

type gitDirectory struct {
	engine     *LocalGit
	root, repo string
}

func (g *LocalGit) temporary(ctx context.Context) (*gitDirectory, context.Context, func(), error) {
	root, err := os.MkdirTemp("", "nudgy-edit-")
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	done := make(chan struct{})
	// Enforce a small checkout budget as well as the manifest budget. This
	// limits concurrent clone resource use without running repository code.
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var size int64
				_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if info, err := d.Info(); err == nil {
						size += info.Size()
					}
					if size > 128<<20 {
						cancel()
						return fs.SkipAll
					}
					return nil
				})
			}
		}
	}()
	cleanup := func() { cancel(); <-done; _ = os.RemoveAll(root) }
	return &gitDirectory{engine: g, root: root, repo: filepath.Join(root, "repo.git")}, ctx, cleanup, nil
}

type boundedGitOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("git çıktı sınırı aşıldı")
	}
	return b.Buffer.Write(p)
}

func (d *gitDirectory) run(ctx context.Context, token string, input []byte, limit int, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, d.engine.binary, args...)
	isolateGitProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = d.root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + d.root, "XDG_CONFIG_HOME=" + d.root,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false",
		"GIT_ALLOW_PROTOCOL=https", "GIT_NO_REPLACE_OBJECTS=1", "GIT_LITERAL_PATHSPECS=1", "LC_ALL=C",
		"GIT_CONFIG_COUNT=4", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_CONFIG_KEY_1=http.followRedirects", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=credential.helper", "GIT_CONFIG_VALUE_2=",
		"GIT_CONFIG_KEY_3=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_3="}
	if token != "" {
		cmd.Env[len(cmd.Env)-1] = "GIT_CONFIG_VALUE_3=AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = bytes.NewReader(input)
	out := &boundedGitOutput{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Git işlemi tamamlanamadı; erişim, repo boyutu veya süre sınırını kontrol et")
	}
	return out.Bytes(), nil
}

func (d *gitDirectory) git(ctx context.Context, input []byte, limit int, args ...string) ([]byte, error) {
	return d.run(ctx, "", input, limit, nil, append([]string{"--git-dir=" + d.repo}, args...)...)
}

func (d *gitDirectory) clone(ctx context.Context, token string, repo Repository) error {
	if !ValidRepository(repo.FullName) || repo.ID <= 0 || repo.InstallationID <= 0 || repo.DefaultBranch == "" {
		return ErrArguments
	}
	if _, err := d.run(ctx, "", nil, 1024, nil, "check-ref-format", "refs/heads/"+repo.DefaultBranch); err != nil {
		return ErrArguments
	}
	_, err := d.run(ctx, token, nil, 4096, nil, "clone", "--bare", "--depth=1", "--single-branch", "--no-tags", "--template=", "--branch", repo.DefaultBranch, "--", "https://github.com/"+repo.FullName+".git", d.repo)
	return err
}

func (g *LocalGit) Clone(ctx context.Context, token string, repo Repository) (Snapshot, error) {
	d, ctx, cleanup, err := g.temporary(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer cleanup()
	if err := d.clone(ctx, token, repo); err != nil {
		return Snapshot{}, err
	}
	return d.snapshot(ctx, repo)
}

func (d *gitDirectory) snapshot(ctx context.Context, repo Repository) (Snapshot, error) {
	result := Snapshot{Repository: repo}
	for _, v := range []struct {
		ref string
		dst *string
	}{{"HEAD", &result.Commit}, {"HEAD^{tree}", &result.Tree}} {
		out, err := d.git(ctx, nil, 256, "rev-parse", "--verify", v.ref)
		if err != nil {
			return Snapshot{}, err
		}
		*v.dst = strings.TrimSpace(string(out))
		if !gitObjectID.MatchString(*v.dst) {
			return Snapshot{}, ErrUnavailable
		}
	}
	listing, err := d.git(ctx, nil, 2<<20, "ls-tree", "-r", "-z", "-l", result.Commit)
	if err != nil {
		return Snapshot{}, err
	}
	total := 0
	for _, entry := range bytes.Split(listing, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return Snapshot{}, ErrUnavailable
		}
		header := strings.Fields(string(parts[0]))
		name := string(parts[1])
		if len(header) != 4 || !ValidFilePath(name) || (header[0] != "100644" && header[0] != "100755") || header[1] != "blob" || !gitObjectID.MatchString(header[2]) {
			return Snapshot{}, errors.New("repo desteklenmeyen dosya yolu, symlink veya alt modül içeriyor")
		}
		size, err := strconv.Atoi(header[3])
		if err != nil || size < 0 || size > MaxSnapshotBytes-total || len(result.Files) >= 5000 {
			return Snapshot{}, errors.New("repo çalışma kopyası sınırını aşıyor (32 MB / 5000 dosya)")
		}
		total += size
		content, err := d.git(ctx, nil, size+1, "cat-file", "blob", header[2])
		if err != nil || len(content) != size {
			return Snapshot{}, ErrUnavailable
		}
		result.Files = append(result.Files, RepositoryFile{Path: name, Mode: header[0], Content: content})
	}
	return result, nil
}

func (d *gitDirectory) commit(ctx context.Context, review Review) (string, error) {
	if err := review.Validate(); err != nil {
		return "", err
	}
	baseTree, err := d.git(ctx, nil, 256, "rev-parse", "--verify", review.BaseCommit+"^{tree}")
	if err != nil || strings.TrimSpace(string(baseTree)) != review.BaseTree {
		return "", ErrDenied
	}
	if _, err := d.git(ctx, nil, 4096, "read-tree", review.BaseTree); err != nil {
		return "", err
	}
	for _, change := range review.Changes {
		object := strings.Repeat("0", 40)
		mode := "0"
		if !change.Delete {
			out, err := d.git(ctx, change.Content, 256, "hash-object", "-w", "--stdin")
			if err != nil {
				return "", err
			}
			object = strings.TrimSpace(string(out))
			mode = change.Mode
			if !gitObjectID.MatchString(object) {
				return "", ErrUnavailable
			}
		}
		if _, err := d.git(ctx, []byte(mode+" "+object+"\t"+change.Path+"\x00"), 4096, "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, err := d.git(ctx, nil, 256, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if !gitObjectID.MatchString(tree) {
		return "", ErrUnavailable
	}
	digest, err := review.Digest()
	if err != nil {
		return "", err
	}
	stamp := review.CreatedAt.UTC().Format(time.RFC3339)
	env := []string{"GIT_AUTHOR_NAME=Nudgy", "GIT_AUTHOR_EMAIL=nudgy@users.noreply.github.com", "GIT_COMMITTER_NAME=Nudgy", "GIT_COMMITTER_EMAIL=nudgy@users.noreply.github.com", "GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp}
	message := fmt.Sprintf("Nudgy: approved changes\n\n%s\n\nNudgy-Review: %s\n", review.Summary, digest)
	out, err = d.run(ctx, "", []byte(message), 256, env, "--git-dir="+d.repo, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", review.BaseCommit)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if !gitObjectID.MatchString(commit) {
		return "", ErrUnavailable
	}
	return commit, nil
}

func (g *LocalGit) Push(ctx context.Context, token string, review Review) (string, error) {
	if err := review.Validate(); err != nil {
		return "", err
	}
	d, ctx, cleanup, err := g.temporary(ctx)
	if err != nil {
		return "", err
	}
	defer cleanup()
	if err := d.clone(ctx, token, review.Repository); err != nil {
		return "", err
	}
	ref := "refs/heads/" + review.Branch
	remote, err := d.run(ctx, token, nil, 1024, nil, "--git-dir="+d.repo, "ls-remote", "--heads", "origin", ref)
	if err != nil {
		return "", err
	}
	head, err := d.git(ctx, nil, 256, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(head)) != review.BaseCommit {
		if len(remote) == 0 {
			return "", errors.New("ana dal değişti; güncel kodla yeni görev hazırlayıp yeniden test etmelisin")
		}
		// Recover a successful push whose response/database commit was lost.
		if _, err := d.run(ctx, token, nil, 4096, nil, "--git-dir="+d.repo, "fetch", "--depth=1", "origin", review.BaseCommit); err != nil {
			return "", err
		}
	}
	commit, err := d.commit(ctx, review)
	if err != nil {
		return "", err
	}
	if len(remote) != 0 {
		if string(remote) == commit+"\t"+ref+"\n" {
			return commit, nil
		}
		return "", errors.New("hedef dalda farklı değişiklik var; üzerine yazılmadı")
	}
	// The explicit empty lease only permits creating a previously absent ref.
	_, err = d.run(ctx, token, nil, 4096, nil, "--git-dir="+d.repo, "push", "--porcelain", "--force-with-lease="+ref+":", "origin", commit+":"+ref)
	if err != nil {
		return "", err
	}
	return commit, nil
}
