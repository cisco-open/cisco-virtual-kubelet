# SNMPv3 NOAUTHNOPRIV manifest execution

The pinned NetAsCode 0.5.0 `device_credentials.snmpv3_credentials` model can now
apply explicit `NOAUTHNOPRIV` definitions through the public configuration
reconciler, with no secret projection or fabricated credential version.

## Implementation

- The existing network-settings adapter compiler routes credential-free
  definitions through the same global credential executor used for versioned
  definitions. Ordinary network settings retain their existing client compiler.
- The existing section ownership and write-ahead operation provide the
  preparation/submission/acknowledgement/recovery boundary. Selected native
  definitions are checked again twice by the shared submission guard.
- An existing `ActionApplications` receipt retains the independently observed
  UUID with its verified operation. Its stable key binds the definition name;
  the operation also binds the selected non-secret metadata. Create keeps its
  initially unknown target immutable, so the receipt key retains the UUID.
- Verification requires the complete acknowledged task tree, two matching
  metadata observations, a stable terminal task boundary and current
  config/source/controller/section ownership. Verified and the exact receipt
  are persisted together using the existing writer and pruning detection.
- Journal release requires the exact retained receipt. Source changes,
  uncertain submission, receipt pruning and replaced UUIDs remain fenced.
- Updates retain the original UUID; metadata drift can be repaired. Missing
  owned definitions are refused rather than silently recreated. Receipt capacity
  is checked before creation. Unowned matching definitions are not adopted.
- Versioned credentials and metadata-only credentials can coexist in one
  network-settings model and keep independent receipt types. Switching an owned
  SNMPv3 definition between metadata-only and authenticated modes is refused.

No transport, credential store, CRD shape or ND implementation was added.
Retention remains `Retain`; public deletion/pruning is not implemented here.

## Fixture validation

The affected race suite passed in 12.588 seconds. It covers public create,
update, drift repair, restarted no-op, exact UUID replacement refusal, stale
source and foreign owner refusal, running/failed child tasks, future task
completion, late readback replacement, atomic receipt persistence and schema
pruning, generic verification bypass and lost acknowledgement without replay.

The mixed versioned/SNMPv3 manifest test verifies independent receipts and
versioned rotation with projection-lag refusal while retaining the metadata-only
receipt unchanged. Combined site-assignment preflight checks that downstream
missing sites prevent an earlier metadata credential write. The new apply
example also passes compilation. Affected network/IPAM, AAA, credential, journal
and SWIM tests are included in the regression command.

The full Catalyst Center adapter race suite also passed in 585.840 seconds.
A final focused credential/composed-executor/example check passed in 7.120
seconds after the composed verification dispatch was updated. The strict
documentation build and whitespace checks pass, and the resource ledger retains
the exact 93 resource types from the pinned module.

## Native qualification

Final run: `TestLiveGlobalCredentialNoAuthManifest`, 11.82 seconds (package
13.703 seconds), passed using the Catalyst Center tunnel and authorized API
credentials. Each reconciliation reconstructs the worker-side reconciler over
simulated Kubernetes persistence.

1. Public manifest created an isolated, unassigned `NOAUTHNOPRIV` definition.
2. The verified action receipt retained UUID
   `df996279-7aeb-4311-9849-0a383a4a6198`; no credential-version receipt was created.
3. Three restarted reconciliations made no additional native write.
4. A new source generation changed the username using the same native UUID.
5. Three more restarted reconciliations made no additional native write.
6. The test deleted only that exact UUID, verified the cleanup task tree and
   confirmed the complete decoded non-secret credential baseline was restored.

Task IDs:

- Create: `01a13feb-2a45-7d55-95e4-4a623920ade4`.
- Update: `01a13feb-3ec7-7584-8a2b-351d23a01ec8`.
- Cleanup: `01a13feb-52b1-73e3-8dc3-7cd5e1a2a228`.

An earlier run also passed and removed UUID
`9deaf043-f366-40e0-b7b0-47d6f3c451e4`. Neither run assigned a definition to a
site/device or exercised SNMP traffic.

## Remaining qualification

Installed manager/worker reconciliation against real Kubernetes persistence,
device assignment and actual SNMP service behavior remain unqualified for this
mode. Combined models are fixture-qualified; this native run selected only the
isolated metadata credential. Cross-mode conversion and public prune/delete
remain unsupported. Other NetAsCode resource gaps in the resource ledger remain
open; this capability does not establish full model parity.
