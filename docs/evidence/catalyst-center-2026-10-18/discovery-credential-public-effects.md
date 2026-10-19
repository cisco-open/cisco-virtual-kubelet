# Versioned public-field proof for credential-bearing discovery

Credential-bearing creator receipts now include a closed public-field mask and
canonical native fingerprint. This is groundwork for guarded replacement and
cleanup; **credential rotation, replacement, removal and deletion remain disabled**.
The pinned NetAsCode model remains 0.5.0.

## Upstream lifecycle and proof design

The inspected official provider
[v0.5.11 discovery schema](https://github.com/CiscoDevNet/terraform-provider-catalystcenter/blob/v0.5.11/internal/provider/resource_catalystcenter_discovery.go)
documents replacement and marks credential fields `RequiresReplace`. Its Update
method performs no appliance mutation. Real credential rotation therefore needs
a qualified Delete/Create lifecycle, not an invented PUT operation or a local
state-only change. This provider inspection does not change the model pin.

| Record | Meaning |
| --- | --- |
| Input fingerprint | Public intent and authorized version/source labels; not native-effect or submission proof |
| Creator receipt v1 | Original verified task/UUID and paired credential-version evidence; compatible read-only convergence |
| Creator receipt v2 | The same original task/UUID/version evidence plus the declared public-field mask and canonical native fingerprint |

New successful creation writes the v2 creator key, credential application and
Verified operation in the same existing status CAS. The typed native reader drops
all password/community/passphrase properties. The effect helper accepts only the
closed public vocabulary and scalar/set types; full credential-bearing bodies,
unknown fields and nested objects are refused before hashing. Password bytes and
their hashes do not enter status. Canonicalization reuses the ordinary discovery
normalizer without widening its field mask or credential-reference requirements.
An explicitly declared empty filter preserves the existing equivalence of native
null, empty string and empty list; omitted source predicates remain unselected.

The v2 key binds the name hash, selected public mask, canonical public digest and
native UUID. Current convergence independently checks both the declared public
effect and fresh native metadata against it. Unknown receipt versions, malformed
masks, altered scope and drift remain fenced. The ordinary ownership selector
recognizes both versions, preserving the cross-family reclassification refusal.
No new CRD, Secret store, transport, operation phase or submission permit exists.

## Existing v1 records

An unchanged, complete v1 creator/version pair can acquire the public fingerprint
through one idle status-only CAS. Current public intent must match its original
input fingerprint, authorized version/source bindings and native UUID. The source,
controller, owner and exact journal snapshot are checked again before publication.
The original operation, delivery versions, source identity and verification time
remain unchanged. The next reconciliation replans; it does not submit a request.

This promotion does not replay or reread the original expired task. The successful
task/UUID/version evidence must already be durably complete. A pending operation,
unverified pair, foreign UUID, changed scope, failed publication or missing data
cannot become a new proof. Lost publication acknowledgement recovers the already
stored key without a second status publication or native request.

Promotion also respects the existing 256-entry/256 KiB action-receipt budget. A
schema-valid historical audit just below the soft byte cap exercises the boundary:
if enriching the key would cross it, CVK retains the v1 read-only no-op instead of
regressing convergence or inventing v2 lifecycle authority. A future destructive
flow must still require its qualified stronger proof.

## Qualification boundary

Fourteen controlled scenarios and the same scenarios through a real Kubernetes
API cover promotion, lost acknowledgement, conflict, pruned publication, native
drift, foreign UUID, active job, missing delivery evidence, unknown version,
changed mask/digest, forged public intent, busy journal and the byte-budget edge.
Successful promotion preserves original operation/time/version evidence and makes
no POST or expired-task GET. The API separately rejects a receipt missing its
required version list; compatibility faults remove the optional receipt rather
than weakening that schema rule. The initial invalid fixture and corrected
qualification are retained in the logs.

Fingerprint tests cover type/address/protocol/set normalization, rejection of all
eight secret leaf types and nested objects, known public SNMP/CLI metadata,
ignored credential echoes, metadata drift and malformed key versions/masks.
Current public discovery/composition, ordinary owned lifecycle, shared journal and
credential/API regressions remain in the broader qualification group. A final
empty-filter compatibility case is included in the terminal current-source run.
One test build returned FAIL without a compiler diagnostic; isolated and complete
sequential diagnostic builds/tests then passed with vet, race detection and API
cases enabled. Its cause is not established and the failed log is retained.

**Native v2 creation/promotion and installed workers remain unqualified.** Earlier
read-only lab refusal checks did not exercise an owned credential-bearing job and
are not relabeled as this feature's native qualification. Authorized device
CLI/SNMP inputs are still needed. Replacement/rotation/removal history, exact-ID
retirement and cleanup, source-loss recovery and the other pinned-model gaps
remain open in the [93-resource ledger](../../controllers/catalyst-center-resource-parity.md).

See the [validation record](discovery-credential-public-effects.json) for source
hashes and terminal test/build results. No current full-package or hosted-CI pass
is inferred from older audits.
