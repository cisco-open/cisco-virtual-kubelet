# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o cisco-vk ./cmd/virtual-kubelet

# Final stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/cisco-vk /usr/local/bin/cisco-vk

# Create config directory
RUN mkdir -p /etc/cisco-vk/certs

# Non-root user (optional, comment out if root access needed)
# RUN adduser -D -u 1000 cisco-vk
# USER cisco-vk

ENTRYPOINT ["/usr/local/bin/cisco-vk"]
