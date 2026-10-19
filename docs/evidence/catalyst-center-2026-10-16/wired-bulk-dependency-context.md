# Wired bulk: allocation and credential dependency context

The inventory source projection now preserves site pool-reservation references
and all six credential slots while still validating site geometry, floor images
and planned APs. The existing network/IPAM and credential parsers validate these
references; no parallel parser, secret reader or ownership system is introduced.

Version-bearing definitions use the existing Apply credential splitter. This
retains original version counters and pointer paths without resolving values.
Metadata-only import context retains the Report parser. An invalid version does
not fall back to metadata validation; bare credentials remain refused. Unknown
site fields, bad reference types and missing/duplicate reservation assignments
are also refused. The original source bytes remain unchanged.

Recognition is not execution qualification. Bulk's scoped network verifier now
explicitly refuses allocation context as well as credential dependencies until
their native identity/version proof is retained. This closes a gap where pool
definitions could otherwise be ignored by the network-only guard. A valid rich
context therefore cannot acquire a bulk operation, claim or receipt merely
because its NTP/banner/clock subset matches. Public bulk submission stays gated.

## Verification

- [Race regression log](wired-bulk-dependency-regression.log): bulk, shared
  configuration source/journal, IPAM, network settings, global/site credentials,
  geometry and floor/AP suites pass in 43.088 seconds. Dependency cases cover
  both metadata-only and versioned definitions for all six slots, preserved
  counters, malformed versions without fallback, pool references, strict geometry
  composition, unknown fields and inline-secret refusal. Preparation with missing
  dependency identity proof makes zero native calls and changes no journal,
  ownership or action receipt.
- [Foundation log](wired-bulk-dependency-foundations.log): adapter registration,
  ND, controller configuration reservations, software lifecycle and upgrades.

## Installed qualification prerequisite

Fresh inspection confirms that context `kind-cisco-vk` still returns EOF. Its
single control-plane container is running, but containerd 2.0.2 and kubelet
auto-restart with exit status 1. The runtime again fails at missing sandbox
metadata. No installed CVK/control-plane process was active in the executable-name
snapshot. The existing private backup checksum and 700/600 permissions match;
it contains stopped etcd, Kubernetes configuration/certificates and runtime
binaries, but not the runtime metadata database.

A separate local API-only recovery proposal has been prepared but **not applied**.
It requires another private offline backup of the 7.9 GiB runtime directory,
verified containerd/shim 2.0.14 and a standalone kubelet restricted to the existing
etcd/API-server manifests, with external traffic fenced. Scheduler,
controller-manager and CVK/device workloads remain stopped. The proposed runtime
repair can delete invalid sandbox metadata, so binary-only rollback is insufficient.
No cluster reset, etcd wipe or device operation is included.

The official [2.0.14 recovery source](https://github.com/containerd/containerd/blob/v2.0.14/internal/cri/server/restart.go)
contains the missing-extension cleanup. The public daemon and shim were checked
against fresh authenticated release metadata and smoke-tested in disposable,
network-isolated read-only containers without cluster data. Both firewall files
passed syntax validation in a disposable namespace. A synthetic kubelet reported
standalone mode and stopped at its deliberately missing CRI socket; this tests
mode/config parsing, not real cluster recovery. Kubernetes documents
[standalone mode](https://v1-32.docs.kubernetes.io/docs/tutorials/cluster-management/kubelet-standalone/)
as using local Pod sources without an API client.

Approval for the additional backup and API-only recovery is pending. The earlier
approval covered the original private backup only. No installed service, binary,
firewall, runtime metadata, etcd data or device configuration has been changed.
All resource/section parity flags remain partial.

## Subsequent identity implementation

The original refusal boundary recorded above has since been extended with [native allocation identity binding](wired-bulk-ipam-binding.md) and [external credential assignment identity binding](wired-bulk-credentials.md). Credential definitions/version ownership, full device convergence and public bulk execution remain pending.
