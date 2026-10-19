# Metadata fence settlement admission

The canonical bootstrap fence policy permits manager settlement of explicitly
metadata-marked records. Creation cannot carry release evidence. Settlement
preserves binding and metadata, adds a release digest and removes the evidence
finalizer. Workers cannot settle or delete records. Unmarked fences remain
immutable and retained. Historical unmarked protection digests remain readable;
new writes require the current exact live protection policy. Legacy comparators
reject metadata-marked records.

Real isolated API tests reject premature deletion, marker adoption, changed
binding/holder/ownership, malformed release evidence, worker settlement and stale
UID deletion after replacement. Exact manager settlement and UID/resourceVersion
conditional deletion pass. Release digests are staged test evidence: admission
checks actor and shape, not actual native outcomes.

Qualification passed 63 real API test/package events, 3317 events across seven
shared API, manager, ND and upgrade packages, and 1049 compatibility events.
Main build, chart rendering and frozen-source checks pass. One test was updated
to normalize installation literals in shared CEL variables; production source
remained unchanged between initial and final foundation runs.

The restored lab tunnel accepts token authentication. All three switches are
Reachable (.100/.103: 17.18.3; .101: 26.02.1). Historical PnP record
6ad1123f0bc0186771caae30 returns HTTP 404. Rechecking performed no writes.

## Remaining runtime work

Canonical fence acquisition, persisted original references, compatible worker
claim/dispatch, original-outcome verification and manager release/restart recovery
remain pending. The composition-root compatibility callback stays unset;
automatic sealing and public new pruning remain disabled. Installed/native
lifecycle qualification, complete consumer protection, deletion finalization
and other pinned 93-resource execution/hardware gaps remain open.

[Results and fingerprints](pnp-metadata-fence-settlement.json).
