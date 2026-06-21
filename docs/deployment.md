## Deployment Guide

This document describes how to prepare a deploy host, configure GHCR pull access, install the SSH forced-command key, and perform the first manual deploy before GitHub Actions automation takes over.

---

## Deploy Host Prep

**Requirements:** Ubuntu 22.04+, Docker Engine with the Compose plugin, and an open port 80 (or 443 with Caddy).

1. Install Docker Engine and the Compose plugin:

   ```bash
   curl -fsSL https://get.docker.com | sh
   apt-get install -y docker-compose-plugin
   ```

2. Create the working directory and copy required files:

   ```bash
   mkdir -p /opt/telegram_server
   # Copy docker-compose.yml from the repo into /opt/telegram_server/
   # Author .env manually — it is never committed to the repo (see below)
   ```

3. Author `.env` at `/opt/telegram_server/.env`. Minimum required variables:

   ```
   TELEGRAM_BOT_TOKEN=<real token from @BotFather>
   DATABASE_URL=postgres://telegram:<password>@postgres:5432/telegram_server?sslmode=disable
   ```

   The `.env` file stays on the deploy host only — it is gitignored and must never be committed.

---

## GHCR Pull Access

The deploy host must authenticate to pull images from `ghcr.io/catpope/telegram_server`.

1. Generate a GitHub Personal Access Token (PAT) with **read:packages** scope only (no write, no repo).
2. On the deploy host:

   ```bash
   docker login ghcr.io -u <github-user> -p <PAT-with-read:packages>
   ```

3. Docker stores the credential in `~/.docker/config.json`. The PAT needs only `read:packages` — it cannot push or modify packages.

> **Cross-org alternative:** If your GitHub Actions runner is in a different organisation than `catpope`, the built-in `GITHUB_TOKEN` cannot push to `ghcr.io/catpope/*`. In that case, create a PAT with `write:packages` scope, add it as the repo secret `GHCR_PUSH_TOKEN`, and replace `secrets.GITHUB_TOKEN` with `secrets.GHCR_PUSH_TOKEN` in the `docker/login-action` step inside `deploy.yml`.

---

## Caddy Reverse Proxy (HTTPS Termination)

The app binds to `127.0.0.1:8080` (loopback only). Caddy runs on the same host and terminates TLS:

```
# /etc/caddy/Caddyfile
bot.example.com {
    reverse_proxy localhost:8080
    tls operator@example.com
}
```

Install Caddy, place the Caddyfile, and reload:

```bash
apt-get install -y caddy
systemctl enable --now caddy
```

Caddy and the app share the host loopback (`127.0.0.1`). Port 8080 is never bound publicly.

---

## `authorized_keys` Install

This restricts the GitHub Actions deploy key to a single forced-command — no shell, no port-forwarding.

1. Generate the ed25519 keypair (no passphrase):

   ```bash
   ssh-keygen -t ed25519 -f deploy_key -N ''
   # Produces: deploy_key (private) and deploy_key.pub (public)
   ```

2. Copy `deploy/authorized_keys.template` from the repo. Replace:
   - `<PUBLIC_KEY_PAYLOAD>` with the contents of `deploy_key.pub` (everything after `ssh-ed25519 AAAA`)
   - `<user>` with the deploy OS user (e.g. `deploy`)

3. Install on the deploy host:

   ```bash
   install -m 700 -d /home/deploy/.ssh
   install -m 600 authorized_keys.template /home/deploy/.ssh/authorized_keys
   ```

4. Add the private key to the GitHub repo as a secret named `DEPLOY_SSH_PRIVATE_KEY`:

   ```bash
   # Copy the full contents of deploy_key (the private key file)
   # Paste into: GitHub repo → Settings → Secrets and variables → Actions → New repository secret
   # Name: DEPLOY_SSH_PRIVATE_KEY
   ```

   Delete the local keypair files after storing them safely.

---

## GitHub Secrets Required

Configure these in the GitHub repo under **Settings → Secrets and variables → Actions**:

| Secret | Value |
|---|---|
| `DEPLOY_SSH_HOST` | IP address or DNS name of the deploy host |
| `DEPLOY_SSH_USER` | OS username on the deploy host (e.g. `deploy`) |
| `DEPLOY_SSH_PRIVATE_KEY` | Full contents of the ed25519 private key file |
| `DEPLOY_PATH` | `/opt/telegram_server` (optional — the forced-command hard-codes this path) |

`GITHUB_TOKEN` is injected automatically by GitHub Actions for same-owner GHCR push. No manual setup is required unless pushing to a different organisation (see GHCR Pull Access above for the `GHCR_PUSH_TOKEN` alternative).

---

## First Deploy (Pre-mortem #3 Bootstrap)

On the very first deploy, `:previous` does not exist in GHCR. `deploy.yml` unconditionally tags `:previous` after a successful `/healthz`, which bootstraps the rollback target.

1. **(Option A — let CI drive it):** Push a commit to `main`. `ci.yml` runs lint + test, then `deploy.yml` publishes the image and SSHs to the deploy host. The forced-command runs `docker compose pull && up -d`, then `/healthz` is checked from the runner. On success, both `:latest` and `:previous` are tagged to the same SHA.

2. **(Option B — manual first run):** SSH to the deploy host and run:

   ```bash
   cd /opt/telegram_server
   docker compose pull
   docker compose up -d
   curl -sf http://localhost/healthz
   ```

   Then push any commit to `main` to let `deploy.yml` handle the `:previous` tagging automatically.

3. Verify bootstrap is complete:

   ```bash
   docker buildx imagetools inspect ghcr.io/catpope/telegram_server:previous
   ```

   Both `:latest` and `:previous` should appear pointing to the same digest.
