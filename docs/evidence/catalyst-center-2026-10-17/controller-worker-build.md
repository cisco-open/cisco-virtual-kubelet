# Controller-worker binary and isolated image preparation

The current branch builds the unified Linux/arm64 `cisco-vk` binary with
`CGO_ENABLED=0`, `-mod=readonly`, `-trimpath` and `-buildvcs=false`. A unique local
qualification image uses the production Dockerfile's pinned final base and
contains that binary and the project license. Production packaging is unchanged;
this local test image has not been published.

[Image/binary metadata](controller-worker-build.json) records the exact binary
SHA256, local image ID/tag, base digest, Linux/arm64 architecture, numeric nonroot
user and entrypoint. The [image build](controller-worker-image-build.log) passed.
The [controller-worker CLI smoke](controller-worker-build-help.log) passed with
network disabled, read-only filesystem, all capabilities dropped and
`no-new-privileges`. No credentials were mounted, and it performed no cluster,
device or native metadata operation.

This proves binary/container construction and command availability, not worker
startup or reconciliation. Bootstrap identity validation, actual cache/watch
behavior, projected credentials/intent keys, actor persistence, restart,
least-privilege RBAC and native worker lifecycle remain to be qualified.

Read-only environment inspection found the main `kind-cisco-vk` API returning
EOF. The separate `kind-cvk-dev` API is ready, but runs the older
`v2026.06.0` controller/device workers and an app-hosted workload, with no
NetworkController API installed. Neither cluster was changed. The worker uses
fixed projected-material paths under `/var/run/secrets/cisco-vk/`; future isolated
qualification should preserve that layout rather than weakening the runtime
path contract or resuming the stopped main control plane.
