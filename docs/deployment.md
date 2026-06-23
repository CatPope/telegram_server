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

배포 호스트(Ubuntu)에서 `ghcr.io/catpope/telegram_server`를 `docker pull` 할 사용자를 `<operator>` 로 표기한다. 운영 호스트에 여러 운영자 계정이 있다면 **각 계정이 아래 절차를 자기 사용자로 1회씩 반복**한다 (Docker 자격 증명은 `~/.docker/config.json`에 사용자별로 저장되므로 공유되지 않는다).

### 1. `<operator>` 를 `docker` 그룹에 가입 (호스트 root 1회 수행)

```bash
sudo usermod -aG docker <operator>
# 변경 사항이 적용되려면 <operator> 가 logout → login (또는 새 SSH 세션)
```

검증:

```bash
sudo -iu <operator> docker info >/dev/null && echo OK
```

### 2. GitHub PAT 발급

GHCR private 이미지를 받으려면 `read:packages` scope PAT가 필요하다.

- 발급: <https://github.com/settings/tokens/new> → Scopes: **`read:packages`** 만 체크 (쓰기·리포 권한 X).
- 여러 운영자 사이에서 같은 PAT를 공유해도 되고, 운영자별로 PAT를 분리해도 된다. 공유는 회전이 1회로 끝나는 운영 단순함, 분리는 GitHub 감사 로그에서 누가 무엇을 pull 했는지 구분 가능.

> 패키지를 **public**으로 전환하면 이 단계와 다음 단계 모두 불필요 — `docker pull`이 익명으로 동작. 단 CI에서 push할 때는 여전히 write 권한이 필요.

### 3. `<operator>` 에서 `docker login` (1회)

`<operator>` 로 호스트에 로그인하여:

```bash
echo "<PAT>" | docker login ghcr.io -u <github-user> --password-stdin
```

자격 증명이 `<operator>` 의 `~/.docker/config.json`에 저장된다 (`--password-stdin`을 쓰면 쉘 history에 토큰이 남지 않는다).

### 4. 검증

```bash
sudo -iu <operator> docker pull ghcr.io/catpope/telegram_server:latest
```

성공하면 digest가 출력된다. `unauthorized` / `denied` 를 받으면 `docker login` 을 안 거쳤거나 PAT가 만료된 것.

### 5. CI 자동 배포 사용자와의 관계

GitHub Actions가 SSH로 들어와 `docker compose pull`을 실행하는 사용자는 별도(`DEPLOY_SSH_USER`로 지정 — 일반적으로 `deploy` 같은 service account). 그 사용자에 대해서도 위 1·3단계를 동일하게 1회 수행해야 CI 강제 명령이 GHCR에서 이미지를 받을 수 있다.

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
| `DEPLOY_SSH_HOST` | 배포 호스트의 tailnet hostname(예: `deploy-host`) 또는 tailscale IP(`100.x.y.z`) |
| `DEPLOY_SSH_USER` | 배포 호스트의 OS 사용자명 (예: `deploy`) |
| `DEPLOY_SSH_PRIVATE_KEY` | ed25519 개인 키 파일의 전체 내용 |
| `DEPLOY_PATH` | `/opt/telegram_server` (선택 사항 — 강제 명령이 이 경로를 하드 코딩) |
| `TS_OAUTH_CLIENT_ID` | Tailscale OAuth client ID (아래 "Tailscale 접근" 섹션 참조) |
| `TS_OAUTH_SECRET` | Tailscale OAuth client secret |
| `TS_TAGS` | (선택) ephemeral 노드에 부여할 tag. 미설정 시 기본값 `tag:ci` |

`GITHUB_TOKEN`은 GitHub Actions에 의해 동일 소유자 GHCR 푸시를 위해 자동 주입된다. 다른 조직으로 푸시하지 않는 한 수동 설정이 필요하지 않다 (GHCR 풀 접근 위 `GHCR_PUSH_TOKEN` 대안 참조).

