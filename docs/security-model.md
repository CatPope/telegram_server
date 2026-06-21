# Security Model

## Bearer Authentication

All API requests (both `/v1/*` and `/admin/*`) are authenticated via a Bearer token in the `Authorization` header.

### Token Format

```
tg_<prefix>_<secret>
```

- `prefix`: short opaque identifier used for O(1) DB lookup (indexed `key_prefix` column in `app_keys`).
- `secret`: high-entropy random bytes; never stored in plaintext.

### Resolution Flow (per request)

1. Parse `key_prefix` from the Bearer token (splits on first `_` after `tg_`).
2. `SELECT k.app_id, k.key_hash, a.capability_set_version FROM app_keys k JOIN apps a ...` filtered by `key_prefix`, `revoked_at IS NULL`, `a.active = true`.
3. For each candidate row, verify the full Bearer token against `key_hash` using **Argon2id** (`VerifyAPIKey`). Only the first matching row is accepted.
4. Load all capabilities for the matched `app_id` from `app_capabilities`.
5. Freeze `RequesterIdentity{AppID, Capabilities, CapabilitySetVer, KeyPrefix}` into the request context for the lifetime of the request.

### Argon2id Parameters

Configured in `internal/auth/argon2.go`. Key derivation is intentionally slow to resist offline brute-force. The prefix lookup narrows the candidate set before the expensive verify step.

---

## `capability_set_version` Semantics

### What it is

Every `apps` row carries an integer `capability_set_version` (default `1`). It is incremented by 1 inside the same transaction whenever `PATCH /admin/apps/{id}` adds or removes capabilities.

### Lifecycle

| Event | Effect on `capability_set_version` |
|---|---|
| `POST /admin/apps` (create) | Set to `1` |
| `PATCH /admin/apps/{id}` with no capability changes | Unchanged |
| `PATCH /admin/apps/{id}` with `add_capabilities` or `remove_capabilities` | `capability_set_version = capability_set_version + 1` (atomic, same tx as the INSERT/DELETE on `app_capabilities`) |

### Request-grain freezing

At request entry (`middleware.Auth`), `capability_set_version` is resolved alongside the key hash and frozen into `RequesterIdentity.CapabilitySetVer`. This value is **not re-read** during the request. All audit rows emitted within the same request carry the same `capability_set_ver` value.

This implements **Pre-mortem #7 mitigation**: a capability mutation that races with an in-flight request does not partially affect it — the frozen version reflects the state at authentication time, and the audit trail is self-consistent per request.

### Forensic query

To determine what capabilities app `X` had at the time of trace `T`:

```sql
SELECT capability_set_ver
FROM audit_log
WHERE trace_id = 'T' AND app_id = 'X'
LIMIT 1;
```

Then cross-reference against `app_capabilities` (current state). **Limitation**: historical snapshots of `app_capabilities` are not currently retained. The version number establishes a monotonic ordering and flags when capabilities changed, but the exact set at a past version requires an external snapshot store (out of scope for Phase 4).

---

## Admin API Capability Layout

| Endpoint | Method | Required Capability | Effect |
|---|---|---|---|
| `/admin/apps` | POST | `apps.register` | Create a new app with initial capabilities |
| `/admin/apps/{id}` | PATCH | `apps.register` | Update app metadata / add / remove capabilities (bumps `capability_set_version` if caps change) |
| `/admin/apps/{id}` | DELETE | `apps.register` | Soft-delete app (`active = false`) |
| `/admin/users/{telegram_id}` | PATCH | `users.promote` | Set user grade (`user` / `developer` / `admin`) |
| `/admin/users/{telegram_id}/subscriptions/{app_id}` | POST | `apps.register` | Subscribe user to app (no topic provisioning — user must run `/apps`) |
| `/admin/users/{telegram_id}/subscriptions/{app_id}` | DELETE | `apps.register` | Remove user subscription |
| `/admin/audit/search` | GET | `audit.search` | Search audit log with filters |

All admin endpoints share the same Auth + RateLimit middleware stack as `/v1/*`.

---

## Pre-mortem #7 Mitigation: Concurrent Capability Mutation

**Threat**: An operator mutates capabilities (adds/removes) while a long-running request is in flight. Without a frozen version, different audit stages within the same trace could reflect different capability sets, making forensic analysis ambiguous.

**Mitigation implemented**:

1. `capability_set_version` is fetched atomically with the key hash at request entry and stored in `RequesterIdentity.CapabilitySetVer`.
2. Every `audit.Event` emitted during the request carries `CapabilitySetVer: id.CapabilitySetVer` — the same frozen value.
3. The `PATCH /admin/apps/{id}` capability mutation (INSERT/DELETE on `app_capabilities` + `capability_set_version` bump) runs inside a single `pgx.Tx`, so the version is always consistent with the actual capability set in the DB.

**Residual risk**: The frozen `CapabilitySetVer` reflects the version at auth time. If capabilities are mutated between auth and the actual capability check (`RequireCapability` middleware), the request proceeds with the pre-mutation capability set. This is acceptable: the window is one middleware-chain traversal; under contention this can include rate-limit wait time, and the audit row records which version was active. A capability revocation takes full effect on the next request.

---

## Phase 4 known limitations

These were surfaced in Phase 4 review and are explicitly deferred to a later phase:

- **Rate-limit policy hot-reload not implemented.** `internal/ratelimit/policy_loader.go` loads `rate_limit_policies` only at boot. PATCH to `rate_limit_policies` is not currently exposed as an admin endpoint; even if exposed, the limiter would not pick up changes until process restart. Capability mutations are immediately visible (capability_set_version bumps on next request entry).
- **PATCH /admin/apps lacks optimistic concurrency.** Two admins reading version=N and each writing distinct mutations will both succeed; the version still advances monotonically but each admin's intent may be partially overwritten by the other. Operator practice: serialize admin writes externally (one operator at a time).
- **/admin/users/{telegram_id}/subscriptions/{app_id} is gated by `apps.register`.** Any key with `apps.register` can also force-subscribe any user to any app — this is intentional (admin-tier authority over app/user matrix) but worth re-evaluating if a separate `subscriptions.write` capability is needed in Phase 6.
- **pre-Phase-4 audit_log rows have `capability_set_ver = NULL`.** Forensic queries filtering by version must explicitly handle the NULL case as "pre-Phase-4 (history)".
