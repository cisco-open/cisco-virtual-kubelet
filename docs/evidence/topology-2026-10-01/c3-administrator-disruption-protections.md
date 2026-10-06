# C3 administrator disruption-protection qualification

Date: 1 October 2026. Candidate: clean commit `617b1cfc` on
`pr/johalley/tas-extentions`.

## Result and boundary

PASS for the administrator-owned critical-service and singleton-path
prohibition increment. The policy accepts at most 16 named rules with bounded
Kubernetes selectors. Every selector key must be a protected,
`requiredTopologyKeys` member. Matching rules fail closed while a target is
frozen and again immediately before execution. Rule changes affect both policy
hashes, so an older plan cannot silently inherit relaxed authority.

The current IOS-XE workflow combines preparation and activation. A match
therefore blocks the complete lifecycle. Preparation may be exempted only
after E04 proves a distinct non-disruptive operation and E05/E06 provide its
own durable authorization contract.

This increment does not infer redundancy from labels and does not close E03.
No protection rule was installed in the shared physical lab policy and no
device software mutation was requested. The physical run qualifies retained-
value compatibility, exact-candidate convergence, accepted observation
continuity and secure read-only device access. Redundant-path, singleton-path,
critical-service and congested-path acceptance still require independently
observable service/traffic fixtures.

## Implementation and automated qualification

- `AdminPolicyConfig.disruptionProtections` is optional and omitted when
  empty, preserving the earlier canonical policy document and hash.
- Rule names are unique DNS labels. Reasons are exactly `CriticalService` or
  `SingletonPath`; empty selectors and selectors outside the frozen required
  topology key set are rejected.
- Canonicalization sorts rules, expressions and expression values. Order-only
  rewrites retain identical semantic and structural hashes.
- The planner returns `CriticalServiceProtected` or
  `SingletonPathProtected` before creating a frozen target. Execution
  revalidation applies the same current administrator rule before admission.
- Unit tests cover validation, deterministic overlapping matches, hash
  stability, planning refusal and execution-time policy tightening.
- The chart schema, templates and render suite cover a valid rule, unknown
  reasons, unrequired selector keys, duplicate names and an omitted legacy
  value.
- `go test -race -count=1 ./...`, pinned Kubernetes 1.35 `make test-envtest`,
  the topology render contract and strict MkDocs build passed.

The first physical Helm attempt also supplied a valuable negative. Revision
123 had no `disruptionProtections` key because it predated the feature.
`--reuse-values` exposed an unsafe template `len` call on nil. Helm failed
before applying resources. Commit `617b1cfc` treats an absent retained value as
an empty list and adds a regression. The retry succeeded as revision 124, and
the retained `policy.json` still omitted the field.

## Candidate identity and physical observations

| Item | Value |
| --- | --- |
| Commit | `617b1cfc4a819ac5b49736d5d853a22e2961daff` |
| Image tag | `cvk-tas-extentions:617b1cfc` |
| OCI index | `sha256:e68fd46c72181ec71626cf92876f29789383a747550557171aeb37b3fa068d37` |
| AMD64 manifest | `sha256:caf6630f653301d77c3dd4d2db3f0af381b3cf36a39ff13a9855da004198c070` |
| Image config | `sha256:0c90606871d81209c35a85032a995839610877f4680279361613b934811121ad` |
| Imported archive SHA-256 | `c449d9b95949aeab0638fc4009fe191dd9ef2ee91570a94d9174bfa944bd5fa6` |
| Cluster | Ubuntu16 k3s `v1.35.8+k3s1` |
| Helm release | `cisco-vk`, namespace `cisco-vk-system`, revision 124 |

The manager, three IOS-XE app workers and three IOS-XE network workers
converged to the exact tag. All three physical Nodes were Ready, schedulable
and untainted. Each CiscoDevice was Ready and its accepted network sample was
complete and bound to the current network-worker Pod UID.

| Device | Verify object / UID | Result |
| --- | --- | --- |
| `cat9k-live` (`198.51.100.100`) | `e03-os-verify-live-617b1cfc` / `6d52f212-da52-4d1c-8f63-8444cd3be729` | `17.18.02.0.4112.1766116039` |
| `cat9k-lab-101` (`198.51.100.101`) | `e03-os-verify-101-617b1cfc` / `8dc71931-008f-48cb-aeb0-6822d6492350` | `17.18.02.0.4112.1766116039` |
| `cat9k-lab-103` (`198.51.100.103`) | `e03-os-verify-103-617b1cfc` / `5754dc84-a91c-45cf-b846-c466a5dc60de` | `17.18.03.0.5496.1776157760` |

The operations used the current shared network-management identity and exact
current worker Pod bindings. The settled recent manager and six-worker scan
contained no warning, error, forbidden or panic records.

## Remaining E03 work

1. Model bounded overlapping risk groups with frozen exact physical
   membership, non-target peer health and one CAS budget across campaigns.
2. Add aggregate transfer accounting and actual byte pacing only after E02
   supplies a controlled non-management path, independent load source and
   predeclared accuracy/burst tolerance.
3. Extend accepted network checks continuously through recovery and soak,
   resetting continuity without abandoning already accepted physical work.
4. Execute E03-C–F with qualified redundant, singleton, critical-service and
   congested service paths. A policy label or successful gNOI probe is not
   forwarding or application-continuity evidence.
