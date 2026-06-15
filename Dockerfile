# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.25 AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# VERSION is injected by CI (docker metadata-action) and stamped into the binary.
ARG VERSION=dev
# CGO disabled: pgx and modernc.org/sqlite are pure Go, so the binary is fully
# static and runs on distroless/static (and scratch).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/gopaste ./cmd/gopaste

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gopaste /usr/local/bin/gopaste
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gopaste"]
