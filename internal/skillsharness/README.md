# skillsharness

Go package providing a fixture-mode test harness for the five Phase-5 skills.

## Modes

### Fixture mode (default — no creds required — primary CI path)

Fixture mode replays a pre-authored JSON transcript against a running
`telegram_server` instance. No `CLAUDE_API_KEY` or external API access is
needed. This is the mode used in CI.

Each transcript in `transcripts/<skill>.json` describes:
- The HTTP calls to make (method, path, body, expected status, body assertions)
- The mocktelegram side-effects to verify via `GET /test/calls`

### Live mode (CLAUDE_API_KEY required — deferred to Phase 6)

Live mode is a stub. When `CLAUDE_API_KEY` is unset, `RunLive` returns an
error immediately (no network calls made). When `CLAUDE_API_KEY` is set, it
returns an error explaining that full claude-CLI subprocess plumbing is
deferred to Phase 6.

## How to run

### Fixture tests (skipped when TELEGRAM_SERVER_URL unset)

```sh
# With a running server:
TELEGRAM_SERVER_URL=http://localhost:8080 go test ./internal/skillsharness/...

# With both server and mocktelegram (for sendMessage assertions):
TELEGRAM_SERVER_URL=http://localhost:8080 \
MOCKTELEGRAM_URL=http://localhost:8090 \
go test ./internal/skillsharness/...
```

### Plain go test (no server — skips fixture tests, runs guard tests)

```sh
go test ./internal/skillsharness/...
```

The localhost guard test (`TestLocalhostGuard`) always runs — no server needed.
The fixture tests call `t.Skip` when `TELEGRAM_SERVER_URL` is unset.
The live-mode stub test (`TestSkillLiveSkipsWithoutAPIKey`) always runs.

## Localhost-only constraint (Plan Risk #7)

`TestLocalhostGuard` enforces that every transcript:
1. Uses server-relative paths (starting with `/`) in `http_calls[].path` — no scheme.
2. Does not embed any URL pointing to a non-loopback host in `env` values or JSON bodies.

CI fails if a transcript ever references an external host. This prevents
accidental credential leakage or external API calls during test runs.

## Transcript schema

```json
{
  "skill": "<skill-name>",
  "description": "<human-readable scenario>",
  "env": {
    "TELEGRAM_API_KEY": "<bearer-token>"
  },
  "http_calls": [
    {
      "method": "POST",
      "path": "/v1/messages/direct",
      "body": { "...": "..." },
      "expected_status": 200,
      "assert_body_contains": ["\"delivered\""]
    }
  ],
  "expected_mocktelegram_calls": [
    { "method": "sendMessage", "min_count": 1 }
  ]
}
```

## Adding a new transcript

1. Create `transcripts/<skill>.json` following the schema above.
2. Add a `TestSkill<Name>Fixture` function in `harness_test.go`.
3. Run `go test ./internal/skillsharness/...` to verify the guard passes.
