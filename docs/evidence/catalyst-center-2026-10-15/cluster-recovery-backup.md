# CVK control-plane recovery investigation and approved backup

## Observed failure

Only `cisco-vk-control-plane` was inspected. Its containerd 2.0.2 fails during
CRI startup because metadata is missing for stored sandbox
`c9224be7e2fa49ad90ae5795d6c76d12240ad420a5202cd20f9e6aacd052d59d`.
The `kind-cisco-vk` API returns EOF. No etcd process was running at capture.
Unrelated Kubernetes contexts were not changed.

Upstream [issue 10848](https://github.com/containerd/containerd/issues/10848)
records this failure class. [PR 11612](https://github.com/containerd/containerd/pull/11612)
prevents new missing-extension cases; [PR 11576](https://github.com/containerd/containerd/pull/11576)
adds recovery of leaked sandbox records when their metadata is not found. The
latter fix is present in the source tag for the staged same-series release,
[v2.0.14](https://github.com/containerd/containerd/releases/tag/v2.0.14).
No wholesale containerd or etcd data deletion is proposed.

## Backup and authorization

Automatic approval review initially rejected the sensitive backup because its
contents and host destination were not explicitly authorized. The user then
approved the exact private backup scope, and that backup succeeded.

- Directory: `/private/tmp/cvk-lan-operation-recovery-2026-10-15/`, mode 700.
- Archive: `control-plane-offline.tar.gz`, mode 600, 53,242,202 bytes.
- SHA256: `2e48d31654a83a84e1eba2a03e6437640f036e0866d23870b69a2869cf3c5a1d`.
- Contents: stopped etcd data, Kubernetes configuration/certificates, containerd
  configuration and existing daemon/shim binaries, kubelet configuration.
- Archive readability and required etcd/runtime members verified; etcd stopped
  and containerd metadata stat stable across capture. This is backup verification,
  not a successful restore rehearsal.
- Companion metadata is mode 600. No archive contents, keys or credentials were
  copied into this repository.

Approval covers the backup only, not cluster data wiping or resumed device
operations. No service or runtime binary was changed during backup creation.

## Staged repair and execution boundary

Official ARM64 `containerd-2.0.14-linux-arm64.tar.gz` was downloaded and checked
against published SHA256
`212f6fff54ae21c6135e2396835ffe1d0459e3a192dae9aa6aa67d86411392cc`.
Only daemon and shim binaries were extracted to private temporary staging; they
have not been installed. Normal kubelet currently uses API mode. Its installed
help confirms standalone mode when kubeconfig is omitted; the existing static
manifest directory contains etcd, API server, controller manager and scheduler.

The proposed isolated recovery stops normal kubelet, replaces the runtime with
the verified same-series binaries, and uses a temporary standalone kubelet to
restore only control-plane static pods. API/workload inspection must precede
resumption of any normal kubelet or CVK/device workload. Separate approval has
been requested for this runtime replacement and isolated recovery; it has not
yet been executed. Recovery success and installed qualification are not claimed.
