# Contributing to gateway-pro

Thanks for taking the time. Here's how to get your changes in cleanly.

## Local dev setup

```bash
git clone https://github.com/snehakhoreja/gateway-pro.git
cd gateway-pro
go mod download
make build   # sanity check
make test    # must be green
```

Requires **Go 1.22+**. No other system deps for the core binary.

## Branching model

| Branch | Purpose |
|--------|---------|
| `main` | Stable, always releasable |
| `develop` | Integration branch — PRs target here |
| `feat/<name>` | Feature work |
| `fix/<name>` | Bug fixes |

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(ratelimiter): add sliding window algorithm
fix(circuitbreaker): race condition on half-open transition
docs(readme): add kubernetes deployment example
```

## Before opening a PR

1. `make fmt` — no diff after formatting
2. `make vet` — no issues
3. `make lint` — no issues (golangci-lint v1.57+)
4. `make test` — all green
5. For new features, add tests. Aim for the existing ~80% coverage bar.
6. Update `CHANGELOG.md` under `[Unreleased]`

## PR etiquette

- Keep PRs focused — one logical change per PR
- Fill in the PR template
- Be responsive to review comments; stale PRs (>14 days) will be closed

## Design principles

We prefer these over cleverness:

- **Correctness first.** Performance second.
- **Zero external deps for the hot path.** Redis is optional; nothing else should be.
- **Every public symbol has a doc comment.**
- **No init() side effects.**

## Good first issues

Look for the [`good-first-issue`](https://github.com/snehakhoreja/gateway-pro/labels/good-first-issue) label.

## Questions

Open a [Discussion](https://github.com/snehakhoreja/gateway-pro/discussions) rather than an issue for general questions.
