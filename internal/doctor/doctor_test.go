package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean1588/herdr-orchestrator/internal/config"
	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

const testConfig = `
version: 0
name: test-pipeline
entry_state: intake
policies:
  max_concurrent_tasks: 1
sources:
  - id: gh_issues
    type: github_issues
    repo: owner/name
    select: { label: agent-ready }
    emits_to: intake
roles:
  implementer:
    launch: ["claude"]
  reviewer:
    launch: ["claude"]
gates:
  pr_exists: { type: github_pr, head: "{branch}" }
states:
  intake:
    transitions:
      - when: { event: scheduled }
        to: implementing
  implementing:
    entry: { spawn: implementer }
    transitions:
      - when: { event: agent.done }
        evaluate: { gate: pr_exists }
        branch: { pass: merged, fail: escalated }
      - when: { timeout: 45m }
        to: escalated
  merged: { terminal: success }
  escalated: { terminal: needs_human, alert: true }
`

type fakeSmoker struct {
	err    error
	calls  int
	launch []string
}

func (f *fakeSmoker) SmokeKickoff(ctx context.Context, dir string, launch []string, kickoff string) error {
	f.calls++
	f.launch = launch
	return f.err
}

// healthyEnv is an environment where every check passes: every command the
// checks run succeeds, every binary resolves, the base is current.
func healthyEnv(t *testing.T) (Env, *proc.Fake) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(cfgPath, []byte(testConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, _, err := config.Parse([]byte(testConfig))
	if err != nil {
		t.Fatalf("fixture config must be valid: %v", err)
	}

	f := &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		switch {
		case c.Name == "herdr" && c.Args[0] == "--version":
			return []byte("herdr 0.8.2\n"), nil
		case c.Name == "gh" && c.Args[0] == "label":
			return []byte(`[{"name":"agent-ready"},{"name":"bug"}]`), nil
		case c.Name == "git" && c.Args[0] == "rev-list":
			return []byte("0\n"), nil
		}
		return nil, nil
	}}

	return Env{
		Runner:       f,
		Smoker:       &fakeSmoker{},
		ConfigPath:   cfgPath,
		ConfigSource: []byte(testConfig),
		Workflow:     wf,
		RepoDir:      dir,
		RepoSlug:     "owner/name",
		Base:         "main",
		Label:        "agent-ready",
		WorktreesDir: filepath.Join(dir, "wt"),
		DBPath:       filepath.Join(dir, "test.db"),
		TempDir:      dir,
		Getenv:       func(string) string { return "" },
		LookPath:     func(c string) (string, error) { return "/usr/local/bin/" + c, nil },
	}, f
}

func byName(results []Result, name string) Result {
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	return Result{Name: name, Status: "missing"}
}

func TestRun_HealthyEnvironmentPassesEverything(t *testing.T) {
	env, _ := healthyEnv(t)
	results := Run(context.Background(), env, true)

	if len(results) != len(Checks()) {
		t.Fatalf("got %d results for %d checks", len(results), len(Checks()))
	}
	containerOnly := map[string]bool{"docker-daemon": true, "doer-image": true, "agent-config-dir": true}
	for _, r := range results {
		if containerOnly[r.Name] {
			if r.Status != StatusSkip {
				t.Errorf("container check %q = %s (%s) on the herdr backend, want skip", r.Name, r.Status, r.Detail)
			}
			continue
		}
		if r.Status != StatusPass {
			t.Errorf("check %q = %s (%s)", r.Name, r.Status, r.Detail)
		}
	}
	if Failed(results) {
		t.Error("a healthy environment must not report failure")
	}
}

