# Unreleased

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
