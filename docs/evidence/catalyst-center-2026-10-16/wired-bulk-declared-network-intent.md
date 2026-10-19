# Wired bulk provisioning: original declared network intent

The bulk source compiler now accepts `network_settings` as unmanaged dependency
context alongside `inventory` and `sites`. This advances the pinned NetAsCode
0.5.0 model without adding an alternate model, controller provider, transport,
ownership journal or dependency-version kind. Only inventory is managed by this
configuration; the context does not apply or acquire network-settings ownership.

## Source and native intent binding

Private site projections separate geometry/assets from `network_settings`
references. The existing complete geometry/floor/AP validators and the existing
network profile/reference parser both validate their projections. Undefined
profiles, invalid geometry, unsupported fields and missing context are refused.
Source bytes are not rewritten. Parent/Global settings resolve by the most
specific declared ancestor **per setting kind**, with deterministic output.

The operation's existing source UID/resourceVersion and config generation pin
the declared values. Recovery recompiles only from the unchanged source. A
changed source remains fenced; this implementation does **not** preserve a
historical content copy or reconstruct one from the controller's newer policy.
The generic dependency-version envelope stays reserved for template graphs;
no payload or secret hash is placed into it or the operation journal.

For declared context, the existing effective network-setting reader now checks
native policy against original desired values during owned preparation,
submission checks, retained batch reconstruction, worker-evidence collection
and both sides of the independent final-proof callback. Site inheritance uses
the existing validated native parent identities. Native mismatch fences work
before a new request, or retains the original accepted operation during recovery.
Credential/AAA contexts cannot substitute metadata for authentication evidence
and remain unavailable to this scoped verifier. Legacy inventory-only private
flows retain their existing contract; absence of context is not a new proof.

## Scoped device comparison

`observeWiredBulkDeclaredNetwork` combines this declared context with the
[existing post-task worker collector](wired-bulk-worker-evidence.md). It builds
bounded IOS-XE queries from original NTP/DNS fields and compares fresh,
identity-bound worker results with those fields. NTP addresses/hostnames are
canonicalized and compared as a set; DNS resolver order is retained. Wrong or
extra servers, duplicates, different domains, unsupported VRF/options, malformed
CLI and status-only text are refused. The domain query selects the name field
rather than unrelated domain-lookup configuration.

Other settings can be checked as native **policy**, but do not acquire device
verification support from this NTP/DNS parser. The method is deliberately not
connected as a final `wiredBulkConvergenceProof`. A successful scoped comparison
does not cover switching profiles, templates, credentials, all provisioning
effects or actual network service behavior. It publishes no receipt or release.

## Verification

- [Regression log](wired-bulk-network-regression.log): race-enabled bulk,
  configuration source/journal, network settings, sites, floor images and planned
  AP suites. Includes profile/geometry projection, inherited and nearest scope,
  source immutability, and original native policy changing before preparation,
  after preparation, during dispatch, after acceptance and during proof.
- [Scoped semantic log](wired-bulk-network-semantic.log): 21 bounded CLI cases
  and three complete source/native/task/worker compositions. Actual device
  policy matching the original source succeeds for the scoped fields;
  different policy and nonsemantic output fail. Manager/worker responses are
  explicitly simulated. Even the matching case leaves Verifying state and
  reservations in place and cannot publish a missing whole-device proof.
- [Foundation log](wired-bulk-network-foundations.log): registry, ND, shared
  controller configuration, software lifecycle, software upgrade and
  DeviceOperation package suites.
- [Native NTP-only attempt](wired-bulk-network-native-ntp-skip.log): skipped
  honestly after 24.61 seconds because an assigned lab device had no settled
  NTP policy; this is not successful NTP device qualification.
- [Native scoped policy qualification](wired-bulk-network-native.log): all
  three switch identities were read. A test-only resolved source was explicitly
  constructed from their existing timezone policy and geometry snapshot.
  The original source matched, a separate changed-source fixture was refused,
  and the original source/native policy was rechecked unchanged. One site scope
  and one batch; 41.79 seconds (package 44.170 seconds), configuration mutation
  attempts zero under a transport guard. No native policy was changed, no
  claimed task was invented, and no installed device-worker result was supplied.

## Remaining work

A [subsequent scalar stage](wired-bulk-scalar-device-evidence.md) now adds scoped
Custom MOTD and fixed-UTC worker comparisons. Native scalar output qualification
and the full-device proof gates below remain open.

This retains and verifies **declared scoped policy**, not the complete rendered
configuration that Catalyst Center may push. Full rendered-intent binding,
switching-profile/template composition, device semantics for the other network
settings/credentials, historical recovery when an accepted operation's source
is replaced, native NTP/DNS worker qualification, installed actors and accepted
bulk POST/PUT cycles remain pending. Public bulk execution and all whole-resource
parity flags remain gated. The 93-resource ledger is still partial.
