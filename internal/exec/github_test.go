package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// hostGitHubFake answers the host commands the GitHub provisioner runs: the gh
// token and login, and git's global identity. A tokenErr fails `gh auth token`.
func hostGitHubFake(token, login, name, email string, tokenErr error) *proc.Fake {
	return &proc.Fake{Responder: func(c proc.Call) ([]byte, error) {
		switch c.Name + " " + strings.Join(c.Args, " ") {
		case "gh auth token":
			return []byte(token + "\n"), tokenErr
		case "gh config get -h github.com user":
			return []byte(login + "\n"), nil
		case "git config --global user.name":
			return []byte(name + "\n"), nil
		case "git config --global user.email":
			return []byte(email + "\n"), nil
		}
		return nil, nil
	}}
}

func TestProvisionGitHub_WritesGhAndGitConfig(t *testing.T) {
	tests := []struct {
		name          string
		gitName       string
		wantGitconfig string
	}{
		{
			name:    "plain identity",
			gitName: "Octo Operator",
			wantGitconfig: `[user]
	name = "Octo Operator"
	email = "octo@example.com"
[credential "https://github.com"]
	helper =
	helper = !gh auth git-credential
[url "https://github.com/"]
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
`,
		},
		{
			name:    "identity with comment and quote characters",
			gitName: `Octo "O#1" Operator`,
			wantGitconfig: `[user]
	name = "Octo \"O#1\" Operator"
	email = "octo@example.com"
[credential "https://github.com"]
	helper =
	helper = !gh auth git-credential
[url "https://github.com/"]
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			f := hostGitHubFake("gho_fakeTOKEN123", "octo-operator", tc.gitName, "octo@example.com", nil)

			if err := provisionGitHub(context.Background(), f, home); err != nil {
				t.Fatalf("provisionGitHub: %v", err)
			}

			hosts, err := os.ReadFile(filepath.Join(home, ".config", "gh", "hosts.yml"))
			if err != nil {
				t.Fatalf("read hosts.yml: %v", err)
			}
			wantHosts := "github.com:\n    oauth_token: gho_fakeTOKEN123\n    user: octo-operator\n    git_protocol: https\n"
			if string(hosts) != wantHosts {
				t.Errorf("hosts.yml =\n%s\nwant\n%s", hosts, wantHosts)
			}
			gitconfig, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
			if err != nil {
				t.Fatalf("read .gitconfig: %v", err)
			}
			if string(gitconfig) != tc.wantGitconfig {
				t.Errorf(".gitconfig =\n%s\nwant\n%s", gitconfig, tc.wantGitconfig)
			}
			assertMode(t, filepath.Join(home, ".config"), 0o700)
			assertMode(t, filepath.Join(home, ".config", "gh"), 0o700)
			assertMode(t, filepath.Join(home, ".config", "gh", "hosts.yml"), 0o600)
			assertMode(t, filepath.Join(home, ".gitconfig"), 0o600)
		})
	}
}

func TestProvisionGitHub_FailsWithoutAToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		tokenErr error
	}{
		{name: "empty token", token: ""},
		{name: "gh not logged in", tokenErr: errors.New("exit status 1: no oauth token found")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			f := hostGitHubFake(tc.token, "octo-operator", "Octo Operator", "octo@example.com", tc.tokenErr)

			if err := provisionGitHub(context.Background(), f, home); err == nil {
				t.Fatal("provisionGitHub = nil error, want a missing-token error")
			}
			for _, p := range []string{".gitconfig", filepath.Join(".config", "gh", "hosts.yml")} {
				if _, err := os.Stat(filepath.Join(home, p)); !os.IsNotExist(err) {
					t.Errorf("%s written despite a missing token (stat err = %v)", p, err)
				}
			}
		})
	}
}
