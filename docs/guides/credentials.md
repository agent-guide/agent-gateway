# Credentials

This guide covers managed upstream credentials, how they differ from provider `api_key` config, and how the gateway selects them at runtime.

## What A Managed Credential Is

A managed credential is a persisted upstream credential object stored in the gateway config store and used by the credential manager and scheduler.

It is separate from:

- caller-facing VirtualKeys
- provider block `api_key` fallback config

Managed credentials are for upstream provider authentication. VirtualKeys are for gateway clients.

## Credential Types

Current built-in credential types are:

- `api_key`
- `cliauth_token`

`api_key` is the normal upstream API-key credential form.

`cliauth_token` is used for upstream credentials produced by CLI-auth login flows such as `codex`, `claudecode`, or `gemini`.

## Core Credential Fields

Current persisted credential fields include:

- `id`
- `provider_type`
- `provider_id`
- `type`
- `label`
- `scope`
- `attributes`
- `metadata`
- `disabled`

Important attribute examples:

- `api_key`
- `base_url`
- `priority`

Important metadata examples:

- `refresh_name`
- `refresh_expiry_delta`
- token or refresh-token data for CLI-auth-backed credentials

## Default Scope Behavior

Credential scheduling always uses a scope key.

If you do not provide `scope`, the credential manager defaults it to:

- `id:<provider_id>`

Examples:

- `id:openai-main`
- `id:tenant-a`
- `type:openai`

The `type:<provider_type>` form is also supported and is commonly used as an explicit CLI-auth login scope.

## How Routes Select Credentials

Routes do not hardcode one credential ID. They select credentials by:

- resolved provider
- credential scope
- credential type
- route credential selection policy

Current default route behavior is:

- `credential_type_order`: `api_key`, then `cliauth_token`
- `credential_selector`: `round_robin`

For direct-provider routes, the default credential scope order is:

- `provider_id`

For logical-model routes, if the route still uses the default provider-scope-only setting, the runtime expands that to:

- `model_custom`
- `provider_id`

That means logical-model routes can try a model-specific managed-model scope first, then fall back to the provider scope.

## Provider `api_key` Versus Managed Credentials

Provider block `api_key` values are provider-local fallback configuration. They do not register as managed credentials and do not participate in scheduler selection.

Use provider `api_key` when:

- one static upstream secret is enough
- you do not need scheduling, rotation, or refresh behavior

Use managed credentials when:

- you need multiple upstream credentials
- you need scheduling or priority behavior
- you need CLI-auth-backed refreshable tokens

## Credential Creation Examples

Create an API-key credential:

```bash
curl -X POST http://localhost:8019/admin/credentials \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "provider_id": "openai-main",
    "label": "primary",
    "attributes": {
      "api_key": "sk-...",
      "base_url": "https://api.openai.com/v1",
      "priority": "10"
    }
  }'
```

Create a CLI-auth-token credential directly:

```bash
curl -X POST http://localhost:8019/admin/credentials \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "cliauth_token",
    "provider_type": "openai",
    "provider_id": "openai-main",
    "scope": "type:openai",
    "label": "codex-shared"
  }'
```

In most cases, CLI-auth-token credentials should be produced through the CLI-auth login flow instead of being hand-authored.

## Operational Notes

- disabled credentials remain persisted but are excluded from scheduling
- credential priority is read from `attributes.priority`
- CLI-auth-backed credentials can refresh through `metadata.refresh_name`
- runtime scheduler state can mark credentials temporarily unavailable without changing the persisted definition

## Related Docs

- [cli-auth.md](cli-auth.md)
- [../reference/admin-api-reference.md](../reference/admin-api-reference.md)
- [../reference/agwctl-reference.md](../reference/agwctl-reference.md)
