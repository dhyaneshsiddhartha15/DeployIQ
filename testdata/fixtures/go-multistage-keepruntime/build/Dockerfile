FROM golang:1.24 AS builder
ARG GIT_TOKEN=unused
WORKDIR /workspace
COPY ./ .
RUN git config --global url."https://${GIT_TOKEN}:x-oauth-basic@github.com/example-private".insteadOf "https://github.com/example-private"
RUN go env -w GOPRIVATE=github.com/example-private/*
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -a -o /workspace/storage-plugin main.go

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates openssh-client bash \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /
COPY --from=builder /workspace/storage-plugin .
COPY default.yml /opt/config.yml
RUN groupadd -r storage && useradd -r -g storage -s /bin/bash storage
RUN chown -R storage:storage /opt/
USER storage
ENTRYPOINT ["/storage-plugin"]
