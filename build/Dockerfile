# Container image for the API server and dashboard (Phase 8.2, Phase 2.3).
#
# The CLI is deliberately not containerized — it ships as a native single static
# binary (Phase 3.1, Phase 8.2). This image is doiq-api plus the static React
# build it serves, for deployment to a small host such as Fly.io or Render.
#
# It follows the same patterns the tool itself generates. A container-
# optimization product shipping a bloated image would be its own
# counterexample: multi-stage build, distroless runtime, non-root user, no
# package manager and no shell in the final layer.
#
# Build from the repository root:  docker build -f build/Dockerfile .

# --- dashboard build stage ------------------------------------------------------
FROM node:26-alpine AS web

WORKDIR /web

# Manifests first, so the npm layer is reused until dependencies change.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# --- api build stage ------------------------------------------------------------
FROM golang:1.24-alpine AS builder

WORKDIR /workspace

# Dependencies first, in their own layer: they change far less often than
# source, so an edit to a .go file does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# CGO off for a fully static binary — required by the distroless runtime
# below. -trimpath and -s -w drop absolute paths and the symbol table.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w \
      -X github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version.Version=${VERSION} \
      -X github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version.Commit=${COMMIT} \
      -X github.com/dhyaneshsiddhartha15/DeployIQ/pkg/version.BuildDate=${BUILD_DATE}" \
    -o /workspace/doiq-api ./cmd/doiq-api

# --- runtime stage --------------------------------------------------------------
# distroless/static: no shell, no package manager, nothing to exploit that we
# did not put there. It carries the CA bundle the binary needs to reach
# MongoDB Atlas and GitHub over TLS, which is why this rather than scratch.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /workspace/doiq-api /doiq-api
COPY --from=web /web/dist /opt/web
COPY configs/default.yml /opt/config.yml

# The dashboard is served from the same origin as the API (Phase 2.3, 8.6).
ENV DEPLOYIQ_SERVER_WEB_DIR=/opt/web

# nonroot (uid 65532) is provided by the base image; no user is created here
# because there is no shell to create one with.
USER nonroot:nonroot

# 8080 public REST/JSON + dashboard. 8090 (gRPC) and 8081 (admin) bind to
# loopback and are deliberately not exposed.
EXPOSE 8080

ENTRYPOINT ["/doiq-api"]
CMD ["-config", "/opt/config.yml"]
