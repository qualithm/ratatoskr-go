# Development Dockerfile - builds from source.
# Use Dockerfile.release for production with pre-built binaries.

FROM golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Build application
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ratatoskr ./cmd/ratatoskr

# Runtime stage
FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/ratatoskr /usr/local/bin/ratatoskr

RUN adduser -D -h /home/app app
USER app

ENTRYPOINT ["/usr/local/bin/ratatoskr"]
