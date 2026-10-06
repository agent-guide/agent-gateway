# Plans

This section holds time-bound execution plans, as opposed to the durable
design and decision documents in `design/`. Each plan targets a specific
version line or implementation effort, is verified against a specific tree
snapshot, and becomes obsolete by design once its work lands.

A plan explains work that is actively being prepared or executed. It includes
an objective, baseline, ordered work packages, acceptance gates, documentation
impact, and an explicit deletion condition. Unscheduled possibilities belong
in a `Proposed` design without an implementation checklist.

Lifecycle convention:

- a plan states the version line or effort it targets
- when the code and the plan disagree, the code wins and the plan is wrong
- when every item in a plan is either completed or explicitly re-homed, and
  the permanent documents (`design/`, `architecture/`, `README.md`,
  `AGENTS.md`, and release notes) describe the resulting behavior, the plan is
  deleted — git history is the archive

Current plans:

- [v0.6.1-stabilization.md](v0.6.1-stabilization.md): patch-release
  verification, a self-contained A2A example, process smoke coverage, and
  SQLite upgrade tests
- [v0.7.0-community-guardrails.md](v0.7.0-community-guardrails.md): first
  Community Guardrails implementation slice for provider-facing chat requests
