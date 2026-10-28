package coding

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func localGitFixture(t *testing.T) (*LocalGit, Repository, string, func(...string) string) {
	t.Helper()
	binary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com"}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, stderr.String())
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--initial-branch=main", source)
	for name, content := range map[string][]byte{"README.md": []byte("before\n"), "binary.dat": {0, 1, 2, 255}, "keep.txt": []byte("unchanged\n"), "remove.txt": []byte("remove me"), "script.sh": []byte("#!/bin/sh\nexit 99\n")} {
		if err := os.WriteFile(filepath.Join(source, name), content, 0755); err != nil {
			t.Fatal(err)
		}
	}
	run("-C", source, "add", ".")
	run("-C", source, "commit", "-m", "base")
	run("clone", "--bare", source, remote)
	// Only the fixture translates the fixed GitHub URL into a local bare remote.
	// All clone/tree/commit/push commands and approval retry logic remain real Git.
	wrapper := filepath.Join(root, "git-fixture")
	script := "#!/bin/sh\nexport GIT_ALLOW_PROTOCOL=file\nif [ \"$1\" = clone ]; then\n for arg do last=$arg; done\n exec " + strconv.Quote(binary) + " clone --bare --depth=1 --single-branch --no-tags --template= --branch main -- " + strconv.Quote("file://"+remote) + " \"$last\"\nfi\nexec " + strconv.Quote(binary) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &LocalGit{binary: wrapper}, Repository{ID: 10, InstallationID: 2, FullName: "owner/repo", DefaultBranch: "main"}, remote, run
}

func TestLocalGitClonesAndPublishesOnlyFrozenBranch(t *testing.T) {
	g, repo, remote, run := localGitFixture(t)
	ctx := context.Background()
	snapshot, err := g.Clone(ctx, "ghu_test", repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 5 {
		t.Fatal("snapshot incomplete", snapshot)
	}
	for _, f := range snapshot.Files {
		if f.Path == "binary.dat" && !bytes.Equal(f.Content, []byte{0, 1, 2, 255}) {
			t.Fatal("binary corrupted")
		}
	}
	r := reviewFixture()
	r.Repository = repo
	r.BaseCommit = snapshot.Commit
	r.BaseTree = snapshot.Tree
	r.Changes = []FileChange{{RepositoryFile: RepositoryFile{Path: "README.md", Mode: "100755", Content: []byte("after\n")}}, {RepositoryFile: RepositoryFile{Path: "remove.txt", Mode: "100755"}, Delete: true}, {RepositoryFile: RepositoryFile{Path: "new dir/file.txt", Mode: "100644", Content: []byte("new")}}}
	commit, err := g.Push(ctx, "ghu_test", r)
	if err != nil {
		t.Fatal(err)
	}
	if head := run("--git-dir="+remote, "rev-parse", "refs/heads/main"); head != snapshot.Commit {
		t.Fatalf("default branch changed: got %q want %q", head, snapshot.Commit)
	}
	if head := run("--git-dir="+remote, "rev-parse", "refs/heads/"+r.Branch); head != commit {
		t.Fatal("target branch absent")
	}
	if text := run("--git-dir="+remote, "show", commit+":README.md"); text != "after" {
		t.Fatal("wrong committed bytes")
	}
	if text := run("--git-dir="+remote, "show", commit+":keep.txt"); text != "unchanged" {
		t.Fatal("unchanged file lost")
	}
	if names := run("--git-dir="+remote, "ls-tree", "-r", "--name-only", commit); strings.Contains(names, "remove.txt") || !strings.Contains(names, "new dir/file.txt") {
		t.Fatal("wrong tree", names)
	}
	second, err := g.Push(ctx, "ghu_test", r)
	if err != nil || second != commit {
		t.Fatal("retry was not idempotent", err)
	}
	// The approved commit also recovers after the default branch moves.
	run("--git-dir="+remote, "update-ref", "refs/heads/main", commit)
	second, err = g.Push(ctx, "ghu_test", r)
	if err != nil || second != commit {
		t.Fatal("lost-result recovery failed after base advanced", err)
	}
	changed := r
	changed.Summary = "different revision"
	if _, err := g.Push(ctx, "ghu_test", changed); err == nil {
		t.Fatal("overwrote an existing branch")
	}
	if head := run("--git-dir="+remote, "rev-parse", "refs/heads/"+r.Branch); head != commit {
		t.Fatal("conflict changed remote")
	}
	newTask := r
	newTask.TaskID = strings.Repeat("b", 32)
	newTask.Branch = "codex/nudgy-" + newTask.TaskID
	if _, err := g.Push(ctx, "ghu_test", newTask); err == nil {
		t.Fatal("published against changed base")
	}
}

func TestLocalGitRejectsSymlinksWithoutFollowingThem(t *testing.T) {
	g, repo, remote, run := localGitFixture(t)
	source := filepath.Join(filepath.Dir(remote), "source")
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "outside")); err != nil {
		t.Fatal(err)
	}
	run("-C", source, "add", "outside")
	run("-C", source, "commit", "-m", "symlink")
	run("-C", source, "push", remote, "main")
	if _, err := g.Clone(context.Background(), "ghu_test", repo); err == nil {
		t.Fatal("symlink snapshot accepted")
	}
}

func TestGitProcessDoesNotInheritSecretsOrHooks(t *testing.T) {
	g, _, _, _ := localGitFixture(t)
	d, ctx, cleanup, err := g.temporary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	t.Setenv("PRIVATE_SERVER_SECRET", "must-not-leak")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.sshCommand")
	t.Setenv("GIT_CONFIG_VALUE_0", "malicious")
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\n/usr/bin/env\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d.engine = &LocalGit{binary: probe}
	out, err := d.run(ctx, "", nil, 16384, nil, "probe")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"must-not-leak", "malicious", "core.sshCommand"} {
		if bytes.Contains(out, []byte(bad)) {
			t.Fatal("unsafe inherited environment")
		}
	}
	if !bytes.Contains(out, []byte("GIT_CONFIG_KEY_0=core.hooksPath")) || !bytes.Contains(out, []byte("GIT_ALLOW_PROTOCOL=https")) {
		t.Fatal("missing git isolation")
	}
}

func TestGitTimeoutKillsTransportChildren(t *testing.T) {
	g, _, _, _ := localGitFixture(t)
	d, ctx, cleanup, err := g.temporary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	probe := filepath.Join(t.TempDir(), "slow-transport")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	d.engine = &LocalGit{binary: probe}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := d.run(ctx, "", nil, 100, nil, "probe"); err == nil {
		t.Fatal("timed out process succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("transport child survived parent cancellation")
	}
}
