# C3 physical pacing and interrupted-install recovery

Date: 1–2 October 2026. Branch: `pr/johalley/tas-extentions`.

This record covers a physical IOS XE downgrade and reverse upgrade on
`cat9k-lab-103` (`198.51.100.103`). It qualifies the candidate's bounded gNOI
image stream, exact-plan topology controls, conservative recovery from a lost
`OS.Install` response, post-reload network-health soak, and final ownership
release. It does not qualify an independently measured production forwarding
path or the remaining E03 service-path matrix.

## Candidate and controls

| Item | Value |
| --- | --- |
| Final source commit | `2f27f302fc9e8d02af00b9e6b6924576e5427572` |
| Runtime image | `cvk-tas-extentions:2f27f302` |
| OCI image index | `sha256:9db739a3478482107fbd25f23dd9dc55ac6bba24a2efabf4df7c925742ae99f5` |
| Helm release | `cisco-vk`, revision 131 on Ubuntu16 k3s `v1.35.8+k3s1` |
| Risk group | `cat9k-103-path` |
| Frozen aggregate transfer ceiling | `25,000,000` bytes/second |
| Risk-group limits | one concurrent transfer; one unavailable member |
| Workload policy | `BlockIfRunning` |
| Network gate | complete manager-accepted evidence, max age 300 seconds |
| Post-mutation soak | 30 seconds |

The policy selected the physical member through
`topology.cisco.vk/lab-node=cat9k-lab-103`. The frozen plans retained the
membership hash and projected `maxTransferBytesPerSecond=25000000`; the worker
rechecked the current manager grant immediately before each mutation claim.

## Downgrade: 17.18.03 to 17.18.02

Rollout `roadmap-pacing-downgrade-20261002-103` targeted the pinned image:

```text
sha256:c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355
size: 1247897709 bytes
```

The original candidate `c8ec293c` transferred all bytes, after which IOS XE
lost the terminal gNOI Install response. CVK retained the mutation claim and
did not replay Install. Read-only IOS XE evidence showed a quiescent native
inventory with every target package added, the exact gNOI source package
verified with the pinned size, and one recent successful `install-add`
operation for that exact filename.

Candidate `0f4a013a` added the conservative observer but correctly remained
quarantined because the driver's lifecycle wrapper did not advertise the
optional observation capability. Candidate `2f27f302` added that neutral
forwarding seam. The same retained leaf then advanced with
`NativeInstallCorroborated`, without a second Install dispatch, and activated
the exact IOS XE identity `17.18.02.0.4112.1766116039`.

The leaf completed at 23:23:19 UTC. The rollout completed its continuous
network-health soak and reached `Succeeded`; a secure follow-up gNOI
`OS.Verify` returned the same exact running version. Device CLI reported:

- IOS XE 17.18.02 / 17.18.2, last reload reason `Image Install`;
- image 17.18.02.0.4112 activated and committed;
- auto-abort timer inactive;
- gNXI secure password authentication enabled and gNOI OS Image service Up;
- control processor Healthy; and
- no hosted application present.

## Reverse upgrade: 17.18.02 to 17.18.03

Rollout `roadmap-pacing-upgrade-20261002-103` froze and approved plan hash
`sha256:95dd86df4119805af514a55c7e7b31ae8e7099eb13adad466bd25e8ce5820827`
for:

```text
sha256:df6055e4e1e88135b311998d721ff6d20a94a475113c1ff678fb65d14dc11049
size: 1249368115 bytes
```

Observed cumulative device acknowledgements were:

| UTC | Bytes | Percent |
| --- | ---: | ---: |
| 23:30:12 | 356,515,840 | 28% |
| 23:30:33 | 870,318,080 | 69% |
| 23:31:02 | 1,247,805,440 | 99% |
| 23:34:03 | 1,249,368,115 | 100% |

The 21-second middle interval advanced by 513,802,240 bytes, approximately
24.47 MB/s, consistent with the configured 25 MB/s ceiling. This is a worker
and device acknowledgement measurement, not an independent forwarding-path
measurement and therefore does not close E02-B or the congested-path portion
of E03.

IOS XE again lost the terminal Install response after receiving the full
image. The exact native proof closed the ambiguity and the leaf advanced with
`NativeInstallCorroborated` at 23:33:11 UTC. It did not replay Install. The
device reloaded and the leaf completed at 23:40:48 UTC with exact running
identity `17.18.03.0.5496.1776157760`. The rollout held its reservation through
recovery and the 30-second continuous health soak, then reached `Succeeded`.

Final secure `OS.Verify` returned the exact target. CLI reported IOS XE
17.18.03 / 17.18.3, image 17.18.03.0.5496 activated and committed, inactive
auto-abort, a healthy control processor, provisioned secure gNOI OS service,
and no hosted application. The topology ledger ended with an empty
`reservations` map and a release fence for this exact reservation.

## Safety result

The two directions establish the following for this candidate and C9300-24P
cohort:

- byte pacing is derived from frozen administrator policy and enforced by the
  worker on the gNOI content stream;
- exact image digest and size are pinned before the device mutation claim;
- a lost terminal Install response is observed without replay;
- recovery requires exact target, package, size, quiescence and one recent
  successful native operation, and fails closed when any proof is absent;
- activation begins only after validated or native-corroborated installation;
- topology ownership and disruption reservation survive reload and health
  recovery; and
- release occurs only after current accepted network evidence remains healthy
  for the configured soak.

This does **not** establish service availability, alternate-path headroom,
critical-service behavior, congestion tolerance, application relocation,
Install-only staging, or separate activation approval. Those gates require
the fixtures listed in the execution plan and remain explicitly open.

## Verification and artifacts

The exact source candidate passed:

- `go test -race ./...`;
- pinned Kubernetes 1.35 provider/controller envtest suites;
- two-pass controller-tools generation parity;
- Helm lint and the managed topology render contract; and
- strict MkDocs rendering.

The [downgrade result](downgrade/06-final-rollout.yaml) and
[upgrade result](upgrade/08-final-rollout.yaml) sit beside their request,
frozen and approved plans, leaf checkpoints, terminal objects, events,
worker/controller logs, CLI health results, secure Verify results, Node state,
policy and final ledger. Each direction's `SHA256SUMS` covers every
artifact except themselves. No Secret object, credential, certificate, key or
kubeconfig is included. The CiscoDevice session token in the saved terminal
snapshot is redacted.
