package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newLogger returns a logger that writes to a buffer for inspection.
func newLogger(buf *bytes.Buffer) *log.Logger {
	return log.New(buf, "", 0)
}

// silentLogger discards all output.
func silentLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// makeSourceRepo creates a local bare git repo with one commit, suitable for
// use as a remote URL (file:///path/to/repo).
func makeSourceRepo(t *testing.T) string {
	t.Helper()
	src := t.TempDir()

	work := t.TempDir()
	mustRun(t, "git", "-C", work, "init", "-b", "main")
	mustRun(t, "git", "-C", work, "config", "user.email", "test@test.com")
	mustRun(t, "git", "-C", work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", work, "add", ".")
	mustRun(t, "git", "-C", work, "commit", "-m", "initial")
	mustRun(t, "git", "clone", "--bare", work, src)
	return src
}

// addCommitToRepo adds a new commit to a non-bare repo and pushes it to a bare remote.
func addCommitToRepo(t *testing.T, bareRemote string) {
	t.Helper()
	work := t.TempDir()
	mustRun(t, "git", "clone", bareRemote, work)
	mustRun(t, "git", "-C", work, "config", "user.email", "test@test.com")
	mustRun(t, "git", "-C", work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "file2.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", work, "add", ".")
	mustRun(t, "git", "-C", work, "commit", "-m", "second")
	mustRun(t, "git", "-C", work, "push")
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, out)
	}
}

// writeConfig writes a YAML config to a temp path and returns it.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubFetchRepos replaces fetchRepos for the duration of a test and restores it on cleanup.
func stubFetchRepos(t *testing.T, fn func(owner string, isOrg bool, token string) ([]repoInfo, error)) {
	t.Helper()
	orig := fetchRepos
	fetchRepos = fn
	t.Cleanup(func() { fetchRepos = orig })
}

// --- --create-config tests ---

func TestCreateConfig_WritesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var buf bytes.Buffer
	code := runCreateConfig(cfgPath, newLogger(&buf))

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; log: %s", code, buf.String())
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "backup-dir") {
		t.Errorf("placeholder missing backup-dir key, got:\n%s", data)
	}
	if !strings.Contains(buf.String(), "placeholder config written") {
		t.Errorf("expected success log, got: %s", buf.String())
	}
}

func TestCreateConfig_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := runCreateConfig(cfgPath, newLogger(&buf))

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	data, _ := os.ReadFile(cfgPath)
	if string(data) != "existing" {
		t.Errorf("existing config was overwritten")
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected 'already exists' log, got: %s", buf.String())
	}
}

// --- missing / invalid config tests ---

func TestMissingConfig(t *testing.T) {
	var buf bytes.Buffer
	code := runBackup("/nonexistent/path/config.yaml", newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "config file not found") {
		t.Errorf("expected 'config file not found', got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "--create-config") {
		t.Errorf("expected hint about --create-config, got: %s", buf.String())
	}
}

func TestInvalidConfig_UnknownKey(t *testing.T) {
	cfgPath := writeConfig(t, "backup-dir: /tmp/x\nfoo: bar\n")
	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "invalid config") {
		t.Errorf("expected 'invalid config', got: %s", buf.String())
	}
}

func TestInvalidConfig_EmptyBackupDir(t *testing.T) {
	cfgPath := writeConfig(t, "repositories:\n  dir:\n    repo: git@github.com:u/r.git\n")
	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "backup-dir") {
		t.Errorf("expected backup-dir error, got: %s", buf.String())
	}
}

func TestInvalidConfig_EmptyRepositories(t *testing.T) {
	cfgPath := writeConfig(t, "backup-dir: /tmp/x\n")
	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "repositories") {
		t.Errorf("expected repositories error, got: %s", buf.String())
	}
}

func TestInvalidConfig_EmptyURL(t *testing.T) {
	cfgPath := writeConfig(t, "backup-dir: /tmp/x\nrepositories:\n  dir:\n    repo: \"\"\n")
	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(buf.String(), "remote URL must not be empty") {
		t.Errorf("expected URL error, got: %s", buf.String())
	}
}

// --- backup operation tests ---

