# Security policy

## Reporting a vulnerability

Use the repository's **Security → Report a vulnerability** flow to open a
[private GitHub security advisory](https://github.com/agent-guide/agent-gateway/security/advisories/new).
Do not disclose a suspected vulnerability in a public issue, discussion, pull
request, or log excerpt.

Include the affected version or commit, deployment mode, impact, reproduction
steps, and any known mitigation. Remove credentials, access tokens, private
URLs, and unrelated user data from the report.

The maintainers aim to acknowledge a complete report within five business days.
Validation, remediation, coordinated disclosure, and release timing depend on
severity and reproducibility. Please allow time for a fix before publishing
details.

## Supported versions

Security fixes are evaluated for the latest published release. Older releases
may require upgrading rather than receiving a backport. The exact current
release and any security-specific exception are recorded in GitHub Releases.

## Deployment boundary

Agent Gateway is self-hosted. Operators remain responsible for network
exposure, TLS, Admin API authentication, provider and adapter credentials,
container/runtime hardening, and timely dependency updates. `GET /admin/health`
is intentionally public on the Admin listener; keep that listener on loopback or
behind an appropriate deployment boundary.
