# Private site manifest lifecycle

The public configuration reconciler now routes qualified sites-only manifests
with `prunePolicy: Delete`, `deletionPolicy: Retain`, private comparison keys and
bulk mode disabled through the existing guarded site lifecycle. No new CRD,
transport, mutation journal or provider-specific scheduling path is introduced.
The example `examples/configs/catalyst-center/sites-private-lifecycle.yaml`
shows the source and comparison-key projection contract; it was not applied to
the lab as a separate configuration.

Removing owned paths from the existing source prunes leaves first. Reintroducing
them recreates parents first and binds child recreation to the replacement parent
creator receipt. Ownership and retired cohorts remain attached to the same
configuration UID. Missing/replaced identities, reappeared retired UUIDs, foreign
children, unresolved consumers and retained image/AP/policy effects block writes.
Unknown acknowledged tasks recover through their original task trees without
resubmission. Configuration deletion still retains resources; this is not
`deletionPolicy: Delete` support.

## Qualification

The [validation record](site-public-lifecycle-validation.json) pins the exact
source hashes and completed logs. Race-enabled site regressions passed 69
families/1,028 subcases in 94.492 seconds. Real API-server fixtures passed nine
families/122 subcases in 93.412 seconds. Common configuration regressions passed
44 families/376 subcases in 13.531 seconds.

The native public-reconciler qualifier passed in 89.403 seconds against the lab
appliance: create parent/child, remove both from the source, restore both, then
remove both again. It observed four Create POSTs, four Deletes, eight distinct
tasks, two archived predecessor cohorts, eight retained receipt rows and one
owner, with no active operation and the typed inventory baseline restored.
Every acknowledged task was deliberately retained as Unknown before recovery.
The historical harness log label says “create task” for all actions; assertions
check actual action types and native POST/DELETE counts independently.

Native execution used simulated Kubernetes persistence. Real API-server tests are
a separate gate; this does not establish installed worker restart qualification.
Native mutation covered empty areas only. Building/floor lifecycle, installed
execution, automatic cleanup of retained effects, composite recreation with
images/APs and configuration deletion remain pending. Populated LAN workflows
and complete operational-reference coverage also remain separate work.

The tunnel was independently rechecked after qualification: Basic authentication
generated a token with HTTP 200. Neither credentials nor tokens were recorded.
The earlier retained unclaimed PnP test UUID has a successful exact cleanup result;
this lifecycle run did not claim devices or perform physical device operations.


Subsequent [hierarchy qualification](site-hierarchy-lifecycle.md) extends native
component lifecycle coverage to empty buildings and floors with parent handoff.
The area-only source/gate boundary above remains historical and unchanged;
installed worker execution and retained-effect cleanup remain pending.
