# Private wired bulk external credential identities

The private bulk dependency observer now supports existing named site credential
references for all six NetAsCode slots. It reuses the global metadata reader and
site assignment executor. No returned credential material is decoded or hashed.
Credential definitions and explicit inheritance-removal intent still require
separate ownership/version receipts and remain refused by this private bridge.

The observer binds the exact native credential UUID and public metadata, local
site slot state, site UUID and parent ancestry. For each inventory site, the
nearest declared assignment must agree with the effective native chain. An
undeclared child override or disabled slot cannot masquerade as its ancestor's
assignment. A declared child assignment may override the parent. All declarations
remain bound, including those outside the currently dispatched batch.

Two complete observations must agree. The existing private v2 bulk identity now
supports credential-only or combined allocation/credential evidence. Existing v1
and allocation-only v2 identities are preserved. No common CRD, journal schema,
ND or software upgrade path was changed. This is dependency identity evidence,
not password-version or rendered-device authentication proof. It does not grant
credential ownership or enable the public bulk execution gate.

## Local verification

- [Bulk regressions](wired-bulk-credentials-regression.log): race tests passed,
  including ten private provision/reprovision lifecycle compositions (legacy,
  original geometry, IPAM, credentials, combined). Device proof and Kubernetes
  actors are simulated; this is not an installed-worker qualification.
- [Boundary races](wired-bulk-credentials-fences.log): fifteen scenarios passed,
  including same-name credential replacement before dispatch, after acceptance,
  during proof and after final authorization; an effective child-slot change
  after final authorization retains the fence and prevents receipt publication.
- [Credential compatibility](wired-bulk-credentials-compatibility.log): all six
  external slots, nearest declared child, malformed/missing/ambiguous metadata,
  disabled/inherited/overridden slots, ancestry changes and two-observation races
  passed alongside existing global/site/versioned credential tests.
- [Foundations](wired-bulk-credentials-foundations.log): adapter registration, ND,
  shared configuration reservations, software lifecycle and upgrades passed.

The [pinned ledger comparison](wired-bulk-credentials-ledger.log) confirms all 93 upstream resource types remain represented without extras; this is scope coverage, not execution completion.

## Native qualification

The [read-only existing-lab run](wired-bulk-credentials-existing-native.log)
found no named assignments in the ancestry of the three selected switches. Its
positive qualification explicitly skipped. A passing Go process containing that
skip is not evidence of native credential inheritance support.

Separate explicitly scoped native checks use the existing generated credential
and empty-area lifecycle fixture. The [SNMPv3 run](wired-bulk-credentials-snmpv3-native.log) passed in 46.22 seconds, including native create, versioned rotation, assignment, stable bulk dependency binding, cleanup-triggered refusal, and verified removal of the exact generated credential and empty area. This is native adapter qualification with synthetic projection/receipt fixtures, not a deployed Kubernetes worker. It adds dependency reads while the exact
assignment exists and checks refusal after isolated slot cleanup. It creates no
second mutation implementation, does not select a physical device and must not
be described as accepted bulk provisioning or device authentication.

Public automatic bulk execution, owned credential-definition/version composition,
full device convergence, installed actors and accepted live bulk mutation remain
pending. All resource and section parity flags remain partial.

The [remaining five-type run](wired-bulk-credentials-five-native.log) also passed:
CLI, HTTPS read/write and SNMPv2 read/write. Across the two positive runs, all six
credential types completed native create, versioned rotation, exact assignment,
repeated stable dependency binding, refusal after isolated slot cleanup, and
verified credential removal. Both generated empty areas were removed and verified
absent. Existing credential baseline identities were retained. These checks
qualify the local empty-area mapping; native child-site inheritance, accepted
bulk provisioning and actual device authentication remain unqualified. The
existing-switch read-only skip above is retained as a distinct limitation.

The strict documentation build passed. No installed cluster recovery or physical
device upgrade was performed during this increment.

Subsequent [dependency-only and native child-site qualification](wired-bulk-dependency-only.md) now covers all six credential types through generated area/building ancestry. The older child-inheritance limitation above records the earlier run; native explicit child overrides and physical authentication remain unqualified.

Subsequent [owned credential prerequisite composition](wired-bulk-owned-credentials.md) now connects existing applied-version and metadata ownership receipts to private bulk execution. The earlier missing-composition statements above describe their original qualification point; installed worker and public bulk gates remain pending.
