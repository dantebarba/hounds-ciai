package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// usesContainerBackend reports whether the loaded workflow runs doers in Docker
// containers, the only configuration the container checks apply to.
func usesContainerBackend(env Env) bool {
	return env.Workflow != nil && env.Workflow.Policies.Execution.Backend == "container"
}

// checkDockerDaemon confirms the Docker daemon answers. Under the container
// backend every doer launch is a `docker run`, so an unreachable daemon would
// otherwise surface only as a failed launch inside an agent's pane.
func checkDockerDaemon(ctx context.Context, env Env) Result {
	const name = "docker-daemon"
	if !usesContainerBackend(env) {
		return skip(name, "execution.backend is not container")
	}
	out, err := env.Runner.Run(ctx, "", env.DockerBin, "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return fail(name, fmt.Sprintf("cannot reach the Docker daemon: %v", err),
			"start Docker and make sure the daemon's user can run `docker info`")
	}
	return pass(name, "server "+firstLine(out))
}

// checkDoerImage confirms the configured doer image exists locally, so a doer
// launch never stalls on a pull or fails on an image that was never built.
func checkDoerImage(ctx context.Context, env Env) Result {
	const name = "doer-image"
	if !usesContainerBackend(env) {
		return skip(name, "execution.backend is not container")
	}
	image := env.Workflow.Policies.Execution.DoerImage()
	if _, err := env.Runner.Run(ctx, "", env.DockerBin, "image", "inspect", image); err != nil {
		return fail(name, fmt.Sprintf("image %s not found locally: %v", image, err),
			fmt.Sprintf("build it with `docker build -t %s -f docker/doer/Dockerfile docker/doer`, or set execution.image to an image you have", image))
	}
	return pass(name, image)
}

// checkAgentConfigDir confirms CLAUDE_CONFIG_DIR names a directory holding the
// agent login, which the container backend copies into every doer's private home.
func checkAgentConfigDir(ctx context.Context, env Env) Result {
	const name = "agent-config-dir"
	if !usesContainerBackend(env) {
		return skip(name, "execution.backend is not container")
	}
	dir := env.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return fail(name, "CLAUDE_CONFIG_DIR is not set",
			"export CLAUDE_CONFIG_DIR as the agent config dir whose login doers copy (e.g. ~/.claude-personal)")
	}
	creds := filepath.Join(dir, ".credentials.json")
	if _, err := os.Stat(creds); err != nil {
		return fail(name, fmt.Sprintf("no readable agent login at %s: %v", creds, err),
			"log the agent in with CLAUDE_CONFIG_DIR set, so it writes .credentials.json there")
	}
	return pass(name, dir)
}
