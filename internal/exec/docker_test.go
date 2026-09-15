package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// paneCreatingFake returns a fake runner whose `herdr workspace create` reports
// the given root pane, the only scripted response Spawn needs to reach the launch.
func paneCreatingFake(pane string) *proc.Fake {
	return &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
		if c.Name == "herdr" && len(c.Args) >= 2 && c.Args[0] == "workspace" && c.Args[1] == "create" {
			return []byte(fmt.Sprintf(`{"result":{"root_pane":{"pane_id":%q}}}`, pane)), nil
		}
		return nil, nil
	}}
}

// hostGitHubAnswer scripts the host GitHub commands a container spawn runs (the
// gh token and login, git's global identity), so docker backend tests reach the
// launch as a host that is logged in to gh. ok is false for any other command.
func hostGitHubAnswer(c proc.Call) (out []byte, ok bool) {
	switch c.Name + " " + strings.Join(c.Args, " ") {
	case "gh auth token":
		return []byte("gho_fakeTOKEN123\n"), true
	case "gh config get -h github.com user":
		return []byte("octo-operator\n"), true
	case "git config --global user.name":
		return []byte("Octo Operator\n"), true
	case "git config --global user.email":
		return []byte("octo@example.com\n"), true
	}
	return nil, false
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
	for _, p := range []string{".gitconfig", filepath.Join(".config", "gh", "hosts.yml")} {
		if _, err := os.Stat(filepath.Join(taskDir, p)); err != nil {
			t.Errorf("GitHub credentials %s not provisioned in the per-task home: %v", p, err)
		}
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
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
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

func TestDockerBackend_SpawnWithoutGitHubTokenFailsAndDiscards(t *testing.T) {
	f := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if c.Name == "gh" && len(c.Args) >= 2 && c.Args[0] == "auth" && c.Args[1] == "token" {
			return nil, errors.New("exit status 1: no oauth token found")
		}
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
		return nil, nil
	}}
	credsRoot := t.TempDir()
	d := testDockerBackend(t, f, credsRoot)

	if _, err := d.Spawn(context.Background(), testSpawn()); err == nil {
		t.Fatal("Spawn = nil error, want the missing GitHub token")
	}
	if _, err := os.Stat(filepath.Join(credsRoot, "issue-5")); !os.IsNotExist(err) {
		t.Errorf("per-task home left on disk after a failed GitHub provision (stat err = %v)", err)
	}
	for _, c := range f.Snapshot() {
		if c.Name == "herdr" && len(c.Args) >= 2 && c.Args[0] == "workspace" && c.Args[1] == "create" {
			t.Error("spawn created a herdr workspace despite having no GitHub token")
		}
	}
}

// reapFake is a logged-in host whose herdr has workspace w1 (issue-1) with a
// live pane and workspace w2 (issue-2) with none; no other task has a workspace.
// When gate is non-nil, the first `herdr workspace create` signals creating and
// blocks until gate is closed, holding a spawn open mid-flight.
func reapFake(gate <-chan struct{}, creating chan<- struct{}) *proc.Fake {
	var once sync.Once
	return &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
		if c.Name != "herdr" || len(c.Args) < 2 {
			return nil, nil
		}
		switch c.Args[0] + " " + c.Args[1] {
		case "workspace create":
			if gate != nil {
				once.Do(func() { close(creating) })
				<-gate
			}
			return []byte(`{"result":{"root_pane":{"pane_id":"w5:p1"}}}`), nil
		case "workspace list":
			return []byte(`{"result":{"workspaces":[{"workspace_id":"w1","label":"issue-1"},{"workspace_id":"w2","label":"issue-2"}]}}`), nil
		case "pane list":
			return []byte(`{"result":{"panes":[{"pane_id":"w1:p1","workspace_id":"w1","cwd":"/home/u/wt-issue-1"}]}}`), nil
		}
		return nil, nil
	}}
}

