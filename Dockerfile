# Build stage
FROM golang:1.24-alpine AS builder

# Define working dir
WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o cisco-vk ./cmd/virtual-kubelet

# Final stage
FROM gcr.io/distroless/static-debian12

# Copy binary
COPY --from=builder /app/cisco-vk /usr/local/bin/cisco-vk

# Use nonroot user
USER nonroot:nonroot

# Define working dir
WORKDIR /app

# Add Entrypoint
ENTRYPOINT ["/usr/local/bin/cisco-vk"]