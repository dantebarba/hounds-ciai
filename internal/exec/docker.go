package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

const (
	containerAgentHome = "/hounds/agent"
	containerBinDir    = "/usr/local/bin"
)

// dockerDeps are the host facts a DockerBackend needs: who the doer runs as,
// where each agent binary lives, where the host agent login comes from and where
// per-task copies of it go, and which image the doer runs in.
type dockerDeps struct {
	uid, gid      int
	resolveAgent  func(name string) (string, error)
	hostConfigDir string
	credsRoot     string
	image         string
}

// DockerBackend runs each doer's agent in its own Docker container inside a herdr
// pane. It is the herdr backend with two differences: before spawning it gives the
// task a private copy of the host agent login, and it wraps the agent launch in
// `docker run`, with HERDR_AGENT set on the host-side docker process so herdr
// still detects the agent and reports its status through the container.
type DockerBackend struct {
	*Herdr
	deps  dockerDeps
	creds *credProvisioner
}

var _ ExecutionBackend = (*DockerBackend)(nil)

// NewDocker returns a DockerBackend that runs each doer in image, with the host
// defaults: the doer runs as the current uid/gid, agent binaries are found on
// PATH with symlinks resolved, the agent login is copied from CLAUDE_CONFIG_DIR,
// and per-task copies live under the user cache dir in hounds/doer-creds.
//
// CLAUDE_CONFIG_DIR must be set. Without it the agent's config and login are
// split across the home directory and, on macOS, the Keychain, so there is no
// single directory whose login can be copied; failing here surfaces that at
// startup instead of as a doer stuck at a login or onboarding screen.
func NewDocker(r proc.Runner, image string) (*DockerBackend, error) {
	hostConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if hostConfigDir == "" {
		return nil, errors.New("docker backend: CLAUDE_CONFIG_DIR must point at the agent config dir whose login doers copy")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("docker backend: locate user cache dir: %w", err)
	}
	return newDockerBackend(r, dockerDeps{
		uid:           os.Getuid(),
		gid:           os.Getgid(),
		resolveAgent:  resolveAgentBinary,
		hostConfigDir: hostConfigDir,
		credsRoot:     filepath.Join(cache, "hounds", "doer-creds"),
		image:         image,
	}), nil
}

// resolveAgentBinary finds name on PATH and follows symlinks to the real binary,
// so the mount carries the executable itself rather than a link that would
// dangle inside the container.
func resolveAgentBinary(name string) (string, error) {
	found, err := osexec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(found)
}

// newDockerBackend returns a DockerBackend that drives herdr and docker through r.
func newDockerBackend(r proc.Runner, deps dockerDeps) *DockerBackend {
	return &DockerBackend{
		Herdr: NewHerdr(r),
		deps:  deps,
		creds: newCredProvisioner(deps.hostConfigDir, deps.credsRoot),
	}
}

// Spawn provisions the task's private agent home (credentials, trust for the
// worktree, a temp dir), then spawns through the herdr backend with the agent
// launch replaced by a container launch. The worktree and the repository's git
// directory are mounted at their host paths, so git metadata and the trust entry
// resolve identically inside the container. A failed spawn discards the
// credential copy: the engine never drives an agent whose spawn returned an
// error, and the next attempt provisions a fresh one.
func (d *DockerBackend) Spawn(ctx context.Context, s Spawn) (Handle, error) {
	if len(s.Launch) == 0 {
		return Handle{}, errors.New("docker spawn: no launch argv")
	}
	agent := filepath.Base(s.Launch[0])
	agentBin, err := d.deps.resolveAgent(s.Launch[0])
	if err != nil {
		return Handle{}, fmt.Errorf("docker spawn %s: resolve agent %s: %w", s.TaskID, s.Launch[0], err)
	}
	workdir := d.worktreePath(s)
	home, err := d.creds.Provision(ctx, s.TaskID, workdir)
	if err != nil {
		return Handle{}, fmt.Errorf("docker spawn: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return Handle{}, fmt.Errorf("docker spawn %s: create agent tmp dir: %w", s.TaskID, err)
	}
	gitDir := filepath.Join(s.RepoDir, ".git")
	launch := []string{
		"HERDR_AGENT=" + agent, "docker", "run", "-it", "--rm",
		"-u", fmt.Sprintf("%d:%d", d.deps.uid, d.deps.gid),
		"-v", agentBin + ":" + path.Join(containerBinDir, agent) + ":ro",
		"-v", home + ":" + containerAgentHome,
		"-v", workdir + ":" + workdir,
		"-v", gitDir + ":" + gitDir,
		"-w", workdir,
		"-e", "HOME=" + containerAgentHome,
		"-e", "CLAUDE_CONFIG_DIR=" + containerAgentHome,
		"-e", "CLAUDE_CODE_TMPDIR=" + path.Join(containerAgentHome, "tmp"),
		d.deps.image,
		agent,
	}
	launch = append(launch, s.Launch[1:]...)
	for i, arg := range launch {
		launch[i] = shellQuote(arg)
	}
	s.Launch = launch
	hd, err := d.Herdr.Spawn(ctx, s)
	if err != nil {
		return hd, errors.Join(err, d.creds.Discard(context.WithoutCancel(ctx), s.TaskID))
	}
	return hd, nil
}

// Cleanup runs the herdr backend's cleanup and always discards the task's
// credential copy, even when there is no workspace left to close or herdr's
// cleanup fails, so a settled task never leaves a token on disk.
func (d *DockerBackend) Cleanup(ctx context.Context, taskID string) error {
	return errors.Join(d.Herdr.Cleanup(ctx, taskID), d.creds.Discard(ctx, taskID))
}

// Release discards the task's credential copy, so a settled task leaves no
// token on disk whether or not its worktree is kept for a human.
func (d *DockerBackend) Release(ctx context.Context, taskID string) error {
	return d.creds.Discard(ctx, taskID)
}

// shellQuote returns arg as a single shell word: unchanged when every character
// is one no POSIX shell treats specially, otherwise single-quoted with embedded
// single quotes escaped. herdr runs the launch line through the pane's shell, so
// an unquoted path containing a space would split into two arguments.
func shellQuote(arg string) string {
	if arg != "" && strings.IndexFunc(arg, isShellUnsafe) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// isShellUnsafe reports whether r needs quoting to stay literal in a shell word.
func isShellUnsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("-_./:=@%+,", r)
}
