#!/bin/sh
# Build the AP executable and redistribution notices used by the Docker image.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' \
  -o ap-metrics ./cmd/ap-metrics

mkdir -p licenses/cisco-virtual-kubelet licenses/go
cp -f ../../LICENSE licenses/cisco-virtual-kubelet/LICENSE
go_root=$(go env GOROOT)
cp -f "$go_root/LICENSE" "$go_root/PATENTS" licenses/go/
for module in golang.org/x/crypto golang.org/x/sys; do
  # A selected indirect module may not be imported by this build, so its
  # source directory can be absent on a fresh runner even after go build.
  go mod download "$module"
  module_dir=$(go list -m -f '{{.Dir}}' "$module")
  mkdir -p "licenses/$module"
  cp -f "$module_dir/LICENSE" "$module_dir/PATENTS" "licenses/$module/"
done
