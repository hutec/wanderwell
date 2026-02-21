# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source and build
COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/wanderwell-backend ./main.go

FROM gcr.io/distroless/static-debian12:nonroot AS runner

WORKDIR /home/nonroot

COPY --from=builder /out/wanderwell-backend /home/nonroot/wanderwell-backend

EXPOSE 3000

USER nonroot:nonroot

ENTRYPOINT ["/home/nonroot/wanderwell-backend"]