func TestRun_HealthyContainerEnvironmentPassesEverything(t *testing.T) {
	login := t.TempDir()
	if err := os.WriteFile(filepath.Join(login, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _ := containerEnv(t, login, "")
	results := Run(context.Background(), env, true)

	if len(results) != len(Checks()) {
		t.Fatalf("got %d results for %d checks", len(results), len(Checks()))
	}
	for _, r := range results {
		if r.Status != StatusPass {
			t.Errorf("check %q = %s (%s)", r.Name, r.Status, r.Detail)
		}
	}
}

// Every non-passing result has to carry a fix. A preflight that reports a
// symptom without a remedy is just a slower way to be confused.
func TestEveryNonPassingResultCarriesAFix(t *testing.T) {
	env, f := healthyEnv(t)
	f.Responder = func(c proc.Call) ([]byte, error) { return nil, errors.New("boom") }
	env.LookPath = func(c string) (string, error) { return "", errors.New("not found") }
	env.Getenv = func(string) string { return "tok" }
	env.DBPath = filepath.Join(t.TempDir(), "nonexistent-dir", "x.db")

	for _, r := range Run(context.Background(), env, true) {
		if (r.Status == StatusWarn || r.Status == StatusFail) && r.Fix == "" {
			t.Errorf("check %q is %s with no fix: %s", r.Name, r.Status, r.Detail)
		}
	}
}

func TestChecks_FailureModes(t *testing.T) {
	tests := []struct {
		name       string
		check      string
		mutate     func(*Env, *proc.Fake)
		wantStatus Status
		wantDetail string
	}{
		{
			name:  "gh not authenticated",
			check: "gh-auth",
			mutate: func(e *Env, f *proc.Fake) {
				f.Responder = func(c proc.Call) ([]byte, error) {
					if c.Name == "gh" && c.Args[0] == "auth" {
						return nil, errors.New("not logged in")
					}
					return nil, nil
				}
			},
			wantStatus: StatusFail,
			wantDetail: "not authenticated",
		},
		{
			name:       "a token in the environment is a warning, not a failure",
			check:      "gh-token-env",
			mutate:     func(e *Env, f *proc.Fake) { e.Getenv = func(v string) string { return "ghp_secret" } },
			wantStatus: StatusWarn,
			wantDetail: "GITHUB_TOKEN",
		},
		{
			name:  "base branch behind origin",
			check: "repo-base-current",
			mutate: func(e *Env, f *proc.Fake) {
				f.Responder = func(c proc.Call) ([]byte, error) {
					if c.Name == "git" && c.Args[0] == "rev-list" {
						return []byte("7\n"), nil
					}
					return nil, nil
				}
			},
			wantStatus: StatusWarn,
			wantDetail: "7 commit(s) behind",
		},
		{
			name:  "source label missing",
			check: "gh-label",
			mutate: func(e *Env, f *proc.Fake) {
				f.Responder = func(c proc.Call) ([]byte, error) {
					if c.Name == "gh" && c.Args[0] == "label" {
						return []byte(`[{"name":"bug"}]`), nil
					}
					return nil, nil
				}
			},
			wantStatus: StatusWarn,
			wantDetail: "does not exist",
		},
		{
			name:  "herdr server unreachable",
			check: "herdr-server",
			mutate: func(e *Env, f *proc.Fake) {
				f.Responder = func(c proc.Call) ([]byte, error) {
					if c.Name == "herdr" && c.Args[0] == "pane" {
						return nil, errors.New("connection refused")
					}
					return []byte("herdr 0.8.2"), nil
				}
			},
			wantStatus: StatusFail,
			wantDetail: "cannot reach the herdr server",
		},
		{
			name:  "agent binary missing",
			check: "agent-binary",
			mutate: func(e *Env, f *proc.Fake) {
				e.LookPath = func(c string) (string, error) {
					if c == "claude" {
						return "", errors.New("not found")
					}
					return "/usr/local/bin/" + c, nil
				}
			},
			wantStatus: StatusFail,
			wantDetail: "not on PATH: claude",
		},
		{
			name:       "kickoff not accepted",
			check:      "kickoff-delivery",
			mutate:     func(e *Env, f *proc.Fake) { e.Smoker = &fakeSmoker{err: errors.New("agent still idle")} },
			wantStatus: StatusFail,
			wantDetail: "did not accept a kickoff",
		},
		{
			name:  "unparseable config",
			check: "config",
			mutate: func(e *Env, f *proc.Fake) {
				e.ConfigSource = []byte("version: 0\nname: broken\nstates: {}\n")
			},
			wantStatus: StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, f := healthyEnv(t)
			tc.mutate(&env, f)
			got := byName(Run(context.Background(), env, true), tc.check)
			if got.Status != tc.wantStatus {
				t.Fatalf("check %q = %s (%s), want %s", tc.check, got.Status, got.Detail, tc.wantStatus)
			}
			if tc.wantDetail != "" && !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, tc.wantDetail)
			}
		})
	}
}

// The token check must never echo the value it found — doctor output gets pasted
// into issues and chat logs.
func TestGHTokenEnvNeverPrintsTheValue(t *testing.T) {
	const secret = "ghp_thisisasecretvalue"
	env, _ := healthyEnv(t)
	env.Getenv = func(v string) string {
		if v == "GITHUB_TOKEN" {
			return secret
		}
		return ""
	}
	r := byName(Run(context.Background(), env, true), "gh-token-env")
	if strings.Contains(r.Detail+r.Fix, secret) {
		t.Fatalf("the token value leaked into the report: %+v", r)
	}
}

// Starting the daemon must never launch an agent, so the expensive check is
// skipped — and skipped visibly, not silently dropped from the report.
func TestQuickModeSkipsTheExpensiveCheckWithoutRunningIt(t *testing.T) {
	env, _ := healthyEnv(t)
	smoker := &fakeSmoker{}
	env.Smoker = smoker

	results := Run(context.Background(), env, false)
	r := byName(results, "kickoff-delivery")
	if r.Status != StatusSkip {
		t.Errorf("kickoff-delivery = %s, want skip", r.Status)
	}
	if smoker.calls != 0 {
		t.Errorf("quick mode launched an agent %d time(s)", smoker.calls)
	}
	if len(results) != len(Checks()) {
		t.Errorf("skipped checks must still appear in the report: %d of %d", len(results), len(Checks()))
	}
	if Failed(results) {
		t.Error("a skipped check must not fail the run")
	}
}

