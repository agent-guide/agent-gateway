# Unreleased

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
