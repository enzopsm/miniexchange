!> # Request

Consider the task described by the documents below.

- Question: What does OCI compliant even mean and why is that relevant here?

# Context

## Task Statement

/Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md

> 

Let me read the challenge statement first to understand the context.
Reading file: /Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md, all lines (using tool: read)
 ✓ Successfully read 3188 bytes from /Users/enzopsm/Documents/personal/lifestask/Decade/challenge_statement/frontier-engineering-challenge.md
 - Completed in 0.0s

OCI stands for Open Container Initiative. It's a set of industry standards that define:

1. The image format — how a container image is structured (layers, manifest, config)
2. The runtime specification — how a container is executed

In practical terms: if you write a standard Dockerfile and build it with Docker (or Podman, Buildah, etc.), the resulting image is already OCI-compliant. Docker images have been OCI-compliant by default since Docker 1.10+.

Why they mention it: they want to make sure you deliver a containerized application that anyone can run with docker build + docker run (or any OCI-compatible runtime) without needing to install Go, Node, or whatever you pick. It's about reproducibility — they clone your repo, run one or two commands, and the whole system is up.

What you need to do: provide a Dockerfile (and ideally a docker-compose.yml if you have multiple services like API + DB + frontend). That's it. Don't overthink this — a well-written Dockerfile already satisfies the OCI requirement.