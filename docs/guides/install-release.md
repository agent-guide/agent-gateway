# Install a published release

Use the container first-run path for evaluation and a platform archive when you
need host binaries. Release identity comes from each binary's `manifest`
command, not from `agw version`, which reports the embedded Caddy version.

## Download and verify an archive

Choose one asset from the v0.7.2 release:

| Host | Archive |
| --- | --- |
| Linux amd64 | `agent-gateway-v0.7.2-linux-amd64.tar.gz` |
| Linux arm64 | `agent-gateway-v0.7.2-linux-arm64.tar.gz` |
| macOS amd64 | `agent-gateway-v0.7.2-darwin-amd64.tar.gz` |
| macOS arm64 | `agent-gateway-v0.7.2-darwin-arm64.tar.gz` |

Download the selected archive and `SHA256SUMS` from the same release. For
example:

```sh
asset=agent-gateway-v0.7.2-darwin-arm64.tar.gz
base=https://github.com/agent-guide/agent-gateway/releases/download/v0.7.2
curl -LO "$base/$asset"
curl -LO "$base/SHA256SUMS"
grep " $asset\$" SHA256SUMS | shasum -a 256 -c -
tar -xzf "$asset"
cd "${asset%.tar.gz}"
```

Use `sha256sum -c -` instead of `shasum -a 256 -c -` when that is the native
tool. Reject the download if the checksum line is absent or verification fails.

## Verify release identity

```sh
./agw manifest
./agwd manifest
./agwctl manifest
```

`agw` and `agwd` must report the Community runtime projection; `agwctl` reports
the Community validation projection. They are intentionally different module
sets. In every output, `edition.version` and every included Community module
version must be `0.7.2`.

## Install the binaries

Place all three binaries in one operator-controlled executable directory. Keep
the archive's `RELEASE-METADATA.json` with deployment records; it identifies the
source commit, Go version, release version, and target tuple.

For standalone mode, provide a persistent writable directory and bind the
Admin listener to loopback unless an authenticated deployment boundary protects
it:

```sh
mkdir -p ./data
hash=$(./agw hash-password --plaintext 'replace-this-password')
AGW_ADMIN_BASIC_AUTH_HASH="admin:$hash" \
  ./agwd --addr 127.0.0.1:8080 \
  --admin-addr 127.0.0.1:8019 \
  --config-store ./data/configstore.db
```

Management clients use the complete plaintext credential:

```sh
export AGW_ADMIN_BASIC_AUTH='admin:replace-this-password'
./agwctl --admin-addr http://127.0.0.1:8019 virtualkey list
```

The public `GET /admin/health` probe intentionally does not require Admin
credentials. All other Admin routes do. See [Admin API auth](admin-auth.md).

## Run the container

The production image defaults to `agwd`, runs as fixed non-root UID/GID 10001,
and expects persistent state under `/var/lib/agent-gateway`. It contains no
provider credential and does not include the external `agw-auth` executable.
The [first-run guide](../getting-started/first-run.md) is the supported Compose
example, including read-only root filesystems and a writable data volume.

Use an immutable digest in managed deployments. The version tag is
`ghcr.io/agent-guide/agent-gateway:v0.7.2`; release notes record the verified
digest. Select `agw` explicitly for Caddy configurations or `agwctl` for a
management job.

## External credential refresh

Managed `oauth_token` credentials require a separately installed `agw-auth` or
another compatible refresh command. Put it on the gateway process `PATH`, or
configure the full command and arguments. The Community container does not
embed it; mount or build a controlled derived image when OAuth refresh is
needed. See [OAuth credentials](oauth-credentials.md).

## Prepare upgrades and rollback

Before replacement:

1. read every intervening release note and the [upgrade guide](upgrade.md);
2. back up the SQLite database while no writer is active, or use a
   SQLite-consistent backup operation;
3. export the current Bundle and retain the current binaries or image digest;
4. record the active runtime and validation manifests;
5. verify the new archive, then validate configuration before switching traffic.

If verification fails, stop the new process, restore the database snapshot when
the attempted version wrote to it, and start the recorded prior binary or image
digest. Do not assume a newer database can always be opened safely by an older
binary.
