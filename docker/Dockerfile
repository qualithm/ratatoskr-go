# Development Dockerfile - builds from source.
# Use Dockerfile.release for production with pre-built binaries.

FROM golang:1.26-alpine3.24@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS builder

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Build application
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ratatoskr ./cmd/ratatoskr

# Runtime stage
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/ratatoskr /usr/local/bin/ratatoskr

RUN adduser -D -h /home/app app
USER app

ENTRYPOINT ["/usr/local/bin/ratatoskr"]