---

## Tailscale 접근 (tailnet-only 배포 호스트)

배포 호스트가 Tailscale tailnet 안에서만 도달 가능한 경우 (공인 인터넷에 SSH 포트를 열지 않음), GitHub Actions 러너는 매 워크플로 실행마다 **ephemeral 노드**로 tailnet에 join한 다음 SSH한다. `tailscale/github-action`이 이 역할을 수행한다.

### 작동 원리

1. `deploy.yml`의 `Tailscale up` 단계가 OAuth client credential로 ephemeral 인증키를 발급받고 `tailscaled`를 띄운다.
2. 러너 머신이 `tag:ci`가 부착된 ephemeral 노드로 tailnet에 등록된다.
3. 이후 SSH 단계와 `/healthz` curl이 tailnet DNS 또는 100.x.y.z 주소를 통해 배포 호스트로 도달한다.
4. 워크플로가 종료되면 ephemeral 노드는 자동으로 tailnet에서 제거된다.

### 운영자 절차 (한 번만 수행)

1. **OAuth client 생성** (Tailscale admin console)
   - 접속: <https://login.tailscale.com/admin/settings/oauth>
   - **Generate OAuth client...** 클릭
   - Scopes: `auth_keys` (write)
   - Tags: `tag:ci` (이 client가 발급하는 키가 부여 가능한 tag 목록)
   - 생성된 **Client ID + Secret**을 그 자리에서 복사한다 (secret은 한 번만 표시됨).

2. **ACL 정의 추가** (Tailscale admin console → Access Controls)

   ACL JSON에 다음 항목을 추가하여 `tag:ci`가 배포 호스트로 SSH할 수 있도록 허용한다. 배포 호스트는 자체 tag (예: `tag:deploy`) 또는 사용자 계정으로 식별한다.

   ```json
   {
     "tagOwners": {
       "tag:ci":     ["autogroup:admin"],
       "tag:deploy": ["autogroup:admin"]
     },
     "acls": [
       {
         "action": "accept",
         "src":    ["tag:ci"],
         "dst":    ["tag:deploy:22", "tag:deploy:80"]
       }
     ]
   }
   ```

   포트 22 = SSH, 포트 80 = `/healthz` curl. HTTPS termination을 Caddy로 종료하는 경우 443도 함께 허용.

3. **배포 호스트에 tag 부여**
   - 배포 호스트에서 `sudo tailscale up --advertise-tags=tag:deploy` 실행
   - admin console에서 해당 노드의 tag 승인

4. **GitHub repo Secrets 등록**
   - `TS_OAUTH_CLIENT_ID` ← 1단계의 Client ID
   - `TS_OAUTH_SECRET` ← 1단계의 Secret
   - `TS_TAGS` (선택) ← `tag:ci` (미설정 시 워크플로 기본값 사용)

5. **`DEPLOY_SSH_HOST` 값 변경**
   - 공인 IP/DNS에서 **tailnet hostname** (예: `deploy-host`) 또는 **tailscale IP** (`100.x.y.z`)로 교체

### 검증

OAuth + ACL이 올바르게 설정됐는지 확인하려면 `workflow_dispatch`로 deploy 워크플로를 수동 실행하거나, 임의의 commit을 main에 푸시한다. `Tailscale up` 단계 로그에 `Success.` 가 출력되고 다음 SSH 단계가 timeout 없이 진행되면 OK.

### 보안 메모

- ephemeral 노드는 워크플로 종료 시 자동 제거됨 → tailnet 영구 노드로 누적되지 않음.
- OAuth client의 scope은 `auth_keys` 발급에만 한정 → ACL 변경, 노드 강제 제거 등 다른 admin 권한 없음.
- `tag:ci`의 ACL은 SSH/healthz 포트만 허용 → 다른 tailnet 노드에 lateral movement 차단.

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
