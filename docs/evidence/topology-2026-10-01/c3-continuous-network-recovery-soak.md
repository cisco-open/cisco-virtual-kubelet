# C3 continuous network recovery and soak qualification

Date: 1 October 2026. Candidate:
`ae4f3a7b43f24fb2cb695343ea85c2b8955ae456` on
`pr/johalley/tas-extentions`.

## What changed

An opted-in rollout network-health gate now remains authoritative after the
device operation. Recovery and every continuous-soak reconciliation re-read
the exact CiscoDevice, confirm its frozen UID, generation and physical
identity, and evaluate the complete manager-accepted interface, neighbor,
headroom, completeness, freshness and current-worker binding policy.

The accepted sample must have started after the device operation completed.
A still-fresh pre-operation sample cannot prove recovery. A missing, stale,
replaced or unhealthy accepted sample leaves the reservation held and resets
the healthy-soak interval. Already accepted device work remains observable;
the manager does not replay or abandon it. Rollouts which omit or disable the
network gate retain their previous behavior.

## Automated verification

The exact clean commit passed:

- focused controller recovery/soak tests, including pre-operation rejection,
  post-operation acceptance and a later incomplete sample;
- `go test -race -count=1 ./...`;
- the pinned Kubernetes 1.35 provider and controller envtest suites;
- topology Helm/render contract checks; and
- strict MkDocs rendering and `git diff --check`.

The shared settlement path is used for both ordinary and workload-drained
leaves. Existing target status transition semantics make a failed health gate
leave `HealthyPostMutationSoak`, so its prior `lastTransitionTime` cannot be
reused after health recovers.

## Exact image and deployment identity

| Item | Value |
| --- | --- |
| Commit | `ae4f3a7b43f24fb2cb695343ea85c2b8955ae456` |
| Image tag | `cvk-tas-extentions:ae4f3a7b` |
| OCI index / local image ID | `sha256:3fdf65c01ed5820396b3ebdfd476c416a082bd0caf0e28f0fdd1f1fb1cda03e0` |
| Linux/AMD64 manifest | `sha256:9bcf823f6887237d200e9797a615ad505ebb3e81e890395550b0982100d97521` |
| Image config | `sha256:51e368ca3c2e6ba89fed513497beddf035604b8f3940a77f715ea1aac2cdba1d` |
| Gzipped image archive SHA-256 | `5431ad5303a8e22decc8d3db005cdc48cd42ce91a821d2bad01b8f6bf2f168da` |
| Packaged chart SHA-256 | `94e38062e162f4b12ab76312152af476e3a6993e5434f7de7d62cd94b60a5f44` |
| Cluster | Ubuntu16, k3s `v1.35.8+k3s1` |
| Helm release | `cisco-vk`, revision 125 |

The controller and the three app workers plus three network workers converged
to this exact tag. The retained administrator policy was not changed and no
software image mutation was initiated for this negative safety increment.

## Physical read-only qualification

All three CiscoDevices were `Ready`. Their accepted samples were complete and
matched the current manager-bound network-worker Pod UID exactly:

| Device | Current/accepted worker Pod UID | Sample sequence | Collection start | Node version |
| --- | --- | ---: | --- | --- |
| `cat9k-live` (`.100`) | `cbdfd620-9b26-456d-be80-9f9e7da79e36` | 1 | 21:17:02 UTC | 17.18.2 |
| `cat9k-lab-101` (`.101`) | `e9a41411-d772-448b-b082-19979cdd7b16` | 2 | 21:17:15 UTC | 17.18.2 |
| `cat9k-lab-103` (`.103`) | `bcaf40c8-a9e2-451f-9e60-839a08365b3e` | 2 | 21:16:59 UTC | 17.18.3 |

Each Node passed the native `Ready` condition wait and was schedulable with no
taints. Fresh secure, read-only `GNOIOSVerify` operations succeeded:

| Device | DeviceOperation UID | Verified running version |
| --- | --- | --- |
| `.100` | `bbea6365-a12b-4445-9ec3-09ad33beda3e` | `17.18.02.0.4112.1766116039` |
| `.101` | `b59ac47e-16fb-466b-ac97-672f33ecd3e9` | `17.18.02.0.4112.1766116039` |
| `.103` | `68291a80-ead6-44b3-b5ec-7f0cbbc62b32` | `17.18.03.0.5496.1776157760` |

During the controller-driven worker replacement, retiring/starting workers
briefly logged fail-closed binding denials, network publication waits and
optimistic Lease conflicts. These occurred before convergence and demonstrate
that an unbound replacement cannot write as the current worker. The explicit
21:18:00–21:19:10 UTC settled log scan across the manager and all six C9K
workers contained no matching error, denial, failure, panic or fatal records.

## Acceptance boundary

This qualifies the exact candidate's deployment, secure connectivity,
current-worker evidence binding, and code-level continuous recovery/soak
semantics. It does **not** claim a physical post-reload failed-path experiment:
the shared lab has no isolated, controllable redundant/singleton/critical or
congested service-path fixture. Deliberately breaking one of the current links
would not be a bounded qualification. Those E03-C–F scenarios remain open,
as do overlapping risk groups and byte-based transfer pacing.