func TestDockerBackend_ReapClosedPanes(t *testing.T) {
	credsRoot := t.TempDir()
	for _, id := range []string{"issue-1", "issue-2"} {
		if err := os.MkdirAll(filepath.Join(credsRoot, id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stray := filepath.Join(credsRoot, "notes.txt")
	if err := os.WriteFile(stray, []byte("not a task"), 0o600); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	creating := make(chan struct{})
	d := testDockerBackend(t, reapFake(gate, creating), credsRoot)
	ctx := context.Background()

	spawned := make(chan error, 1)
	go func() {
		_, err := d.Spawn(ctx, testSpawn())
		spawned <- err
	}()
	select {
	case <-creating:
	case err := <-spawned:
		close(gate)
		t.Fatalf("spawn finished before reaching workspace create: %v", err)
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("spawn never reached workspace create")
	}

	reapErr := d.ReapClosedPanes(ctx)
	close(gate)
	if err := <-spawned; err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if reapErr != nil {
		t.Fatalf("ReapClosedPanes: %v", reapErr)
	}

	for id, wantKept := range map[string]bool{"issue-1": true, "issue-2": false, "issue-5": true} {
		_, err := os.Stat(filepath.Join(credsRoot, id))
		if kept := err == nil; kept != wantKept {
			t.Errorf("%s kept = %v, want %v (stat err = %v)", id, kept, wantKept, err)
		}
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("stray file in the creds root was touched: %v", err)
	}
}

func TestDockerBackend_WatchPaneClosuresReapsUntilCancelled(t *testing.T) {
	credsRoot := t.TempDir()
	closed := filepath.Join(credsRoot, "issue-2")
	if err := os.MkdirAll(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	d := testDockerBackend(t, reapFake(nil, nil), credsRoot)
	ctx, cancel := context.WithCancel(context.Background())
	var reportedErrors atomic.Int32
	done := make(chan struct{})
	go func() {
		d.WatchPaneClosures(ctx, 10*time.Millisecond, func(error) { reportedErrors.Add(1) })
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(closed); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the closed pane's credentials were never reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchPaneClosures did not return after its context was cancelled")
	}
	if n := reportedErrors.Load(); n != 0 {
		t.Errorf("reported %d errors from healthy passes, want 0", n)
	}
}

func TestDockerBackend_WatchPaneClosuresReportsErrorsAndKeepsGoing(t *testing.T) {
	credsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(credsRoot, "issue-9"), 0o700); err != nil {
		t.Fatal(err)
	}
	failing := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if c.Name == "herdr" && len(c.Args) >= 2 && c.Args[0] == "workspace" && c.Args[1] == "list" {
			return nil, errors.New("herdr server unreachable")
		}
		return nil, nil
	}}
	d := testDockerBackend(t, failing, credsRoot)
	ctx, cancel := context.WithCancel(context.Background())
	var reportedErrors atomic.Int32
	done := make(chan struct{})
	go func() {
		d.WatchPaneClosures(ctx, 10*time.Millisecond, func(error) { reportedErrors.Add(1) })
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for reportedErrors.Load() < 2 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("reported %d errors, want the loop to keep reporting failed passes", reportedErrors.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if _, err := os.Stat(filepath.Join(credsRoot, "issue-9")); err != nil {
		t.Errorf("a pass that could not reach herdr must not discard credentials: %v", err)
	}
}

func TestDockerBackend_ReapClosedPanesWithoutCredsRoot(t *testing.T) {
	d := testDockerBackend(t, reapFake(nil, nil), filepath.Join(t.TempDir(), "never-created"))
	if err := d.ReapClosedPanes(context.Background()); err != nil {
		t.Errorf("ReapClosedPanes with no creds root = %v, want nil", err)
	}
}

func TestNewDocker_RequiresAgentConfigDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	if _, err := NewDocker(&proc.Fake{}, "hounds-doer:latest"); err == nil {
		t.Fatal("NewDocker with CLAUDE_CONFIG_DIR unset = nil error, want a clear configuration error")
	}
}

// paneLifeFake is a logged-in host whose herdr has a workspace labelled issue-5;
// panesJSON is what `herdr pane list` reports, so a test decides whether that
// workspace still has a live pane.
func paneLifeFake(panesJSON string) *proc.Fake {
	return &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
		if c.Name != "herdr" || len(c.Args) < 2 {
			return nil, nil
		}
		switch c.Args[0] + " " + c.Args[1] {
		case "workspace create":
			return []byte(`{"result":{"root_pane":{"pane_id":"w7:p1"}}}`), nil
		case "workspace list":
			return []byte(`{"result":{"workspaces":[{"workspace_id":"w7","label":"issue-5"}]}}`), nil
		case "pane list":
			return []byte(panesJSON), nil
		}
		return nil, nil
	}}
}

func TestDockerBackend_ReleaseFollowsPaneLifetime(t *testing.T) {
	tests := []struct {
		name      string
		panesJSON string
		wantKept  bool
	}{
		{
			name:      "pane still open keeps credentials",
			panesJSON: `{"result":{"panes":[{"pane_id":"w7:p1","workspace_id":"w7","cwd":"/home/u/wt-issue-5"}]}}`,
			wantKept:  true,
		},
		{
			name:      "pane closed discards credentials",
			panesJSON: `{"result":{"panes":[]}}`,
			wantKept:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := paneLifeFake(tc.panesJSON)
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

			if err := d.Release(ctx, "issue-5"); err != nil {
				t.Fatalf("Release: %v", err)
			}
			_, statErr := os.Stat(taskDir)
			if kept := statErr == nil; kept != tc.wantKept {
				t.Errorf("credential copy kept = %v after Release, want %v (stat err = %v)", kept, tc.wantKept, statErr)
			}
			if err := d.Release(ctx, "issue-5"); err != nil {
				t.Errorf("second Release = %v, want nil", err)
			}
		})
	}
}

func TestDockerBackend_CleanupDiscardsCredentialsEvenWithNoWorkspace(t *testing.T) {
	f := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		if out, ok := hostGitHubAnswer(c); ok {
			return out, nil
		}
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
