# How to put gateway-pro on GitHub and publish it

Step-by-step. Takes about 30 minutes the first time.

---

## 1. Create the GitHub repository

1. Go to https://github.com/new
2. Repository name: `gateway-pro`
3. Description: `Lightweight API gateway in Go — rate limiting, load balancing, circuit breaking`
4. Visibility: **Public**
5. Do NOT initialize with README (you already have one)
6. Click **Create repository**

---

## 2. Push your code

```bash
cd /path/to/gateway-pro

git init
git add .
git commit -m "feat: initial release

- Load balancing: round-robin, least-conn, weighted, IP-hash
- Rate limiting: token bucket + sliding window (local + Redis)
- Circuit breaking: per-backend, three-state
- Active health checks
- Prometheus metrics + structured logging
- Hot-reload config via fsnotify
- Docker + Kubernetes + Helm"

git branch -M main
git remote add origin https://github.com/snehakhoreja/gateway-pro.git
git push -u origin main
```

---

## 3. Add repository secrets (for Docker Hub publishing)

1. Go to your repo → **Settings** → **Secrets and variables** → **Actions**
2. Click **New repository secret** and add:
   - `DOCKERHUB_USERNAME` → your Docker Hub username
   - `DOCKERHUB_TOKEN` → Docker Hub access token (create one at https://hub.docker.com/settings/security)

---

## 4. Create a Docker Hub repository

1. Go to https://hub.docker.com
2. Create account or sign in
3. Click **Create Repository**
4. Name: `gateway-pro`
5. Visibility: Public
6. Click **Create**

---

## 5. Tag and release v0.1.0

```bash
git tag v0.1.0
git push origin v0.1.0
```

This triggers the release workflow which:
- Builds binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- Creates a GitHub Release with the binaries attached
- Pushes Docker images tagged `0.1.0`, `0.1`, and `latest` to Docker Hub

Watch the progress at: https://github.com/snehakhoreja/gateway-pro/actions

---

## 6. Set up the repository properly

### Topics (helps discovery)
Go to your repo page, click the gear icon next to **About**, and add topics:
```
go golang api-gateway reverse-proxy rate-limiting load-balancer circuit-breaker
microservices kubernetes prometheus devops
```

### Social preview image
Settings → Options → Social preview → Upload an image. Even a simple diagram of the architecture looks professional.

### Enable Discussions
Settings → Features → check **Discussions**. This gives you a place for Q&A that doesn't pollute Issues.

### Branch protection
Settings → Branches → Add branch protection rule for `main`:
- Require a pull request before merging
- Require status checks to pass (select the CI workflow)
- This signals to contributors that the project takes quality seriously

---

## 7. Submit to awesome-go

Open a PR to https://github.com/avelino/awesome-go

Find the **Networking** section and add:
```markdown
- [gateway-pro](https://github.com/snehakhoreja/gateway-pro) - Lightweight API gateway with rate limiting, load balancing, and circuit breaking.
```

Follow their contribution guide exactly — they reject PRs that don't. Wait 2–4 weeks for review.

---

## 8. Verify everything works

```bash
# Clone fresh copy and confirm quick start works
cd /tmp
git clone https://github.com/snehakhoreja/gateway-pro.git
cd gateway-pro
make build
./bin/gateway-pro -version

# Verify Docker image
docker run --rm snehakhoreja/gateway-pro:latest -version
```

If both of those work, you're ready to announce.
