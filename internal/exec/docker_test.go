package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// paneCreatingFake returns a fake runner whose `herdr workspace create` reports
// the given root pane, the only scripted response Spawn needs to reach the launch.
func paneCreatingFake(pane string) *proc.Fake {
	return &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if c.Name == "herdr" && len(c.Args) >= 2 && c.Args[0] == "workspace" && c.Args[1] == "create" {
			return []byte(fmt.Sprintf(`{"result":{"root_pane":{"pane_id":%q}}}`, pane)), nil
		}
		return nil, nil
	}}
}

// testDockerBackend builds a DockerBackend over f with a fixed host identity and
// agent binary, provisioning credentials from a temp host config into credsRoot.
func testDockerBackend(t *testing.T, f *proc.Fake, credsRoot string) *DockerBackend {
	t.Helper()
	d := newDockerBackend(f, dockerDeps{
		uid:           502,
		gid:           20,
		resolveAgent:  func(string) (string, error) { return "/opt/claude/versions/2.1.272", nil },
		hostConfigDir: writeHostConfig(t, map[string]string{".credentials.json": fakeCredentials}),
		credsRoot:     credsRoot,
		image:         "hounds-doer:latest",
	})
	d.SubmitDelay = 0
	d.KickoffAckTimeout = 0
	return d
}

func TestDockerBackend_SpawnLaunchesAgentInContainer(t *testing.T) {
	f := paneCreatingFake("w7:p1")
	credsRoot := t.TempDir()
	d := testDockerBackend(t, f, credsRoot)

	if _, err := d.Spawn(context.Background(), testSpawn()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	taskDir := filepath.Join(credsRoot, "issue-5")
	want := "HERDR_AGENT=claude docker run -it --rm -u 502:20" +
		" -v /opt/claude/versions/2.1.272:/usr/local/bin/claude:ro" +
		" -v " + taskDir + ":/hounds/agent" +
		" -v /home/u/wt-issue-5:/home/u/wt-issue-5" +
		" -v /home/u/repo/.git:/home/u/repo/.git" +
		" -w /home/u/wt-issue-5" +
		" -e HOME=/hounds/agent -e CLAUDE_CONFIG_DIR=/hounds/agent -e CLAUDE_CODE_TMPDIR=/hounds/agent/tmp" +
		" hounds-doer:latest claude"
	runs := paneRunCalls(f.Snapshot())
	if len(runs) != 1 {
		t.Fatalf("want 1 pane run (the launch), got %d:\n%s", len(runs), formatCalls(runs))
	}
	hasExactCall(t, runs, "herdr", "pane", "run", "w7:p1", want)

	raw, err := os.ReadFile(filepath.Join(taskDir, ".claude.json"))
	if err != nil {
		t.Fatalf("per-task agent config not provisioned: %v", err)
	}
	var cfg struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode per-task agent config: %v", err)
	}
	if cfg.Projects["/home/u/wt-issue-5"]["hasTrustDialogAccepted"] != true {
		t.Errorf("trust entry must be keyed on the container workdir /home/u/wt-issue-5, got %s", raw)
	}
	if info, err := os.Stat(filepath.Join(taskDir, "tmp")); err != nil || !info.IsDir() {
		t.Errorf("agent tmp dir not created under the per-task dir (err = %v)", err)
	}
}

func TestDockerBackend_SpawnShellQuotesPathsWithSpaces(t *testing.T) {
	f := paneCreatingFake("w7:p1")
	credsRoot := t.TempDir()
	d := testDockerBackend(t, f, credsRoot)
	s := testSpawn()
	s.RepoDir = "/home/u/my repo"

	if _, err := d.Spawn(context.Background(), s); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	taskDir := filepath.Join(credsRoot, "issue-5")
	want := "HERDR_AGENT=claude docker run -it --rm -u 502:20" +
		" -v /opt/claude/versions/2.1.272:/usr/local/bin/claude:ro" +
		" -v " + taskDir + ":/hounds/agent" +
		" -v /home/u/wt-issue-5:/home/u/wt-issue-5" +
		" -v '/home/u/my repo/.git:/home/u/my repo/.git'" +
		" -w /home/u/wt-issue-5" +
		" -e HOME=/hounds/agent -e CLAUDE_CONFIG_DIR=/hounds/agent -e CLAUDE_CODE_TMPDIR=/hounds/agent/tmp" +
		" hounds-doer:latest claude"
	hasExactCall(t, paneRunCalls(f.Snapshot()), "herdr", "pane", "run", "w7:p1", want)
}

