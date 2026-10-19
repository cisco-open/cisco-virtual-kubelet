# Discovery metadata-only incarnations for existing versioned owners

Branch: `pr/johalley/catc-support`. Pinned model: Catalyst Center NetAsCode 0.5.0.

An owner with a recorded versioned creator may now replace a discovery job with
global-credential references or SNMPv3 NoAuthNoPriv metadata. The explicit v4
input format contains no credential versions and requires public source/effect
scope. Older v1/v2/v3 inputs still require nonempty credential bindings. Unknown
formats remain reserved and cannot fall through into generic completion.

The existing source/compiler, native resolver, one-use dispatcher, task observer,
action journal, retirement writer and finalizer are reused. A metadata creator
records one action proof and **no CredentialApplication**; the shared receipt
schema still requires at least one delivered version. Full versioned ancestors,
custody seals, native identities and field floors are preserved. Retirement binds
the exact v4 successor scope and reserves capacity before deleting its predecessor.

Existing owners can mix metadata and projected rows, prune/recreate them, and
reintroduce projected fields only above historical removed-field floors. A typed
ConfigMap source stays on the existing credential source attestation path rather
than passing through the ordinary discovery parser. No new provider, transport,
CRD, operation store or finalizer is introduced.

## Current qualification

- **131 overlay test events passed**: 57 revision scenarios with controlled
  persistence, the same 57 against a real isolated Kubernetes API server, and
  15 protocol/history events. This includes 23 new metadata scenarios per backend.
- Ten initial positive inline/ConfigMap, global/NoAuth and reintroduction cases
  also passed before the full revision/fault matrix.
- Cases cover version reuse refusal, mixed selection, pruning/recreation,
  original Unknown tasks, source/scope loss, ambiguous acknowledgement,
  lost acceptance/permit/receipt acknowledgements and a rejected receipt CAS.
- Successful cases delete the config, verify finalizer release and complete
  a subsequent public owner lifecycle while preserving old receipts and floors.
- The exact overlay candidate was applied with a byte-for-byte source check.
  Full affected regression passed on the actual worktree: 2,678 passing
  events with the race detector and real Kubernetes API scenarios (1,249.806s).
  Foundation, ND, direct upgrade, maintenance, SWIM and strict documentation
  checks also passed.

The first protocol attempt exposed a timestamp-pointer alias in its audit fixture;
that fixture was corrected and all protocol tests passed. The failure log is
retained separately. [Qualification record](discovery-metadata-incarnations.json)
contains source fingerprints, gate provenance and explicit pending gates.

## Limits

This extension requires the same owner's recorded versioned history and complete
execution authority. Initial metadata-only typed enrollment, ordinary/typed
custody migration, broader mixed/grouped imports, native credential mutation,
installed actors and physical authentication remain unqualified. Controller API
credentials are not device CLI/SNMP credentials. Wider 93-resource parity remains
incomplete; no reporting-only capability is counted as execution parity.
