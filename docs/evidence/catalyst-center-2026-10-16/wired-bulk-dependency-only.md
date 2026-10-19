# Dependency-only wired bulk intent and native child inheritance

Private bulk planning now accepts an original manifest containing IPAM and/or
external credential dependencies without requiring an unrelated scalar network
profile. No NTP, DNS, banner or timezone value is invented. An explicit but empty
network context with no independently retained dependencies remains refused.

The existing private v2 dependency observer still retains allocation UUIDs/data
and credential/site identity. Native dependency changes still invalidate the
operation. Empty scalar selections are explicitly represented per member, and
the existing worker command planner refuses them. No empty selection is a device
convergence proof; receipt publication still requires independent complete proof.
Public bulk dispatch remains unavailable and no common operation schema changes.

The shared private lifecycle suite now includes provision and reprovision for
IPAM-only, external-credential-only and combined dependencies without scalar
profiles, in addition to the previous ten lifecycle cases. Kubernetes actors and
independent device proof in these tests are simulated. This does not qualify
installed workers or physical provisioning.

A separate native check extends the existing generated credential lifecycle with
an empty child building beneath the generated area. The child is created through
the existing site planner and task reader. Native local/inherited slot states and
parent UUID ancestry feed the original-source dependency observer. Cleanup is
exact-UUID, child-before-parent and refuses changed/uncertain fixtures.

The [SNMPv3 run](wired-bulk-credential-inheritance-snmpv3-native.log) passed:
parent assignment and child inheritance produced a stable dependency identity;
clearing the parent slot invalidated the original identity. Credential, child
building and parent area removals were verified. No physical device was selected
or changed, and device authentication was not tested. The [remaining five-type inheritance run](wired-bulk-credential-inheritance-five-native.log) also passed in 237.04 seconds. All six types therefore have native parent-assignment to child-building inheritance qualification. Each exact child UUID was removed before credential/parent cleanup; both generated parent areas and all generated credentials were verified removed. Existing credential baseline identities were preserved.

Owned credential-definition/version receipt composition, actual device
convergence, installed actors, accepted native bulk mutation, and public bulk
activation remain pending. All resource and section parity flags remain partial.

Validation: [all bulk race regressions](wired-bulk-dependency-only-regression.log)
passed in 43.571 seconds, including sixteen lifecycle compositions and explicit
refusal of empty scalar proof plans. [Shared foundation checks](wired-bulk-dependency-only-foundations.log)
also passed for adapter registration, ND, configuration reservations, software
lifecycle and upgrades.

This qualifies native inherited credential identity in an otherwise empty generated
hierarchy. Native explicit child overrides, device authentication, installed
workers and actual bulk provisioning are still separate requirements. The
read-only existing-switch qualification previously skipped because that ancestry
had no named assignments; this generated positive fixture does not rewrite that
historical result.

The [composed worker-entry regression](wired-bulk-dependency-only-worker.log)
also passed all ten cases. The dependency-only case reaches Verifying after one
simulated native submission, then refuses scalar proof with zero DeviceOperations,
zero application receipts and the original operation fence retained. Existing
NTP/banner/timezone comparisons continue to pass. Strict documentation validation
also passed. Installed cluster recovery remains separately pending approval;
no installed runtime, etcd data, CVK worker or physical device was changed.

Subsequent [owned credential prerequisite composition](wired-bulk-owned-credentials.md) now connects existing applied-version and metadata ownership receipts to private bulk execution. The earlier missing-composition statements above describe their original qualification point; installed worker and public bulk gates remain pending.
