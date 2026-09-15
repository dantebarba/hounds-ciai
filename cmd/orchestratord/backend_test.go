package main

import (
	"testing"

	"github.com/sean1588/herdr-orchestrator/internal/config"
	"github.com/sean1588/herdr-orchestrator/internal/exec"
	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// workflowWithBackend returns a workflow whose execution policy selects backend.
func workflowWithBackend(backend string) *config.Workflow {
	return &config.Workflow{Policies: config.Policies{Execution: config.Execution{Backend: backend}}}
}

func TestNewBackend_SelectsFromExecutionBackend(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tests := []struct {
		backend    string
		wantDocker bool
		wantErr    bool
	}{
		{backend: ""},
		{backend: "herdr"},
		{backend: "container", wantDocker: true},
		{backend: "local", wantErr: true},
	}
	for _, tc := range tests {
		t.Run("backend="+tc.backend, func(t *testing.T) {
			b, err := newBackend(workflowWithBackend(tc.backend), &proc.Fake{}, "/home/u/repo", "/home/u/wts")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("newBackend(%q) = nil error, want unsupported-backend error", tc.backend)
				}
				return
			}
			if err != nil {
				t.Fatalf("newBackend(%q): %v", tc.backend, err)
			}
			var herdr *exec.Herdr
			switch got := b.(type) {
			case *exec.DockerBackend:
				if !tc.wantDocker {
					t.Fatalf("newBackend(%q) = DockerBackend, want herdr backend", tc.backend)
				}
				herdr = got.Herdr
			case *exec.Herdr:
				if tc.wantDocker {
					t.Fatalf("newBackend(%q) = herdr backend, want DockerBackend", tc.backend)
				}
				herdr = got
			default:
				t.Fatalf("newBackend(%q) = %T, want a herdr or Docker backend", tc.backend, b)
			}
			if herdr.RepoDir != "/home/u/repo" || herdr.WorktreesDir != "/home/u/wts" {
				t.Errorf("RepoDir/WorktreesDir = %q/%q, want /home/u/repo and /home/u/wts", herdr.RepoDir, herdr.WorktreesDir)
			}
		})
	}
}
