# Explicit NTP/DHCP empty-server execution

The pinned NetAsCode 0.5.0 module selects a non-null NTP/DHCP list, including an
empty list. Provider 0.6.1 serializes an empty Terraform server set as native
`servers: null`. CVK now uses that encoding for explicit empty model lists.
Manifest null/omission remains unselected; it does not enter the executor.

## Proven native distinction

The isolated [encoding probe](network-empty-probe.log) found:

- NTP `servers: []` is rejected synchronously with HTTP 400, and two fresh reads
  show the generated setting unchanged. It is not an accepted/completed task.
- NTP `servers: null` is accepted and completes as an empty setting group.
- DHCP accepts both encodings and also returns an empty group.

Using the same null encoding for both follows the pinned provider and avoids a
platform-specific retry/fallback. Every mutating request still has one attempt;
ambiguous requests retain their original journal and are never replayed.

## Reconciliation and custody

An observed empty native group now satisfies only an explicitly empty NTP/DHCP
list. It cannot satisfy nonempty servers, DNS, a banner or timezone. Existing
inheritance resolution remains common: a missing/inherited group walks verified
parents, while an empty group is not guessed to inherit. Malformed native null
server fields remain unavailable; an accepted reset's actual readback is `{}`.

The existing executor, frozen cohort/preflight checks, no-dispatch refusal
retirement, source/owner/target-bound original task-tree recovery and atomic
latest application records are reused. Reset/refill/reset replaces one row per
logical setting and preserves its target UUID. A matching imported empty group
has no mutation or invented receipt. Lost acknowledgements, concurrent nonempty
readback and malformed responses retain fences without inventing task evidence.

This is explicit value reset under Apply/Retain, not automatic source pruning,
CR deletion, native creator adoption or ownership handoff. Native values are not
stored in public status, and no shared foundation, CRD, ND, SWIM or direct-device
upgrade contract is changed.

Immutable upstream references and current source fingerprints: [source manifest](network-settings-empty-servers-sources.json).

The `examples/configs/catalyst-center/network-settings-reset-apply.yaml` reset
example reuses the existing example owner rather than creating a competing
section owner. The [ordinary example compiler](network-empty-example.log) passes;
it was not applied to an existing user site.

## Qualification

- [Expanded race regression](network-empty-regression.log): 16 new public cases
  across both kinds, including reset/refill/reset, nonempty and inherited parent
  fixtures, stale preflight, lost acknowledgement, task/native disagreement,
  malformed reads and already-empty no-op; plus existing optional DNS, null
  selection, network/task/receipt, AAA, credential, IPAM, report and wired
  dependency contracts. PASS: combined race suite, 74.625 seconds.
- [Private Kubernetes API](network-empty-api.log): all 16 public cases use actual
  controller/config/ownership/task/application storage and current CRDs. Native
  responses and actor interruptions are controlled fixtures. PASS: all 16 cases,
  18.537 seconds.
- [Native area cycle](network-empty-native.log): six original public mutations,
  six original Unknown recoveries, three source revisions, native readback,
  bounded two latest application rows, six preflight refusal retirements and
  repeated no-op; independent resolver seeding and generated site setup are
  fixture setup, separate from the six public changes. PASS: 73.455 seconds;
  `Global/CVK-Q-20261017-040741-000000000` removed with final absence verified.
- [Native child/nonempty-parent cycle](network-empty-inheritance.log): separate
  generated hierarchy for the same cycle on a child building, with independent
  reads checking that the parent's nonempty server settings remain unchanged.
  PASS: six original tasks, native unset child groups with unchanged nonempty
  parent, reset/refill/reset and latest receipts (77.492 seconds).
  `Global/CVK-Q-20261017-040951-000000000` cleanup/final absence verified.
- [Foundation regression](network-empty-foundations.log): ND, controller
  ownership, device operations, diagnostics, software lifecycle/upgrades and IOS
  XE GNOI/runtime packages pass; unchanged packages mostly cached.
- [Strict documentation build](network-empty-docs.log).

Native API tests use real Catalyst Center tasks and GETs. Kubernetes storage,
worker restarts, injected GET failures and Unknown checkpoints are simulated.
Generated hierarchies are cleaned only after verified outcomes; unproven native
outcomes retain their exact fixture for inspection. Existing sites, global
settings and device configurations are outside mutation scope.

Permanent native baselines, destructive lifecycle, installed actors, physical
convergence, broader defaults and full pinned-model parity remain incomplete.