func TestBackup_ClonesNewRepo(t *testing.T) {
	src := makeSourceRepo(t)
	backupDir := t.TempDir()

	cfgPath := writeConfig(t, fmt.Sprintf(
		"backup-dir: %s\nrepositories:\n  personal:\n    my-repo: file://%s\n",
		backupDir, src,
	))

	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; log:\n%s", code, buf.String())
	}

	destPath := filepath.Join(backupDir, "personal", "my-repo.git")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Errorf("bare repo not created at %s", destPath)
	}
	if _, err := os.Stat(filepath.Join(destPath, "HEAD")); os.IsNotExist(err) {
		t.Errorf("cloned directory does not look like a bare repo (no HEAD)")
	}
	if !strings.Contains(buf.String(), "cloning personal/my-repo") {
		t.Errorf("expected 'cloning' log, got: %s", buf.String())
	}
}

func TestBackup_UpdatesExistingRepo(t *testing.T) {
	src := makeSourceRepo(t)
	backupDir := t.TempDir()

	url := "file://" + src
	cfgPath := writeConfig(t, fmt.Sprintf(
		"backup-dir: %s\nrepositories:\n  personal:\n    my-repo: %s\n",
		backupDir, url,
	))

	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	if code := runBackup(cfgPath, silentLogger()); code != 0 {
		t.Fatalf("first run (clone) failed")
	}

	addCommitToRepo(t, src)

	var buf bytes.Buffer
	if code := runBackup(cfgPath, newLogger(&buf)); code != 0 {
		t.Fatalf("second run (update) failed; log:\n%s", buf.String())
	}

	if !strings.Contains(buf.String(), "updating personal/my-repo") {
		t.Errorf("expected 'updating' log, got: %s", buf.String())
	}

	out, err := exec.Command("git", "-C", filepath.Join(backupDir, "personal", "my-repo.git"), "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(out), "second") {
		t.Errorf("mirror does not contain the new commit; git log:\n%s", out)
	}
}

func TestBackup_NonFatalRepoError(t *testing.T) {
	src := makeSourceRepo(t)
	backupDir := t.TempDir()

	cfgPath := writeConfig(t, fmt.Sprintf(`
backup-dir: %s
repositories:
  personal:
    good-repo: file://%s
    bad-repo: file:///nonexistent/path/does-not-exist
`, backupDir, src))

	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	destPath := filepath.Join(backupDir, "personal", "good-repo.git")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Errorf("good-repo was not cloned despite bad-repo failure")
	}
	if !strings.Contains(buf.String(), "error cloning personal/bad-repo") {
		t.Errorf("expected error log for bad-repo, got: %s", buf.String())
	}
}

func TestBackup_CreatesMissingBackupDir(t *testing.T) {
	src := makeSourceRepo(t)
	backupDir := filepath.Join(t.TempDir(), "nested", "backup")

	cfgPath := writeConfig(t, fmt.Sprintf(
		"backup-dir: %s\nrepositories:\n  dir:\n    repo: file://%s\n",
		backupDir, src,
	))

	orig := gitExec
	gitExec = defaultGitExec
	t.Cleanup(func() { gitExec = orig })

	var buf bytes.Buffer
	code := runBackup(cfgPath, newLogger(&buf))

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; log:\n%s", code, buf.String())
	}
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		t.Errorf("backup directory was not created")
	}
	if !strings.Contains(buf.String(), "creating backup directory") {
		t.Errorf("expected 'creating backup directory' log, got: %s", buf.String())
	}
}

// --- scan tests ---

func TestScan_NoScanSection(t *testing.T) {
	cfgPath := writeConfig(t, "backup-dir: /tmp/x\nrepositories:\n  dir:\n    repo: git@github.com:u/r.git\n")

	var logBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), io.Discard)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(logBuf.String(), "no scan targets configured") {
		t.Errorf("expected 'no scan targets configured', got: %s", logBuf.String())
	}
}

func TestScan_EmptyScanLists(t *testing.T) {
	cfgPath := writeConfig(t, "backup-dir: /tmp/x\nrepositories:\n  dir:\n    repo: git@github.com:u/r.git\nscan:\n  users: []\n  orgs: []\n")

	var logBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), io.Discard)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(logBuf.String(), "no scan targets configured") {
		t.Errorf("expected 'no scan targets configured', got: %s", logBuf.String())
	}
}