func TestDockerBackend_SpawnFailureDiscardsCredentials(t *testing.T) {
	f := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if c.Name == "git" && slices.Contains(c.Args, "worktree") && slices.Contains(c.Args, "add") {
			return nil, errors.New("fatal: could not create worktree")
		}
		return nil, nil
	}}
	credsRoot := t.TempDir()
	d := testDockerBackend(t, f, credsRoot)

	if _, err := d.Spawn(context.Background(), testSpawn()); err == nil {
		t.Fatal("Spawn = nil error, want the worktree failure")
	}
	if _, err := os.Stat(filepath.Join(credsRoot, "issue-5")); !os.IsNotExist(err) {
		t.Errorf("credential copy left on disk after a failed spawn (stat err = %v)", err)
	}
}

func TestNewDocker_DefaultsFromHostEnvironment(t *testing.T) {
	bin := t.TempDir()
	agentBin := filepath.Join(bin, "versions", "9.9.9")
	if err := os.MkdirAll(filepath.Dir(agentBin), 0o755); err != nil {
		t.Fatalf("mkdir agent versions: %v", err)
	}
	if err := os.WriteFile(agentBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake agent binary: %v", err)
	}
	if err := os.Symlink(agentBin, filepath.Join(bin, "claude")); err != nil {
		t.Fatalf("symlink claude: %v", err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", writeHostConfig(t, map[string]string{".credentials.json": fakeCredentials}))

	f := paneCreatingFake("w7:p1")
	d, err := NewDocker(f, "ghcr.io/acme/doer:1.2")
	if err != nil {
		t.Fatalf("NewDocker: %v", err)
	}
	d.SubmitDelay = 0
	d.KickoffAckTimeout = 0
	if _, err := d.Spawn(context.Background(), testSpawn()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("user cache dir: %v", err)
	}
	taskDir := filepath.Join(cache, "hounds", "doer-creds", "issue-5")
	realAgent, err := filepath.EvalSymlinks(agentBin)
	if err != nil {
		t.Fatalf("resolve fixture binary: %v", err)
	}
	want := fmt.Sprintf("HERDR_AGENT=claude docker run -it --rm -u %d:%d", os.Getuid(), os.Getgid()) +
		" -v " + realAgent + ":/usr/local/bin/claude:ro" +
		" -v " + taskDir + ":/hounds/agent" +
		" -v /home/u/wt-issue-5:/home/u/wt-issue-5" +
		" -v /home/u/repo/.git:/home/u/repo/.git" +
		" -w /home/u/wt-issue-5" +
		" -e HOME=/hounds/agent -e CLAUDE_CONFIG_DIR=/hounds/agent -e CLAUDE_CODE_TMPDIR=/hounds/agent/tmp" +
		" ghcr.io/acme/doer:1.2 claude"
	hasExactCall(t, paneRunCalls(f.Snapshot()), "herdr", "pane", "run", "w7:p1", want)
	if _, err := os.Stat(filepath.Join(taskDir, ".credentials.json")); err != nil {
		t.Errorf("credentials not copied from CLAUDE_CONFIG_DIR into the cache-dir root: %v", err)
	}
}

func TestNewDocker_RequiresAgentConfigDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	if _, err := NewDocker(&proc.Fake{}, "hounds-doer:latest"); err == nil {
		t.Fatal("NewDocker with CLAUDE_CONFIG_DIR unset = nil error, want a clear configuration error")
	}
}

func TestDockerBackend_CleanupDiscardsCredentialsEvenWithNoWorkspace(t *testing.T) {
	f := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if c.Name == "herdr" && len(c.Args) >= 2 && c.Args[0] == "workspace" {
			switch c.Args[1] {
			case "create":
				return []byte(`{"result":{"root_pane":{"pane_id":"w7:p1"}}}`), nil
			case "list":
				return []byte(`{"result":{"workspaces":[]}}`), nil
			}
		}
		return nil, nil
	}}
	credsRoot := t.TempDir()
	d := testDockerBackend(t, f, credsRoot)
	ctx := context.Background()

	if _, err := d.Spawn(ctx, testSpawn()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	taskDir := filepath.Join(credsRoot, "issue-5")
	if _, err := os.Stat(taskDir); err != nil {
		t.Fatalf("precondition: per-task dir missing after Spawn: %v", err)
	}

	if err := d.Cleanup(ctx, "issue-5"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Errorf("credential copy still on disk after Cleanup (stat err = %v)", err)
	}
}
