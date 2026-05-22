# Development Dockerfile - builds from source.
# Use Dockerfile.release for production with pre-built binaries.

FROM golang:1.26-alpine3.22@sha256:be93003ee861b3b91b6ebcb22678524947e0cd786c2df3f32af520006b1e54f5 AS builder

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Build application
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ratatoskr ./cmd/ratatoskr

# Runtime stage
FROM alpine:3.22@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/ratatoskr /usr/local/bin/ratatoskr

RUN adduser -D -h /home/app app
USER app

ENTRYPOINT ["/usr/local/bin/ratatoskr"]
