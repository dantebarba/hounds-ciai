package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const fakeCredentials = `{"claudeAiOauth":{"accessToken":"at-fake","refreshToken":"rt-fake","expiresAt":1767225600000}}`

// writeHostConfig creates a host agent config dir holding the given files and
// returns its path.
func writeHostConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write host %s: %v", name, err)
		}
	}
	return dir
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

func TestCredProvisioner_ProvisionCopiesCredentialsPrivately(t *testing.T) {
	host := writeHostConfig(t, map[string]string{".credentials.json": fakeCredentials})
	root := t.TempDir()

	cfg, err := newCredProvisioner(host, root).Provision(context.Background(), "issue-5", "/work")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if want := filepath.Join(root, "issue-5"); cfg != want {
		t.Errorf("config dir = %q, want %q", cfg, want)
	}
	got, err := os.ReadFile(filepath.Join(cfg, ".credentials.json"))
	if err != nil {
		t.Fatalf("read copied credentials: %v", err)
	}
	if !bytes.Equal(got, []byte(fakeCredentials)) {
		t.Errorf("copied credentials = %q, want host bytes %q", got, fakeCredentials)
	}
	assertMode(t, cfg, 0o700)
	assertMode(t, filepath.Join(cfg, ".credentials.json"), 0o600)
}

func TestCredProvisioner_ProvisionCopiesOnlyStartupKeysAndTrustsWorkdir(t *testing.T) {
	hostClaudeJSON := `{
  "hasCompletedOnboarding": true,
  "lastOnboardingVersion": "2.1.260",
  "oauthAccount": { "emailAddress": "op@example.com", "accountCreatedAt": 9007199254740993 },
  "machineID": "m-fake",
  "userID": "u-fake",
  "numStartups": 42,
  "projects": {
    "/elsewhere": { "hasTrustDialogAccepted": true, "lastCost": 1.25 }
  }
}`
	host := writeHostConfig(t, map[string]string{
		".credentials.json": fakeCredentials,
		".claude.json":      hostClaudeJSON,
	})

	cfg, err := newCredProvisioner(host, t.TempDir()).Provision(context.Background(), "issue-5", "/work")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	path := filepath.Join(cfg, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read copied .claude.json: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("copied .claude.json is not JSON: %v\n%s", err, raw)
	}
	var keys []string
	for k := range top {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"hasCompletedOnboarding", "lastOnboardingVersion", "oauthAccount", "projects"}; !slices.Equal(keys, want) {
		t.Errorf("top-level keys = %v, want %v", keys, want)
	}
	for _, leaked := range []string{"m-fake", "u-fake", "/elsewhere", "numStartups"} {
		if bytes.Contains(raw, []byte(leaked)) {
			t.Errorf("host value %q leaked into the doer config:\n%s", leaked, raw)
		}
	}
	for _, kept := range []string{"op@example.com", "2.1.260", "9007199254740993"} {
		if !bytes.Contains(raw, []byte(kept)) {
			t.Errorf("allowlisted host value %q missing (or lost precision):\n%s", kept, raw)
		}
	}
	var got struct {
		HasCompletedOnboarding bool                      `json:"hasCompletedOnboarding"`
		Projects               map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode copied .claude.json: %v", err)
	}
	if !got.HasCompletedOnboarding {
		t.Errorf("hasCompletedOnboarding not carried over:\n%s", raw)
	}
	wantProjects := map[string]map[string]any{"/work": {"hasTrustDialogAccepted": true}}
	if len(got.Projects) != 1 || len(got.Projects["/work"]) != 1 || got.Projects["/work"]["hasTrustDialogAccepted"] != true {
		t.Errorf("projects = %v, want exactly %v", got.Projects, wantProjects)
	}
	assertMode(t, path, 0o600)
}

func TestCredProvisioner_DiscardRemovesCopyAndIsIdempotent(t *testing.T) {
	host := writeHostConfig(t, map[string]string{".credentials.json": fakeCredentials})
	p := newCredProvisioner(host, t.TempDir())
	ctx := context.Background()

	cfg, err := p.Provision(ctx, "issue-5", "/work")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := p.Discard(ctx, "issue-5"); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(cfg); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("config dir still present after Discard (stat err = %v)", err)
	}
	if err := p.Discard(ctx, "issue-5"); err != nil {
		t.Errorf("second Discard = %v, want nil", err)
	}
	if err := p.Discard(ctx, "never-provisioned"); err != nil {
		t.Errorf("Discard of unknown task = %v, want nil", err)
	}
}

func TestCredProvisioner_RejectsTaskIDsThatLeaveRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "creds")
	sibling := filepath.Join(parent, "sibling")
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	keep := filepath.Join(sibling, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write sibling file: %v", err)
	}
	host := writeHostConfig(t, map[string]string{".credentials.json": fakeCredentials})
	p := newCredProvisioner(host, root)
	ctx := context.Background()

	for _, id := range []string{"", ".", "..", "../sibling", "a/b", "/abs"} {
		t.Run(id, func(t *testing.T) {
			if _, err := p.Provision(ctx, id, "/work"); err == nil {
				t.Errorf("Provision(%q) = nil error, want rejection", id)
			}
			if err := p.Discard(ctx, id); err == nil {
				t.Errorf("Discard(%q) = nil error, want rejection", id)
			}
		})
	}

	if _, err := os.Stat(keep); err != nil {
		t.Errorf("file beside the creds root was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, ".credentials.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("credentials written outside the creds root (stat err = %v)", err)
	}
}
