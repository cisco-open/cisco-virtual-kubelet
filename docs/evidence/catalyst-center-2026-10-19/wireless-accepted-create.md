# Ordinary wireless metadata Create completion

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full model parity remains **incomplete**.

## Execution change

Declared ordinary metadata Creates now use the same complete rooted task-tree
observer as acknowledged Unknown Creates and metadata Updates. A successful root
cannot release the operation while a child failed, remains pending, belongs to a
foreign tree or cannot be observed. Source/generation/owner fences and independent
native desired-state readback remain required. Task observation never resubmits
the mutation.

The existing resource selector now handles both normal Accepted and Unknown
Create/Update observation; a redundant Update-only selector was removed.
Accepted is normalized only in a private observation copy, not written to the
journal as Unknown. The shared task-tree reader, journal, guard and transport
remain unchanged. No second completion protocol, CRD or provider is introduced.

The qualified metadata families are anchor groups, interfaces, power profiles,
802.11be profiles, pre-auth ACLs, RF profiles and credential-free AP profiles.
Credential-bearing AP/SSID, other section and physical-operation protocols retain
their existing completion/admission rules. This adds no creator UUID receipt,
per-object custody, adoption or destruction authority.

## Verification

- Race-enabled selector/completion, public reconciliation and existing AP
  credential/prerequisite/no-dispatch tests pass. Pure completion tests require
  the tree endpoint and forbid root fallback for all seven selected families.
- Forty-two public ordinary Accepted Create cases cover complete, child-failed,
  child-pending, child-foreign, tree-unavailable and changed-source outcomes.
  The failed/untrusted cases retain the Accepted operation without replay;
  success converges without creating ownership or credential receipts.
- The same 42 cases pass against a real temporary Kubernetes API server.
- Seven isolated native cycles complete ordinary Accepted Create followed by
  same-UUID Update, original acknowledged Update recovery, certified preflight
  refusal/replan, stale closure refusal and exact cleanup. No Create is injected
  into Unknown during this qualification. Each family emits one Create, one
  Update and one exact Delete: 21 native mutations total. All typed baselines
  are restored, with no active operation or generated fixture left. Native
  Kubernetes persistence remains simulated; installed qualification is separate.

Production build and strict documentation checks pass. The complete regression
gate passes **19,621 test events across 16 packages**, including ND, shared
configuration/managed protocol, maintenance and direct-device/SWIM paths.

See [machine-readable results](wireless-accepted-create.json) for source
fingerprints, original task IDs, per-cycle results and gate log hashes.

## Fixture corrections and remaining work

An older credential-AP fixture answered only root task reads for its metadata
power prerequisite. It now uses the shared task-tree fixture; credential AP
completion was not widened. New temporary-API namespace names initially contained
collection underscores; RFC1123 names fix those fixture failures.

This closes the earlier normal-Accepted-Create completion exception described in
[AP dispatch qualification](ap-profile-dispatch.md). It does not resolve missing
creator identity evidence, installed/device behavior, complete field/catalogue
coverage or the other resource-specific physical/bulk and lifecycle gaps in the
[93-resource ledger](../../controllers/catalyst-center-resource-parity.md).