// Warnings are things worth saying, not reasons to refuse to start.
func TestFailedIgnoresWarnings(t *testing.T) {
	if Failed([]Result{{Status: StatusWarn}, {Status: StatusPass}, {Status: StatusSkip}}) {
		t.Error("warnings and skips must not count as failure")
	}
	if !Failed([]Result{{Status: StatusPass}, {Status: StatusFail}}) {
		t.Error("a failure must be reported")
	}
}

// The smoke test must exercise a role's real launch argv, picked deterministically
// (map iteration is not) so the report does not vary run to run.
func TestKickoffSmokeUsesADeterministicRoleLaunch(t *testing.T) {
	wf := &config.Workflow{Roles: map[string]config.Role{
		"zulu":        {Launch: []string{"zulu-agent"}},
		"implementer": {Launch: []string{"claude", "--flag"}},
		"reviewer":    {Launch: []string{"reviewer-agent"}},
	}}
	for i := 0; i < 20; i++ {
		got := firstLaunch(wf)
		if fmt.Sprint(got) != fmt.Sprint([]string{"claude", "--flag"}) {
			t.Fatalf("firstLaunch = %v on iteration %d, want the alphabetically-first role's argv", got, i)
		}
	}
}

// containerEnv is healthyEnv switched to the container backend, with
// CLAUDE_CONFIG_DIR reporting configDir and the docker subcommand named by
// failDocker (e.g. "info", "image") failing; every other command succeeds.
func containerEnv(t *testing.T, configDir, failDocker string) (Env, *proc.Fake) {
	t.Helper()
	env, f := healthyEnv(t)
	env.Workflow.Policies.Execution.Backend = "container"
	env.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return configDir
		}
		return ""
	}
	base := f.Responder
	f.Responder = func(c proc.Call) ([]byte, error) {
		if c.Name == "docker" && len(c.Args) > 0 && c.Args[0] == failDocker {
			return nil, errors.New("docker: simulated failure")
		}
		return base(c)
	}
	return env, f
}

func TestContainerPreflightChecks(t *testing.T) {
	login := t.TempDir()
	if err := os.WriteFile(filepath.Join(login, ".credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	noLogin := t.TempDir()
	containerChecks := []string{"docker-daemon", "doer-image", "agent-config-dir"}

	tests := []struct {
		name     string
		env      func(t *testing.T) (Env, *proc.Fake)
		want     map[string]Status
		wantFix  map[string]string
		wantCall string
	}{
		{
			name: "herdr backend skips container checks",
			env:  healthyEnv,
			want: map[string]Status{"docker-daemon": StatusSkip, "doer-image": StatusSkip, "agent-config-dir": StatusSkip},
		},
		{
			name:     "healthy container host passes",
			env:      func(t *testing.T) (Env, *proc.Fake) { return containerEnv(t, login, "") },
			want:     map[string]Status{"docker-daemon": StatusPass, "doer-image": StatusPass, "agent-config-dir": StatusPass},
			wantCall: "docker image inspect hounds-doer:latest",
		},
		{
			name: "unreachable docker daemon fails",
			env:  func(t *testing.T) (Env, *proc.Fake) { return containerEnv(t, login, "info") },
			want: map[string]Status{"docker-daemon": StatusFail},
		},
		{
			name:    "missing doer image fails with the build command",
			env:     func(t *testing.T) (Env, *proc.Fake) { return containerEnv(t, login, "image") },
			want:    map[string]Status{"doer-image": StatusFail},
			wantFix: map[string]string{"doer-image": "docker build"},
		},
		{
			name: "unset agent config dir fails",
			env:  func(t *testing.T) (Env, *proc.Fake) { return containerEnv(t, "", "") },
			want: map[string]Status{"agent-config-dir": StatusFail},
		},
		{
			name: "agent config dir without a login fails",
			env:  func(t *testing.T) (Env, *proc.Fake) { return containerEnv(t, noLogin, "") },
			want: map[string]Status{"agent-config-dir": StatusFail},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, f := tc.env(t)
			results := Run(context.Background(), env, false)
			for _, name := range containerChecks {
				if byName(results, name).Status == "missing" {
					t.Errorf("check %q is not registered", name)
				}
			}
			for name, want := range tc.want {
				got := byName(results, name)
				if got.Status != want {
					t.Errorf("%s = %s (%s), want %s", name, got.Status, got.Detail, want)
				}
				if want == StatusFail && got.Fix == "" {
					t.Errorf("%s failed without a fix", name)
				}
				if sub, ok := tc.wantFix[name]; ok && !strings.Contains(got.Fix, sub) {
					t.Errorf("%s fix = %q, want it to mention %q", name, got.Fix, sub)
				}
			}
			if tc.wantCall != "" {
				found := false
				for _, c := range f.Snapshot() {
					if c.Name+" "+strings.Join(c.Args, " ") == tc.wantCall {
						found = true
					}
				}
				if !found {
					t.Errorf("no call %q recorded", tc.wantCall)
				}
			}
		})
	}
}
