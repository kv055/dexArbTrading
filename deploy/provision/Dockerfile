# Slim provisioning image: terraform + ansible + gh + openssh-client + git.
# Built once by provision-docker.sh; the container is the only thing that
# needs Ansible/Terraform installed — the host stays clean.
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    PATH="/usr/local/bin:/usr/bin:/bin"

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg lsb-release unzip \
        git openssh-client jq sudo \
        ansible python3 python3-apt \
    && rm -rf /var/lib/apt/lists/*

# Terraform (HashiCorp apt repo).
RUN curl -fsSL https://apt.releases.hashicorp.com/gpg \
        | gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com bookworm main" \
        > /etc/apt/sources.list.d/hashicorp.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends terraform \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI (official apt repo).
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# The container runs as root against a bind-mounted host repo owned by a
# non-root UID. Without this, git refuses with "dubious ownership in repository".
RUN git config --system --add safe.directory '*'

WORKDIR /repo
ENTRYPOINT ["/bin/bash"]
