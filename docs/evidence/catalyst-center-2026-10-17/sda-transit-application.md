# SDA proof-required completion applications

SDA now has a private completion bridge to the existing atomic group receipt
protocol. It requires a semantic verifier in addition to complete fresh worker
reads, successful original task-tree execution and exact native/managed/source/
owner evidence. There is no default semantic verifier and public acknowledged
recovery continues waiting at `GroupConvergenceVerificationUnavailable` after
fresh worker results. This is completion plumbing, not finished SDA execution.

## Common lifecycle and ownership

`recordSDATransitApplication` loads the actual declared source and binds the exact
retained Verifying operation. Every admitted CP must have fresh, successful,
query/worker/chassis-bound output through the existing DeviceOperation reader.
The existing `observeConfigurationGroupConvergence` brackets the supplied proof
with complete repeated observations. Desired references, operation, targets and
output slices passed to the verifier are private copies. Callback mutation cannot
rewrite the journal, evidence or comparison baseline.

The native result UUID, logical definition and canonical complete physical group
bind the application key. Original source, nonce, task, dependency fingerprint,
claims and group remain unchanged in the retained operation. The key contains no
raw device output. This evidence does not establish exclusive native creator
ownership; adoption, replacement and destruction remain unavailable.

Before persistence the bridge repeats semantic verification and the entire
observation after current authority checks, requiring the same native result,
task completion and worker evidence. It then uses the existing
`persistVerifiedConfigurationGroupReceipt` / `PersistVerifiedGroupReceipt` path
to atomically write the Verified operation and application receipt. There is no
new schema, transport, reservation store, release path or CAS retry. A lost write
acknowledgement is returned; the retained Verified operation prevents replay.

Receipt publication does not complete, release or settle any member. The shared
completion/worker/manager protocol remains responsible for those steps. Fixture
qualification proves that journal retirement refuses unsettled members, then
accepts all-member simulated completion/release/settlement and retains the
historical application after clearing the active operation.

## Verification

- [Race regression](sda-transit-application-regression.log): 159 SDA subcases plus
  standalone checks pass in 33.502 seconds. Twenty new cases exercise successful
  persistence, missing/rejected proofs, pending/partial/disabled evidence,
  undeclared or wrong operations, callback isolation, native/task/source drift,
  authority/member revocation after proof, rejected or changed second proof,
  duplicate receipts, deliberately stripped receipts and lost acknowledgements.
  The successful case also exercises shared simulated settlement and retirement.
  Native service, grants, worker output and semantic verification are fixtures.
- [Real API envelope](sda-transit-application-api.log), 9.517 seconds, checks that the actual CRD
  preserves a bridge-generated Verified operation and full historical receipt
  through a status write and fresh read, without persisting raw device output.
  This is a schema-envelope test on a separate ephemeral controller, not installed
  admission, live semantic proof or a complete real-API reservation cycle.
- [Native read-only prerequisites](sda-transit-application-native.log), 12.512 seconds, resolve the
  three lab aliases and assess their combined source; missing provisioned-device
  records still prevent readiness. No product writes or live device reads are
  performed by this qualification.
- [Shared foundations](sda-transit-application-foundations.log) cover controller
  contracts, ND, device operations, diagnostics, lifecycle, upgrades and GNOI;
  unchanged packages may use cached results.
- [Current source/upstream fingerprints](sda-transit-application-sources.json)
  and [strict documentation build](sda-transit-application-docs.log).

## Remaining work

A real semantic convergence verifier must be implemented and qualified for the
supported SDA modes before the public path can invoke this bridge. Native
provisioning prerequisites, accepted live SDA creation, installed device workers
and real all-member settlement remain unqualified. New public intent sequencing,
automatic Prepared dispatch, provisioning orchestration, full mixed-model
composition, updates/consumer footprints and creator/adoption/destruction
ownership remain pending. The complete pinned NetAsCode goal is still open.
