FROM golang:1.25.5-alpine3.22 AS build

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

FROM alpine:3.22

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
    apk add --no-cache ca-certificates && \
    install -d -o 10001 -g 10001 /var/lib/agent-gateway
COPY --from=build /out/agw /out/agwd /out/agwctl /usr/local/bin/

USER 10001:10001
WORKDIR /var/lib/agent-gateway
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=6 \
    CMD wget -q -O /dev/null http://127.0.0.1:8019/admin/health || exit 1
CMD ["agwd", "--addr", "0.0.0.0:8080", "--admin-addr", "127.0.0.1:8019", "--config-store", "/var/lib/agent-gateway/configstore.db"]
