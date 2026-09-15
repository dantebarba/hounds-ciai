package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	credentialsFile  = ".credentials.json"
	agentConfigFile  = ".claude.json"
	trustAcceptedKey = "hasTrustDialogAccepted"
)

// credProvisioner gives each doer its own private copy of the host agent's
// login, so concurrent doers never share, corrupt, or race on refreshing the
// host token, and the copy can be discarded when the task settles.
type credProvisioner struct {
	hostConfigDir string
	root          string
}

// newCredProvisioner returns a provisioner that copies from hostConfigDir (the
// agent's CLAUDE_CONFIG_DIR on the host) into per-task directories under root.
func newCredProvisioner(hostConfigDir, root string) *credProvisioner {
	return &credProvisioner{hostConfigDir: hostConfigDir, root: root}
}

// Provision creates the task's private config directory under root, copies the
// host credentials into it, and writes a copy of the host agent config with
// workdir marked trusted so the agent never stops at the folder-trust dialog.
// It returns the directory to mount as the container's CLAUDE_CONFIG_DIR.
// workdir is the agent's working directory as seen inside the container.
func (p *credProvisioner) Provision(ctx context.Context, taskID, workdir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := p.taskDir(taskID)
	if err != nil {
		return "", fmt.Errorf("provision: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("provision %s: create config dir: %w", taskID, err)
	}
	creds, err := os.ReadFile(filepath.Join(p.hostConfigDir, credentialsFile))
	if err != nil {
		return "", fmt.Errorf("provision %s: read host credentials: %w", taskID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, credentialsFile), creds, 0o600); err != nil {
		return "", fmt.Errorf("provision %s: write credentials: %w", taskID, err)
	}
	hostConfig, err := os.ReadFile(filepath.Join(p.hostConfigDir, agentConfigFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("provision %s: read host agent config: %w", taskID, err)
	}
	trusted, err := trustWorkdir(hostConfig, workdir)
	if err != nil {
		return "", fmt.Errorf("provision %s: %w", taskID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentConfigFile), trusted, 0o600); err != nil {
		return "", fmt.Errorf("provision %s: write agent config: %w", taskID, err)
	}
	return dir, nil
}

// Discard removes the task's private config directory, and with it the
// credential copy, so a settled task leaves no token on disk. Discarding a task
// that was never provisioned, or was already discarded, is not an error.
func (p *credProvisioner) Discard(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := p.taskDir(taskID)
	if err != nil {
		return fmt.Errorf("discard: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("discard %s: %w", taskID, err)
	}
	return nil
}

// taskDir returns the task's private directory under root. taskID must be a
// single plain path element, so no task can read, write, or delete anything
// outside root.
func (p *credProvisioner) taskDir(taskID string) (string, error) {
	if taskID == "." || taskID == ".." || filepath.Base(taskID) != taskID {
		return "", fmt.Errorf("invalid task id %q: must be a single path element", taskID)
	}
	return filepath.Join(p.root, taskID), nil
}

// trustWorkdir returns hostConfig with projects[workdir] marked as having
// accepted the folder-trust dialog, every other value preserved. An empty
// hostConfig yields a config holding only that trust entry. Numbers are kept as
// their literal digits so large integers survive the round trip.
func trustWorkdir(hostConfig []byte, workdir string) ([]byte, error) {
	config := map[string]any{}
	if len(bytes.TrimSpace(hostConfig)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(hostConfig))
		dec.UseNumber()
		if err := dec.Decode(&config); err != nil {
			return nil, fmt.Errorf("parse host agent config: %w", err)
		}
	}
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		config["projects"] = projects
	}
	project, _ := projects[workdir].(map[string]any)
	if project == nil {
		project = map[string]any{}
		projects[workdir] = project
	}
	project[trustAcceptedKey] = true
	out, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode agent config: %w", err)
	}
	return out, nil
}
