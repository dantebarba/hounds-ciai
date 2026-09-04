# Doer image: the sandbox a single agent runs in, one container per task.
#
# It bakes only the environment (git, gh, ca-certs, ripgrep) and a non-root
# home. The claude binary and the OAuth credentials are NOT baked — they are
# injected at `docker run` time as bind mounts from the host, so the image
# carries no secrets and no coupling to a claude version.
FROM debian:stable-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        git \
        ripgrep \
        curl \
        gnupg \
        bash \
        python3 \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# A home the task's uid (bind -u <uid>:<gid> at run time) can write to. 1000
# matches the primary host user; the mounted CLAUDE_CONFIG_DIR lives under here.
RUN useradd -m -u 1000 -s /bin/bash agent
ENV HOME=/home/agent
WORKDIR /work
