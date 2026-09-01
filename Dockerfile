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
FROM --platform=$BUILDPLATFORM golang:1.26.7-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download
RUN GOBIN=/out/tools GOTOOLCHAIN=local \
    go install github.com/google/go-licenses/v2@v2.0.1

# Copy source code
COPY . .

# Build the unified binary (supports 'run' and 'manager' subcommands).
# TARGETOS/TARGETARCH are provided automatically by buildx per target platform.
ARG TARGETOS TARGETARCH
ARG VERSION=devel
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 \
    GOENV=off \
    GOTOOLCHAIN=local \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH} \
    go build \
      -mod=readonly \
      -trimpath \
      -buildvcs=false \
      -ldflags="-w -s -buildid= -X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME}" \
      -o cisco-vk \
      ./cmd/cisco-vk

# Generate the redistribution bundle from the exact packages linked into this
# binary. go-licenses preserves upstream NOTICE files and copies the covered
# source for reciprocal licenses such as MPL-2.0. The Go standard library is
# handled separately because go-licenses intentionally omits it.
RUN mkdir -p /out/licenses/go && \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} /out/tools/go-licenses \
      check ./cmd/cisco-vk && \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} /out/tools/go-licenses \
      save ./cmd/cisco-vk --save_path /out/licenses/third-party && \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
      go list -deps -f '{{with .Module}}{{.Path}} {{.Dir}}{{end}}' ./cmd/cisco-vk \
      | sort -u \
      | while read -r module module_dir; do \
          find "$module_dir" -type f -name PATENTS | while read -r patents; do \
            relative="${patents#${module_dir}/}"; \
            destination="/out/licenses/third-party/${module}/${relative}"; \
            mkdir -p "$(dirname "$destination")"; \
            cp "$patents" "$destination"; \
          done; \
        done && \
    test -s /out/licenses/third-party/github.com/moby/spdystream/spdy/PATENTS && \
    cp "$(go env GOROOT)/LICENSE" /out/licenses/go/LICENSE && \
    cp "$(go env GOROOT)/PATENTS" /out/licenses/go/PATENTS

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
COPY --from=builder /app/cisco-vk /usr/local/bin/cisco-vk
COPY --from=builder /out/licenses /licenses
# Keep the project license in the distributed image. The plugin archives carry
# the same file, and /licenses is the conventional container attribution path.
COPY LICENSE /licenses/cisco-virtual-kubelet/LICENSE
# Distroless defines its nonroot user and group as 65532. Keep the OCI image
# metadata numeric so kubelet can verify runAsNonRoot without resolving names.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/cisco-vk"]
