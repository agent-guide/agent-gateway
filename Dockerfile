FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.26.11-alpine3.23 AS build

ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags=-buildid= -o /out/agw ./cmd/agw && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags=-buildid= -o /out/agwd ./cmd/agwd && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags=-buildid= -o /out/agwctl ./cmd/agwctl

FROM docker.io/library/alpine:3.23 AS acp

RUN apk add --no-cache npm

# Require Codex's native package at the same version as the JS launcher.
# Use OpenCode's baseline musl build on x86-64 for older CPUs.
RUN alpine_arch="$(apk --print-arch)" && \
    case "$alpine_arch" in \
        aarch64) codex_arch=arm64; opencode_package=opencode-linux-arm64-musl ;; \
        x86_64) codex_arch=x64; opencode_package=opencode-linux-x64-baseline-musl ;; \
        *) echo "ACP runtimes are unavailable for Alpine architecture $alpine_arch" >&2; exit 1 ;; \
    esac && \
    codex_version=0.172.0 && \
    npm install --prefix /opt/codex --no-save --no-audit --no-fund \
        @agentclientprotocol/codex-acp@2.12.1 "@openai/codex@$codex_version" \
        "@openai/codex-linux-$codex_arch@npm:@openai/codex@$codex_version-linux-$codex_arch" && \
    npm install --global --prefix /opt/opencode --ignore-scripts --no-audit --no-fund \
        "${opencode_package}@1.19.6" && \
    ln -s "lib/node_modules/$opencode_package/bin/opencode" /opt/opencode/opencode

FROM docker.io/library/alpine:3.23

ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Agent Gateway" \
      org.opencontainers.image.description="Self-hosted gateway for governed LLM, MCP, ACP, and A2A traffic" \
      org.opencontainers.image.source="https://github.com/agent-guide/agent-gateway" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.documentation="https://agentguide.online"

RUN addgroup -S -g 10001 agentgateway && \
    adduser -S -D -H -u 10001 -G agentgateway agentgateway && \
    apk add --no-cache ca-certificates nodejs bash git ripgrep tini && \
    install -d -o 10001 -g 10001 /var/lib/agent-gateway /home/agentgateway && \
    install -d -m 0700 -o 10001 -g 10001 /home/agentgateway/.codex && \
    ln -s /opt/codex/node_modules/.bin/codex-acp /usr/local/bin/codex-acp && \
    ln -s /opt/opencode/opencode /usr/local/bin/opencode
COPY --from=acp /opt/codex/ /opt/codex/
COPY --from=acp /opt/opencode/ /opt/opencode/
COPY --from=build /out/agw /out/agwd /out/agwctl /usr/local/bin/

ENV HOME=/home/agentgateway
ENV CODEX_HOME=/home/agentgateway/.codex
USER 10001:10001
WORKDIR /var/lib/agent-gateway
RUN acp_check_home="$(mktemp -d "$HOME/.acp-check.XXXXXX")" && \
    codex-acp --version && \
    CODEX_HOME="$acp_check_home" codex-acp cli login --help >/dev/null && \
    HOME="$acp_check_home" opencode --version && \
    HOME="$acp_check_home" opencode acp --help >/dev/null && \
    rm -rf "$acp_check_home"
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=6 \
    CMD wget -q -O /dev/null http://127.0.0.1:8019/admin/health || exit 1
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["agwd", "--addr", "0.0.0.0:8080", "--admin-addr", "127.0.0.1:8019", "--config-store", "/var/lib/agent-gateway/configstore.db"]
