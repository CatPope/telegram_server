## 배포 가이드

이 문서는 배포 호스트를 준비하고, GHCR 풀 접근을 구성하고, SSH 강제 명령 키를 설치하고, GitHub Actions 자동화가 인수받기 전에 첫 번째 수동 배포를 수행하는 방법을 설명한다.

---

## 배포 호스트 준비

**요구사항:** Ubuntu 22.04+, Docker Engine (Compose 플러그인 포함), 그리고 개방된 포트 80 (또는 Caddy를 사용하는 443).

1. Docker Engine 및 Compose 플러그인을 설치한다:

   ```bash
   curl -fsSL https://get.docker.com | sh
   apt-get install -y docker-compose-plugin
   ```

2. 작업 디렉토리를 생성하고 필수 파일을 복사한다:

   ```bash
   mkdir -p /opt/telegram_server
   # 리포지토리에서 docker-compose.yml을 /opt/telegram_server/로 복사
   # .env를 수동으로 작성 — 리포지토리에 커밋되지 않음 (아래 참조)
   ```

3. `/opt/telegram_server/.env`에서 `.env`를 작성한다. 최소 필수 변수:

   ```
   TELEGRAM_BOT_TOKEN=<real token from @BotFather>
   DATABASE_URL=postgres://telegram:<password>@postgres:5432/telegram_server?sslmode=disable
   ```

   `.env` 파일은 배포 호스트에만 유지된다 — gitignored이고 절대 커밋되지 않아야 한다.

---

## GHCR 풀 접근

배포 호스트는 `ghcr.io/catpope/telegram_server`에서 이미지를 풀하기 위해 인증해야 한다.

1. **read:packages** 범위만 있는 GitHub Personal Access Token(PAT)을 생성한다 (쓰기 없음, 리포 없음).
2. 배포 호스트에서:

   ```bash
   docker login ghcr.io -u <github-user> -p <PAT-with-read:packages>
   ```

3. Docker는 자격 증명을 `~/.docker/config.json`에 저장한다. PAT는 `read:packages`만 필요하다 — 패키지를 푸시하거나 수정할 수 없다.

> **교차 조직 대안:** GitHub Actions 러너가 `catpope`와 다른 조직에 있는 경우, 기본 제공 `GITHUB_TOKEN`은 `ghcr.io/catpope/*`로 푸시할 수 없다. 이 경우 `write:packages` 범위를 사용하여 PAT를 생성하고, 리포 시크릿 `GHCR_PUSH_TOKEN`으로 추가하고, `deploy.yml` 내 `docker/login-action` 단계에서 `secrets.GITHUB_TOKEN`을 `secrets.GHCR_PUSH_TOKEN`으로 바꾼다.

---

## Caddy 역방향 프록시 (HTTPS 종료)

앱은 `127.0.0.1:8080` (루프백만)에 바인딩된다. Caddy는 동일 호스트에서 실행되고 TLS를 종료한다:

```
# /etc/caddy/Caddyfile
bot.example.com {
    reverse_proxy localhost:8080
    tls operator@example.com
}
```

Caddy를 설치하고, Caddyfile을 배치하고, 재로드한다:

```bash
apt-get install -y caddy
systemctl enable --now caddy
```

Caddy와 앱은 호스트 루프백 (`127.0.0.1`)을 공유한다. 포트 8080은 절대 공개적으로 바인딩되지 않는다.

---

## `authorized_keys` 설치

이는 GitHub Actions 배포 키를 단일 강제 명령으로 제한한다 — 쉘 없음, 포트 포워딩 없음.

1. ed25519 키쌍을 생성한다 (암호 없음):

   ```bash
   ssh-keygen -t ed25519 -f deploy_key -N ''
   # 생성: deploy_key (개인) 및 deploy_key.pub (공개)
   ```

2. 리포지토리에서 `deploy/authorized_keys.template`을 복사한다. 바꾼다:
   - `<PUBLIC_KEY_PAYLOAD>`를 `deploy_key.pub` (파일 내용, `ssh-ed25519 AAAA` 이후)의 내용으로
   - `<user>`를 배포 OS 사용자로 (예: `deploy`)

3. 배포 호스트에 설치한다:

   ```bash
   install -m 700 -d /home/deploy/.ssh
   install -m 600 authorized_keys.template /home/deploy/.ssh/authorized_keys
   ```

4. 개인 키를 GitHub 리포지토리에 `DEPLOY_SSH_PRIVATE_KEY`라는 시크릿으로 추가한다:

   ```bash
   # deploy_key (개인 키 파일)의 전체 내용 복사
   # 붙여 넣기: GitHub 리포 → Settings → Secrets and variables → Actions → New repository secret
   # Name: DEPLOY_SSH_PRIVATE_KEY
   ```

   안전하게 저장한 후 로컬 키쌍 파일을 삭제한다.

---

## GitHub 시크릿 필수

GitHub 리포의 **Settings → Secrets and variables → Actions**에서 이들을 구성한다:

| 시크릿 | 값 |
|---|---|
| `DEPLOY_SSH_HOST` | 배포 호스트의 IP 주소 또는 DNS 이름 |
| `DEPLOY_SSH_USER` | 배포 호스트의 OS 사용자명 (예: `deploy`) |
| `DEPLOY_SSH_PRIVATE_KEY` | ed25519 개인 키 파일의 전체 내용 |
| `DEPLOY_PATH` | `/opt/telegram_server` (선택 사항 — 강제 명령이 이 경로를 하드 코딩) |

`GITHUB_TOKEN`은 GitHub Actions에 의해 동일 소유자 GHCR 푸시를 위해 자동 주입된다. 다른 조직으로 푸시하지 않는 한 수동 설정이 필요하지 않다 (GHCR 풀 접근 위 `GHCR_PUSH_TOKEN` 대안 참조).

---

## 첫 번째 배포 (사전 사망 #3 부트스트랩)

맨 처음 배포에서 `:previous`는 GHCR에 존재하지 않는다. `deploy.yml`은 성공적인 `/healthz` 후 조건 없이 `:previous`를 태그하여, 롤백 대상을 부트스트랩한다.

1. **(옵션 A — CI 구동):** `main`으로 커밋을 푸시한다. `ci.yml`은 린트 + 테스트를 실행한 후, `deploy.yml`은 이미지를 공개하고 배포 호스트로 SSH한다. 강제 명령은 `docker compose pull && up -d`를 실행한 후, 러너에서 `/healthz`가 확인된다. 성공 시, `:latest`와 `:previous` 모두 동일 SHA로 태그된다.

2. **(옵션 B — 수동 첫 실행):** 배포 호스트로 SSH하고 실행한다:

   ```bash
   cd /opt/telegram_server
   docker compose pull
   docker compose up -d
   curl -sf http://localhost/healthz
   ```

   그런 다음 `main`으로 임의의 커밋을 푸시하여 `deploy.yml`이 `:previous` 태깅을 자동으로 처리하도록 한다.

3. 부트스트랩이 완료되었는지 확인한다:

   ```bash
   docker buildx imagetools inspect ghcr.io/catpope/telegram_server:previous
   ```

   `:latest`와 `:previous` 모두 동일 다이제스트를 가리키는 것으로 나타나야 한다.
