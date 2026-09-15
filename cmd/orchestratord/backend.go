package main

import (
	"fmt"

	"github.com/sean1588/herdr-orchestrator/internal/config"
	"github.com/sean1588/herdr-orchestrator/internal/doctor"
	"github.com/sean1588/herdr-orchestrator/internal/exec"
	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// agentBackend is what the daemon and doctor need from an execution backend:
// driving agents for the engine, and the kickoff smoke test for the doctor.
type agentBackend interface {
	exec.ExecutionBackend
	doctor.KickoffSmoker
}

// newBackend builds the execution backend the workflow's execution.backend
// selects: herdr (the default) runs agents on the host, container runs each doer
// in its own Docker container. Both resolve worktrees from repoDir and, when set,
// worktreesDir. Any other value is refused rather than silently falling back to
// herdr, so a config asking for isolation never runs unsandboxed.
func newBackend(wf *config.Workflow, r proc.Runner, repoDir, worktreesDir string) (agentBackend, error) {
	switch backend := wf.Policies.Execution.Backend; backend {
	case "", "herdr":
		h := exec.NewHerdr(r)
		configureWorktrees(h, repoDir, worktreesDir)
		return h, nil
	case "container":
		d, err := exec.NewDocker(r, wf.Policies.Execution.DoerImage())
		if err != nil {
			return nil, err
		}
		configureWorktrees(d.Herdr, repoDir, worktreesDir)
		return d, nil
	default:
		return nil, fmt.Errorf("execution.backend %q is not supported (use herdr or container)", backend)
	}
}

// configureWorktrees points h at the main checkout, so Cleanup can resolve a
// task's worktree without a live pane, and at the worktrees dir when one is set.
func configureWorktrees(h *exec.Herdr, repoDir, worktreesDir string) {
	h.RepoDir = repoDir
	if worktreesDir != "" {
		h.WorktreesDir = worktreesDir
	}
}
