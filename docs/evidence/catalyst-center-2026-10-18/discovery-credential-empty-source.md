# Discovery credential empty-source lifecycle

Branch: `pr/johalley/catc-support`. Pinned model: NetAsCode Catalyst Center 0.5.0.

## Implementation

Existing delivered pure credential-discovery owners may remove all discovery
rows and SecretRefs. The custody metadata gate now recognizes current-owner
typed history with the existing Delete policy and manager finalizer. Full
cohort, immutable root, archive seal, current-source attestation and normal
compiler/dispatch checks remain required. Initial empty-owner enrollment is
unchanged. No provider, transport, CRD, finalizer or persistent store is added.

Explicit empty inline/ConfigMap sources use the existing prune/Retain path.
Cleanup and the next public handoff retain all original rows and version floors.
A shared test assertion now counts archived owner identities rather than
assuming exactly one credential delivery per owner.

## Validation

- Targeted race-enabled tests passed: 14 modes with controlled persistence and
  the same 14 modes against an isolated real Kubernetes API server (28 cases).
- Modes cover empty inline/ConfigMap, Retain, prune/recreate, original Unknown
  task recovery, lost acceptance/retirement/permit acknowledgements, ambiguous
  deletion, source/scope loss, missing seal and returned archived/current jobs.
- Successful cases also delete the config, verify manager finalizer release,
  transfer through the public handoff and run the next owner through cleanup.
- Foundation, Nexus Dashboard, direct upgrades, maintenance and SWIM regressions
  passed; the complete CVK binary built successfully.
- Native read-only qualification passed: three device identities, two SWIM images
  and one discovery job preserved, with zero native mutation attempts.
- Full affected discovery/configuration/credential regression passed with race
  detection: **2,617 passing test events in 1,038.210 seconds**. No failures or
  race reports were observed.
- Strict documentation build passed. The [qualification manifest](discovery-credential-empty-source.json)
  records the frozen 1,636-file package hash, gate logs and build digest.

## Limits

This proves controlled-native resource semantics with real Kubernetes persistence,
not native credential mutation or installed worker delivery. Removing the last
projected leaf from a retained job, global-only/mixed family migration and an
empty incoming owner lacking delivered history or a first-preparation witness
remain outside this qualified scope. The 93-resource model ledger remains
incomplete. Actual switch CLI/SNMP credential values are still needed for
physical authentication qualification.
