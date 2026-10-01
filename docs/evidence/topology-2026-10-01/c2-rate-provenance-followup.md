# C2 rate-provenance and compatibility follow-up

Date: 1 October 2026. Candidate: clean commit `95077ba7` on
`pr/johalley/tas-extentions`.

## Result

PASS for the bounded directional-rate representation, worker publication,
manager acceptance and Kubernetes 1.35 compatibility exercised here. This is
not E02-B loaded-path qualification and does not authorize C3 headroom-based
device disruption.

The implementation preserves each device-reported capacity and ingress/egress
rate together with the derived headroom and the source identifier
`cisco-ios-xe-interfaces-oper:statistics-kbps`. The manager independently
recomputes headroom and accepts the sample only when its current
CiscoDevice/device identity, producer revision, Pod UID, collection interval
and monotonic sequence all match manager-observed state.

## Physical failure found and corrected

The first exact build, `7b1128ae`, passed unit, race, envtest, rendering and
native bound-token tests but failed against the Ubuntu16 k3s API server. The
generated OpenAPI maximum for a `uint64` rate was rounded from signed-int64 max
to `9223372036854776000`; that server converted the rounded bound to
`-9223372036854775808` and rejected every nonnegative sample.

The physical failure was fail-closed: the new manager did not promote the old
worker samples, and the new workers logged API validation failures instead of
silently omitting or trusting the rates. No device operation was dispatched.

`95077ba7` changes the public ceiling to `2^53-1`
(`9007199254740991` bits/s), the largest exactly representable JSON integer.
This remains over 9 Pbit/s. Provider normalization uses the identical bound,
and a generated-CRD regression test asserts the limit on raw and accepted
capacity/ingress/egress fields. The live CRD reported the corrected maximum.

## Reproducible candidate and local gates

| Item | Value / result |
| --- | --- |
| Commit | `95077ba7` |
| OCI index | `sha256:e3feb31189d39c72b82bfeda9ddc267b47719ff76c8d2d8c0eb3d7d5c17a3a19` |
| Linux/amd64 manifest | `sha256:1c9a0ec610dbe92980cfdf6a485f26308a0f0cd99e39b1498ed99233ffd9e9a4` |
| Transfer archive SHA-256 | `400d8b25b14fb1aede90025ed67c0a4a7fc0414ba437cd6073ee36cb68069e6c` |
| Full `go test -race -count=1 ./...` | PASS outside the filesystem sandbox; the first sandbox run failed only because loopback listeners were prohibited |
| Pinned Kubernetes 1.35 `make test-envtest` | PASS, including network-observation CRD round trip |
| Focused provider/controller/IOS-XE race tests | PASS |
| Two-pass generators | PASS before physical execution; no generated drift |
| Helm lint and topology render contract | PASS |
| Kubernetes 1.35 bound-token suite | PASS in disposable cluster `cvk-roadmap-c2-rate` |

## Physical deployment

Ubuntu16 ran k3s client/server `v1.35.8+k3s1`. The CiscoDevice CRD was applied
before the manager image and Helm revision 122 deployed `95077ba7`. All three
app-hosting workers, all three network-management workers and the manager
became Ready on that exact tag. The three projected Nodes were `Ready=True`
and had no taints.

During the deliberate manager-new/worker-old transition, accepted evidence was
removed when an old sample no longer matched the manager-observed worker. New
workers initially received `network worker binding is not at the desired
revision` until their exact Pod was independently observed. Those startup
warnings stopped after convergence. There were no corrected-schema validation
errors.

Final representative manager acceptance:

| CiscoDevice | Accepted producer Pod | Sequence observed after convergence | Rate-qualified interfaces |
| --- | --- | ---: | ---: |
| `cat9k-live` | `0b53fa64-3a9b-4817-9ab1-6a66e7c1784d` | 10 | 46 |
| `cat9k-lab-101` | `76b97b6d-3cb6-4b64-b0fd-da9779baa0eb` | 10 | 46 |
| `cat9k-lab-103` | `47744430-062d-40d7-8cae-10831e38c7d9` | 9 | 44 |

The manager was restarted without rotating a device worker. Raw and accepted
sequences continued advancing with the same exact producer Pod UIDs. A raw
sample may be one manager reconcile ahead of the accepted copy; rollout
consumers use only the latter.

## Device-side read-only comparison

Three Kubernetes-native `DeviceOperation` objects succeeded:

| Operation | UID | Device CLI result for `GigabitEthernet0/0` |
| --- | --- | --- |
| `c2-rate-audit-100-95077ba7` | `f68ef34f-178e-4048-a198-1846e4e6614d` | 1 Gbit/s; input 7,000 bit/s; output 133,000 bit/s |
| `c2-rate-audit-101-95077ba7` | `1f8a195d-9346-4a52-8303-2c47d101cc23` | 1 Gbit/s; input 1,000 bit/s; output 116,000 bit/s |
| `c2-rate-audit-103-95077ba7` | `2e9b2f91-a350-4446-a455-e859149cfbb9` | 1 Gbit/s; CLI five-minute rates rounded to zero at that instant |

The time-aligned accepted samples for `.100` and `.101` matched those values
exactly and retained 99% conservative integer headroom. `.103`'s device-model
sample reported a low nonzero management rate while the five-minute CLI view
rounded to zero; the two sources use different sampling windows and the CLI
query itself adds management traffic. This is useful provenance evidence, not
an independent accuracy fixture.

## Remaining C2 exit gates

- Declare a non-management test path, independent traffic generator/receiver,
  sample alignment and tolerance before controlled idle/loaded ingress and
  egress testing.
- Qualify redundant-supervisor/stack health only on hardware that exposes that
  capability; it remains Unknown on this standalone cohort.
- Complete the reverse old-manager/new-worker and explicit rollback cases.
  The manager-new/old-worker transition was exercised fail-closed here.
- Candidate-specific remote CI remains required after publication.

Concurrency, optimistic-lock collision, lost-status-response replay,
same-Pod monotonic sequence, producer replacement and manager restart now have
automated coverage. They should no longer be described as wholly untested.