func TestScan_ListsUnmanagedRepos(t *testing.T) {
	stubFetchRepos(t, func(owner string, isOrg bool, token string) ([]repoInfo, error) {
		switch owner {
		case "alice":
			return []repoInfo{
				{Name: "managed-repo", SSHURL: "git@github.com:alice/managed-repo.git"},
				{Name: "unmanaged-repo", SSHURL: "git@github.com:alice/unmanaged-repo.git"},
			}, nil
		case "my-org":
			return []repoInfo{
				{Name: "org-repo", SSHURL: "git@github.com:my-org/org-repo.git"},
			}, nil
		}
		return nil, nil
	})

	cfgPath := writeConfig(t, `
backup-dir: /tmp/x
repositories:
  personal:
    managed-repo: git@github.com:alice/managed-repo.git
scan:
  users:
    - alice
  orgs:
    - my-org
`)

	var logBuf, outBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), &outBuf)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; log: %s", code, logBuf.String())
	}
	out := outBuf.String()
	if !strings.Contains(out, "alice/unmanaged-repo") {
		t.Errorf("expected unmanaged-repo in output, got:\n%s", out)
	}
	if !strings.Contains(out, "my-org/org-repo") {
		t.Errorf("expected org-repo in output, got:\n%s", out)
	}
	if strings.Contains(out, "alice/managed-repo") {
		t.Errorf("alice/managed-repo should not appear in output, got:\n%s", out)
	}
}

func TestScan_AllReposManaged(t *testing.T) {
	stubFetchRepos(t, func(owner string, isOrg bool, token string) ([]repoInfo, error) {
		return []repoInfo{
			{Name: "repo-a", SSHURL: "git@github.com:alice/repo-a.git"},
		}, nil
	})

	cfgPath := writeConfig(t, `
backup-dir: /tmp/x
repositories:
  personal:
    repo-a: git@github.com:alice/repo-a.git
scan:
  users:
    - alice
`)

	var logBuf, outBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), &outBuf)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if outBuf.Len() != 0 {
		t.Errorf("expected no output for fully managed repos, got: %s", outBuf.String())
	}
	if !strings.Contains(logBuf.String(), "all repos are managed") {
		t.Errorf("expected 'all repos are managed' log, got: %s", logBuf.String())
	}
}

func TestScan_FetchError(t *testing.T) {
	stubFetchRepos(t, func(owner string, isOrg bool, token string) ([]repoInfo, error) {
		return nil, errors.New("network failure")
	})

	cfgPath := writeConfig(t, `
backup-dir: /tmp/x
repositories:
  personal:
    repo-a: git@github.com:alice/repo-a.git
scan:
  users:
    - alice
`)

	var logBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), io.Discard)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(logBuf.String(), "network failure") {
		t.Errorf("expected error message in log, got: %s", logBuf.String())
	}
}

func TestScan_ContinuesAfterFetchError(t *testing.T) {
	stubFetchRepos(t, func(owner string, isOrg bool, token string) ([]repoInfo, error) {
		if owner == "bad-user" {
			return nil, errors.New("network failure")
		}
		return []repoInfo{
			{Name: "new-repo", SSHURL: "git@github.com:good-user/new-repo.git"},
		}, nil
	})

	cfgPath := writeConfig(t, `
backup-dir: /tmp/x
repositories: {}
scan:
  users:
    - bad-user
    - good-user
`)

	var logBuf, outBuf bytes.Buffer
	code := runScan(cfgPath, newLogger(&logBuf), &outBuf)

	if code != 1 {
		t.Fatalf("expected exit 1 due to fetch error, got %d", code)
	}
	if !strings.Contains(outBuf.String(), "good-user/new-repo") {
		t.Errorf("expected good-user repos in output despite bad-user failure, got:\n%s", outBuf.String())
	}
}

func TestScan_MissingConfig(t *testing.T) {
	var logBuf bytes.Buffer
	code := runScan("/nonexistent/path/config.yaml", newLogger(&logBuf), io.Discard)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(logBuf.String(), "config file not found") {
		t.Errorf("expected 'config file not found', got: %s", logBuf.String())
	}
}
