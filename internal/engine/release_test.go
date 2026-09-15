package engine

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sean1588/herdr-orchestrator/internal/exec"
	"github.com/sean1588/herdr-orchestrator/internal/github"
	"github.com/sean1588/herdr-orchestrator/internal/store"
)

func TestDrive_TerminalWithPR_ReleasesOnce(t *testing.T) {
	st := newStore(t)
	b := agentDoneBackend()
	b.verdictOnSpawn = map[string]string{"reviewer": `{"verdict":"escalate","feedback":""}`}
	e := newEngine(t, st, b, &fakeGH{}, 5*time.Second)
	task := seedPROpen(t, st, 42)

	final, err := e.drive(context.Background(), task)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if final != "escalated" {
		t.Fatalf("final = %q, want escalated", final)
	}
	if !slices.Equal(b.releases, []string{task.ID}) {
		t.Errorf("releases = %v, want exactly [%s] for a PR-bearing terminal", b.releases, task.ID)
	}
	if len(b.cleanups) != 0 {
		t.Errorf("a PR-bearing terminal keeps its worktree, got cleanups %v", b.cleanups)
	}
}

func TestDrive_NoPRTerminal_ReleasesOnce(t *testing.T) {
	st := newStore(t)
	b := agentDoneBackend()
	b.verdictOnSpawn = map[string]string{"triager": `{"verdict":"reject","feedback":""}`}
	e := newEngine(t, st, b, &fakeGH{}, 5*time.Second)
	task := newIntakeTask(t, st)

	final, err := e.drive(context.Background(), task)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if final != "closed" {
		t.Fatalf("final = %q, want closed", final)
	}
	if !slices.Equal(b.releases, []string{task.ID}) {
		t.Errorf("releases = %v, want exactly [%s]", b.releases, task.ID)
	}
}

func TestDrive_ReDriveOfSettledTask_DoesNotReleaseAgain(t *testing.T) {
	st := newStore(t)
	b := agentDoneBackend()
	b.verdictOnSpawn = map[string]string{"triager": `{"verdict":"reject","feedback":""}`}
	e := newEngine(t, st, b, &fakeGH{}, 5*time.Second)
	task := newIntakeTask(t, st)

	if _, err := e.drive(context.Background(), task); err != nil {
		t.Fatalf("first drive: %v", err)
	}
	if _, err := e.drive(context.Background(), task); err != nil {
		t.Fatalf("re-drive: %v", err)
	}
	if len(b.releases) != 1 {
		t.Errorf("releases = %v, want one release for one settle", b.releases)
	}
}

func TestDrive_GoalHalt_DoesNotRelease(t *testing.T) {
	st := newStore(t)
	b := &fakeBackend{pane: "w1:p1", events: []exec.Event{
		{PaneID: "w1:p1", State: exec.StateDone},
	}}
	e := newEngine(t, st, b, &fakeGH{pr: &github.PR{Number: 42, State: "OPEN"}}, 5*time.Second)
	e.goal = "pr_open"

	if _, err := e.Run(context.Background(), 7); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(b.releases) != 0 {
		t.Errorf("a non-terminal goal halt (pr_open) must not release, got %v", b.releases)
	}
}

func TestDrive_OperatorCancel_Releases(t *testing.T) {
	st := newStore(t)
	b := &fakeBackend{pane: "w1:p1"}
	e := newEngine(t, st, b, &fakeGH{}, 5*time.Second)
	bg := context.Background()
	if err := st.CreateTask(bg, &store.Task{
		ID: "issue-7", Issue: 7, Repo: "owner/repo",
		Branch: "agent/issue-7", CurrentState: "implementing",
	}); err != nil {
		t.Fatal(err)
	}
	task, _ := st.GetTask(bg, "issue-7")

	ctx, cancel := context.WithCancelCause(bg)
	done := make(chan string, 1)
	go func() {
		final, _ := e.drive(ctx, task)
		done <- final
	}()
	time.Sleep(50 * time.Millisecond)
	cancel(ErrOperatorCancel)

	select {
	case final := <-done:
		if final != CancelState {
			t.Fatalf("final = %q, want %q", final, CancelState)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drive did not settle after operator cancel")
	}
	if !slices.Equal(b.releases, []string{"issue-7"}) {
		t.Errorf("releases = %v, want exactly [issue-7] after an operator cancel", b.releases)
	}
}

func TestDrive_ReleaseError_DoesNotFailDrive(t *testing.T) {
	st := newStore(t)
	b := agentDoneBackend()
	b.releaseErr = errors.New("credential dir busy")
	b.verdictOnSpawn = map[string]string{"triager": `{"verdict":"reject","feedback":""}`}
	e := newEngine(t, st, b, &fakeGH{}, 5*time.Second)
	task := newIntakeTask(t, st)

	final, err := e.drive(context.Background(), task)
	if err != nil {
		t.Fatalf("drive must not fail on a Release error, got %v", err)
	}
	if final != "closed" {
		t.Fatalf("final = %q, want closed", final)
	}
	if len(b.releases) != 1 {
		t.Errorf("Release should still have been attempted once, got %v", b.releases)
	}
}
