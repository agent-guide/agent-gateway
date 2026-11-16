# Configure an HTTP API connector

Use [the order API Bundle](../../examples/gateway.bundle.http-api.yaml) as a
starting point. Set `ORDERS_API_URL` to the API's fixed HTTPS origin, including
any common path prefix. Static mappings and descriptions are exportable
configuration; keep authentication material in the referenced credential.

Choose the Connector `id` once, such as `order-api`. It is the stable identifier
used by Agents, MCP routes, credential scopes and approval entries. Change
`name` to update its display label; names may repeat. Connector IDs cannot be
renamed, and there is no separate `slug`. On update, omit the body ID or keep it
equal to the ID in the Admin URL. A different ID is rejected with HTTP 400.

Create an enabled `api_key` credential with ID `orders-token`, scope
`connector:order-api`, empty `provider_type`/`provider_id`, and the API key in
`attributes.api_key`. Provision it through `/admin/credentials`. Then apply:

```sh
agwctl validate -f examples/gateway.bundle.http-api.yaml
agwctl apply -f examples/gateway.bundle.http-api.yaml
agwctl connector tools order-api
```

The connector can be created before its credential. Inspect
`GET /admin/connectors/order-api/state` for local readiness. Disabled, missing,
wrong-owner and unusable credentials fail closed at invocation. Another
credential in the same scope is never an implicit fallback.

To use the tool in a builtin Agent, include `order-api` in
`resources.connector_ids` and select `connector_id: order-api` with
`tools: [get_order]`. Its model node must expose unique tool names. Interactive
approval uses `order-api/get_order`; loading a skill does not execute a tool.

The example publishes the same connector at `/mcp/orders`. Create a VirtualKey
allowing route `orders-tools` for external clients. Ingress VirtualKeys and
business-API credentials are separate. HTTP connectors advertise tools only;
resources, prompts and upstream sessions are unsupported.

Connector destinations are trusted by default: HTTP, HTTPS, custom ports and
internal addresses work without a policy file. Connector management must be
restricted to trusted administrators. Upstream credentials and ingress
VirtualKeys are independent of this network policy.

For a restricted deployment, enable the host-owned `connector_egress` policy.
It applies equally to HTTP API connectors and MCP HTTP/SSE connections:

```caddyfile
agent_gateway {
    connector_egress {
        mode public_only
        allow_origin http://orders.internal:8080
        allow_origin https://mcp.internal
    }
}
```

Caddy JSON uses `"connector_egress": {"mode": "public_only", "allow_origins":
["http://orders.internal:8080", "https://mcp.internal"]}`. Standalone uses
`--connector-egress-mode public_only` with repeatable
`--connector-egress-allow-origin http://orders.internal:8080` flags.

Omitted mode or `unrestricted` permits all administrator-configured HTTP(S)
destinations. `public_only` permits public HTTPS on port 443; each `allow_origin`
grants an exact scheme, host and effective port, including access to private
addresses. Paths, credentials, queries, fragments and wildcards are not origin
selectors. No CIDR configuration is needed. Hostnames are case-insensitive and
omitted ports use 80/443. IPv6 literal origins use brackets.

Connector creation and execution-configuration updates check destination
admission without DNS. Each network request resolves DNS again, checks every
answer and dials an admitted address directly, including MCP redirects and SSE
POST endpoints. HTTP API tools continue to reject redirects. MCP stdio execution
is outside this network policy. Connector/Agent/Bundle/Admin configuration cannot
relax host policy. Invalid policy fails host preparation. Apply policy changes
through host reload or restart.

When upgrading, remove `http_api_policy_file` / `--http-api-policy-file` and
translate required destinations into `allow_origin` entries. The old policy
file schema is removed. HTTP execution/queue limits retain fixed defaults:
128/256 globally and 16/32 per connector. The new default allows internal
addresses; configure `public_only` explicitly if outbound isolation is required.

Tool failures expose bounded categories. When a POST result is unconfirmed,
verify the business operation through the API's own reconciliation mechanism
before submitting it again. The gateway does not retry HTTP tools.

Query logical tool history through `/admin/metrics/mcp/events`, optionally
filtering by `connector_type=http_api`. HTTP summaries remain queryable with
OTLP disabled. Enable `http_client_spans` inside the Caddy metrics/OTLP block,
or `--metrics-otlp-http-client-spans`, to export tracing-only HTTP children.

Before changing connection/tool settings on the same connector ID, first remove
Agent declarations and tool selections, then remove or retarget its MCP routes.
Wait for calls and remote discovery/initialization to finish before submitting
the change, then restore references. Merely removing a route does not finish
requests already admitted through it. Stop direct Admin tool/discovery activity
while carrying out these maintenance steps.

For a staged replacement, create another connector under a new ID with the new
configuration, move references one object at a time, and delete the old connector
after its references are removed and calls finish. This also applies to adding,
removing or renaming tools in a referenced connector. Changing display name or
description does not require moving references. Do not change its ID/type.
Bundle apply updates objects individually; earlier successes remain committed
when another update fails. Use normal single-resource mutation inspection and
recovery for an uncertain write before retrying.
