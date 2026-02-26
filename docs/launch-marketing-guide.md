# Launch & Marketing Playbook for gateway-pro

A realistic, step-by-step plan. Nothing here requires a budget.

---

## Part 1 — Before you open the repo

### Get the basics right first

Before announcing anything, make sure these are in place:

- `README.md` renders cleanly on GitHub (check the Preview tab)
- `make build && make test` both pass from a fresh clone on macOS and Linux
- `docker run snehakhoreja/gateway-pro` actually works
- `/healthz` and `/metrics` respond correctly
- At least one issue labeled `good-first-issue` is open

A broken "quick start" will kill your launch faster than anything else.

### Tag your first release

```bash
git tag v0.1.0
git push origin v0.1.0
```

GoReleaser + the release workflow will build binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, and windows/amd64, and publish them to GitHub Releases automatically.

---

## Part 2 — Launch sequence

### Week 1, Day 1 — Hacker News

Post a "Show HN" — this is your highest-leverage channel.

**Optimal timing:** Tuesday–Thursday, 8–10 AM US Eastern.

**Title (keep it factual):**
```
Show HN: Gateway-pro – lightweight API gateway in Go (rate limiting, LB, circuit breaking)
```

**Body (write this yourself — short, honest, direct):**
```
I built gateway-pro because I wanted a gateway I could actually read and
modify. Kong is powerful but complex; Traefik is great but config-heavy
for simple cases.

The project is ~1,500 lines of Go, no CGO, single binary. It covers:
- Load balancing: round-robin, least-conn, weighted, IP-hash
- Rate limiting: token bucket or sliding window; in-process or Redis
- Circuit breaking: per-backend, configurable thresholds
- Hot-reload config without restart

Happy to answer questions about the design decisions.

GitHub: https://github.com/snehakhoreja/gateway-pro
```

**Respond to every comment within the first 2 hours.** HN rewards engagement in the early window. Be honest about limitations — trying to oversell will get you flamed.

---

### Week 1, Days 2–3 — Reddit

Post to these subreddits, but space them out (don't batch-post):

- **r/golang** — most relevant audience, highest quality feedback
- **r/devops** — practitioners who actually run gateways
- **r/selfhosted** — this community loves single-binary tools
- **r/kubernetes** (if you have the K8s story solid)

**Template for r/golang:**
```
Title: gateway-pro: API gateway in Go — rate limiting, LB, circuit breaking (~1500 lines)

I wanted something I could read end-to-end and own the config model for,
so I built this instead of wrapping an existing tool.

The design is fairly standard: fsnotify for hot-reload, httputil.ReverseProxy
for the actual proxying, Prometheus metrics on a separate port. The circuit
breaker is a textbook three-state machine. Redis is optional (rate limiting
falls back to in-process).

What I found interesting: using smooth weighted round-robin (same algorithm
nginx uses) makes the weighted LB actually smooth rather than bursty.

GitHub: [link]

Feedback on the code welcome, especially the concurrency model in the
load balancer.
```

The last sentence matters — it invites technical feedback, which is what r/golang wants to give.

---

### Week 1, Days 4–5 — Write one technical article

Pick **one** platform and write a real article (2,000–3,000 words):

**Dev.to** is the best channel for this kind of project.

**Suggested title:**
> "Building a production-grade API gateway in Go: what I learned about circuit breakers and rate limiting"

**Outline:**
1. Why I built it (1 paragraph — be honest, not self-promotional)
2. The three-state circuit breaker — how it works, the rolling window, why the half-open state exists
3. Token bucket vs. sliding window rate limiting — the trade-offs
4. Hot-reload without restart — how fsnotify + atomic swaps work
5. What I'd do differently

Write like you're explaining it to a colleague, not marketing it to a customer. Technical readers will share honest content; they'll ignore promotional content.

Cross-post to Hashnode. Don't cross-post to Medium (their algorithm deprioritizes external links).

---

### Week 2 — Slack communities

Post in `#show-and-tell` or equivalent channels:

- **Gophers Slack** (invite: https://invite.slack.golangbridge.org/)
- **DevOps Chat** (https://devopschat.co)
- **CNCF Slack** — `#gateway-api` or `#networking`

Keep the message short in Slack — one paragraph + link. These communities are good for feedback and potential contributors, not raw traffic.

---

## Part 3 — Approaching companies

This is a different motion from getting GitHub stars. Stars don't pay rent; adoption by companies builds credibility.

### Who to target

Small-to-mid engineering teams (10–100 engineers) that:
- Run microservices but haven't yet standardized on a gateway
- Are on Go (they'll trust a Go implementation more)
- Have outgrown nginx as a poor man's gateway but don't want Kong's complexity

### How to find them

- Job postings that mention "API gateway" + Go → they have the problem
- GitHub repos that depend on `gorilla/mux`, `gin-gonic/gin`, `grpc/grpc-go` with no gateway in the stack
- Twitter/X: search "microservices golang" + frustration keywords

### What to send

Don't pitch. Start with something useful:

```
Hi [name],

I noticed [company] is using [relevant tech] — we're doing something
similar and ran into the API gateway problem.

I built gateway-pro (https://github.com/snehakhoreja/gateway-pro) to
solve it for our case. It's ~1500 lines of Go, single binary, handles
rate limiting + circuit breaking out of the box.

Not sure if it fits your stack, but if you're evaluating options I'm
happy to walk through the design.
```

The goal of the first message is not to get them to use it. It's to start a conversation with someone who has the problem.

### What companies actually want to hear

When you get on a call:

- **Operational simplicity:** single binary, no sidecars, no service mesh required
- **Auditability:** they can read the code; it's not a black box
- **Upgrade path:** Apache 2.0, no vendor lock-in, they can fork it
- **Observability:** Prometheus metrics work with their existing stack

Be upfront about what it's not: it's not Envoy, it doesn't do mTLS, it doesn't have a control plane. Companies respect honesty about scope.

---

## Part 4 — Growing past the launch

### What actually gets projects to 500+ stars

1. **Being listed in awesome-go** — open a PR to https://github.com/avelino/awesome-go under the "Networking" section
2. **Solving a specific, Googleable problem** — write articles targeting searches like "golang api gateway rate limiting" or "circuit breaker go example"
3. **Good issues** — label issues well, close them fast, thank contributors in release notes
4. **Showing up in comparison lists** — when someone asks "which Go gateway should I use?" on Reddit or HN, you want your project to have the answer already posted somewhere

### What doesn't work

- Buying followers / upvotes (projects get banned from HN for this)
- Announcing the same thing five times in a week
- Asking people to star the repo in your README (looks desperate)
- Over-claiming benchmarks without a reproducible methodology

### Measuring what matters

Metrics worth tracking:
- GitHub stars / week (leading indicator of discovery)
- Issues opened by people you don't know (real adoption signal)
- Forks (someone is building on it)
- Docker Hub pulls

Stars without issues usually means people found it interesting but didn't use it. Issues from strangers mean they're actually running it.
