# Agent Gateway release archive

This archive contains the Agent Gateway Community binaries for one platform:

- `agw` runs the Caddy-based gateway;
- `agwd` runs the standalone gateway and is the simplest server entry point;
- `agwctl` validates and applies Gateway Bundles through the Admin API.

The checksum file is distributed next to the archive, not inside it. Before
installing, extract the archive, enter this directory, and verify the original
archive from its parent download directory:

```sh
archive="$(basename "$PWD").tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  (cd .. && grep " $archive\$" SHA256SUMS | sha256sum -c -)
else
  (cd .. && grep " $archive\$" SHA256SUMS | shasum -a 256 -c -)
fi
```

Reject the archive if the checksum line is absent or either command fails. Then
verify the Agent Gateway release identity with:

```sh
./agw manifest
./agwd manifest
./agwctl manifest
```

`agw` and `agwd` report the Community runtime manifest. `agwctl` reports the
smaller Community validation manifest. Their `edition.version` and included
module versions must be `0.7.1`. The upstream `agw version` command reports the
embedded Caddy version and is not the Agent Gateway release identity.

For installation, Admin authentication, data mounts, upgrades, and rollback,
see https://agentguide.online and the versioned documentation at
https://github.com/agent-guide/agent-gateway/tree/v0.7.1/docs.
