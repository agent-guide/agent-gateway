FROM golang:1.25.5-alpine3.22 AS build

ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags=-buildid= -o /out/a2a-agent ./examples/a2a-agent

FROM alpine:3.22

ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Agent Gateway deterministic A2A fixture" \
      org.opencontainers.image.description="Development-only A2A Protocol 1.0 fixture for Agent Gateway evaluation" \
      org.opencontainers.image.source="https://github.com/agent-guide/agent-gateway" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.documentation="https://github.com/agent-guide/agent-gateway/tree/v0.7.1/examples/a2a-agent"

RUN addgroup -S -g 10002 fixture && \
    adduser -S -D -H -u 10002 -G fixture fixture
COPY --from=build /out/a2a-agent /usr/local/bin/a2a-agent

USER 10002:10002
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=2s --retries=6 \
    CMD wget -q -O /dev/null http://127.0.0.1:8090/healthz || exit 1
ENTRYPOINT ["a2a-agent"]
CMD ["--listen", "127.0.0.1:8090"]
