# Copyright 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Build stage
# Pin the builder to the runner's native architecture ($BUILDPLATFORM) and
# cross-compile to the requested target arch via Go's GOOS/GOARCH below. This
# keeps the Go toolchain off QEMU emulation for the non-native leg of a
# multi-arch build (e.g. linux/arm64 on an amd64 runner) — emulated Go builds
# are what pushed the release pipeline past its timeout. CGO is disabled, so the
# cross-compile is pure-Go and needs no target-arch toolchain.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the unified binary (supports 'run' and 'manager' subcommands).
# TARGETOS/TARGETARCH are provided automatically by buildx per target platform.
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o cisco-vk ./cmd/cisco-vk

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/cisco-vk /usr/local/bin/cisco-vk
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/cisco-vk"]