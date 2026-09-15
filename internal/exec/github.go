package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sean1588/herdr-orchestrator/internal/proc"
)

// provisionGitHub gives a doer GitHub access through files in its private home:
// a gh hosts.yml holding the host's gh token and login, and a .gitconfig with the
// host's git identity, gh as the credential helper for github.com, and SSH
// github.com remotes rewritten to HTTPS so the token serves them too. It uses
// files rather than environment variables so a repository's project env can
// neither override nor shadow the channel. Nothing is written unless the host's
// gh has a token.
func provisionGitHub(ctx context.Context, r proc.Runner, home string) error {
	token, err := hostOutput(ctx, r, "gh", "auth", "token")
	if err != nil {
		return fmt.Errorf("github credentials: %w", err)
	}
	if token == "" {
		return errors.New("github credentials: the host's gh has no token; run `gh auth login`")
	}
	login, err := hostOutput(ctx, r, "gh", "config", "get", "-h", "github.com", "user")
	if err != nil {
		return fmt.Errorf("github credentials: %w", err)
	}
	name, err := hostOutput(ctx, r, "git", "config", "--global", "user.name")
	if err != nil {
		return fmt.Errorf("github credentials: %w", err)
	}
	email, err := hostOutput(ctx, r, "git", "config", "--global", "user.email")
	if err != nil {
		return fmt.Errorf("github credentials: %w", err)
	}

	ghDir := filepath.Join(home, ".config", "gh")
	if err := os.MkdirAll(ghDir, 0o700); err != nil {
		return fmt.Errorf("github credentials: create gh config dir: %w", err)
	}
	hosts := fmt.Sprintf("github.com:\n    oauth_token: %s\n    user: %s\n    git_protocol: https\n", token, login)
	if err := os.WriteFile(filepath.Join(ghDir, "hosts.yml"), []byte(hosts), 0o600); err != nil {
		return fmt.Errorf("github credentials: write gh hosts.yml: %w", err)
	}
	gitconfig := "[user]\n" +
		"\tname = " + gitConfigQuote(name) + "\n" +
		"\temail = " + gitConfigQuote(email) + "\n" +
		"[credential \"https://github.com\"]\n" +
		"\thelper =\n" +
		"\thelper = !gh auth git-credential\n" +
		"[url \"https://github.com/\"]\n" +
		"\tinsteadOf = git@github.com:\n" +
		"\tinsteadOf = ssh://git@github.com/\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		return fmt.Errorf("github credentials: write .gitconfig: %w", err)
	}
	return nil
}

// hostOutput runs a command on the host and returns its trimmed output.
func hostOutput(ctx context.Context, r proc.Runner, name string, args ...string) (string, error) {
	out, err := r.Run(ctx, "", name, args...)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitConfigQuote returns v as a double-quoted git config value with backslashes
// and quotes escaped, so characters such as # and ; stay part of the value
// instead of starting a comment.
func gitConfigQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}
