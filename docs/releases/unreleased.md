# Unreleased

- Unconfirmed Agent and Provider mutations now have resource/operation-specific
  pending state. New execution and conflicting writes fail closed while
  unrelated resources continue. Inspect `GET .../{id}/mutation` and use
  `POST .../{id}/recover` under `/admin/agents` or `/admin/llm/providers` to
  verify persisted facts without another write. Recovery retries back off
  1–30 seconds. SQLite Agent readback is transactional; known failures roll back.
  Listener preparation/commit panics no longer leave the snapshot lock held.
  Token-only refresh avoids reentrant configuration coordination and cannot
  resurrect a concurrently revoked credential.

- Provider deletion and ManagedModel cleanup now commit together in SQLite.
  All composed-manager write entry points, including direct calls and Bundle,
  enforce Provider/Route/model references. Provider credentials also block their
  Provider's deletion and are never cascade-deleted. Agent resource declarations
  remain management links; HTTP credential revocation remains allowed. Failed
  credential deletion no longer removes runtime selection state prematurely.

- Non-null Agent `policy.budget` is rejected, including empty and all-zero
  objects, because budgets are not enforced. Stored records can prevent startup;
  remove them explicitly through offline recovery. Null/absent values remain
  accepted. Agent Refresh validates the complete candidate before publication and
  retains its last valid generation on failure. Resource declarations and the
  independent builtin/dispatcher depth limits retain their existing scope.

- Direct LLM targets store only `target_policy.provider_target.provider_id`.
  The outer `target_policy.provider_id` field and its normalization aliases have
  been removed. All new input rejects it, including empty/null/equal duplicates.
  Before upgrading existing data, inspect and explicitly convert it offline.
  Conflicting old provider values require an explicit replacement; a failed
  batch never partially converts records or silently changes upstream targets.

- Route configuration rejects equal-score intersecting matches across LLM, MCP
  and Agent routes, including static/dynamic ties and disabled entries. Static
  routes no longer win equal-score matches. Overlapping method sets count as
  intersections; native A2A retains ownership of errors for every HTTP method.
  Ambiguous runtime matches fail deterministically. Invalid Refresh retains the
  last valid configuration; invalid first startup fails with recovery guidance.
- Offline configuration inspection and explicit SQLite/static-file repair are
  available through `scripts/repair-architecture-config`, without online Admin.
  Repairs require stopped services, an unused backup path, and an explicit batch;
  conflicts never choose a route/provider automatically, and budgets are never
  silently removed. See the [recovery guide](../guides/architecture-config-recovery.md).
