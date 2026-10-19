# Credential-free AP and SSID manifest execution

Metadata-only AP profiles and OPEN SSIDs could repeatedly prepare and release a
journal entry without sending the configuration request. Their payload planners
returned an allocated empty credential-version slice. Kubernetes omits that empty
slice from JSON; after journal readback it is nil, so the exact original-operation
comparison correctly rejected the different in-memory representation.

Both planners now return the canonical nil version list when there are no
credential intents. Versioned credential resolution and the common journal's
strict comparison, source checks, one-use submission boundary and no-replay
rules remain unchanged. No transport, controller factory, schema or physical
reservation change was needed. Regression tests demonstrate metadata-only
create/update/restart/no-op convergence and confirm that no credential application
receipt is manufactured. Tests also exercise actual Kubernetes CRD persistence
and generation changes with a controlled appliance.

## Catalyst Center qualification

The isolated AP qualification covers DAILY, WEEKLY and MONTHLY calendar changes,
two calendars, row reordering, explicit clearing, disjoint weekly day sets,
monthly boundary dates and leading-zero normalization, a negative timezone
hour with a positive minute offset, country changes, overlapping calendars and
a return to the baseline settings. Native metadata readback and fresh-compiler
no-op checks pass. Calendar execution on AP hardware is not attested.

The public manifest run uses the same shared configuration reconciler as normal
Apply. Two power prerequisites complete before AP creation. Fourteen distinct
native task IDs and fourteen journal operation IDs pass accepted-task JSON
restart, independent verification and no replay. Each updated source converges
to a public no-op. The one AP and two power definitions are removed by exact
captured UUID, with original AP/power/network-profile baselines restored.

The credential-free disabled OPEN SSID public run independently qualifies Create,
Update, two accepted-task JSON restarts and repeated current-source no-ops. It
retains the original UUID, remains disabled and unassociated, and restores the
SSID/network-profile baselines after its exact-UUID cleanup. There are two native
create/update task IDs and one cleanup task.

Both public native runs use the existing transport and real Catalyst Center API,
with simulated Kubernetes storage. Separate Kubernetes 1.35 API-server tests cover
real CRD serialization. These layers do not establish installed manager/worker
qualification; Docker still returns no response before its health-check timeout.
No daemon or cluster was restarted.

The shared qualification-only transport guard permits only the selected generated
AP/power/SSID operation, once. It refuses other names/UUIDs, enabled or
credential-bearing SSIDs, device-provisioning endpoints and repeated mutations.
Cleanup is limited to captured generated IDs. Guard tests and the native public
cycles pass under Go's race detector. No profile is assigned, no AP is provisioned,
and no switch software is changed.

## Stopped run and cleanup

The first public run exposed the journal mismatch after creating its two power
prerequisites; no AP was created. A controlled local regression reproduced the
failure before the planner fix. Those exact power UUIDs were retained and then
removed using explicit generated-fixture cleanup after checking AP absence,
references and unrelated baselines. This supervised test tooling does not add
provider adoption or automatic prune authority. The stopped journal snapshot is
historical and must not be replayed after cleanup.

[Machine-readable evidence](wireless-metadata-identities.json) records the final
source fingerprints, regression/API/race/native gates, completed private
qualification folders and cleanup results. Generic AP/power native Create tasks
still lack authoritative created-UUID binding for provider creator receipts;
supervised fixture cleanup must not be presented as automatic ownership/pruning.

## Interrupted metadata acquisition remains held

Investigation also identified the prerequisite for partial PnP cancellation.
AcquireMetadataFenceCohort checks custody, issues a separate Lease Create and then
publishes the observed UID in another status CAS. A lost Create acknowledgement
can therefore leave the next canonical Lease present but unrecorded. Source
invalidation also prevents ordinary acquisition from completing its current-source
checks. Current cancellation requires Ready custody and the complete original UID
cohort, so it cannot retire that partial state.

Partial retirement needs a durable acquisition-quiescence/drain contract that
accounts for recorded UIDs, the next unacknowledged create and potentially late
create requests before discarding custody. A missing key is not proof that a
previous request is finished. This slice does not publish artificial readiness,
release partial custody or infer an uncertain native outcome. That prerequisite
remains open for implementation and installed qualification.

## Model and remaining scope

The data-model pin remains NetAsCode 0.5.0, revision
a435555fe828f03cf3311d52fe1ab6572a247916. The
[AP model reference](https://netascode.cisco.com/docs/data_models/catalyst_center/wireless/ap_profile/)
and [Cisco AP response reference](https://developer.cisco.com/docs/catalyst-center/2-3-7-9/getapprofilesresponse/)
provide terminology; current upstream documentation is not substituted for the
pinned model or for observed lab behavior.

Full resource ownership, adoption/pruning, broader field combinations,
AP/WLC/client behavior, profile associations and installed actors remain pending.
Full pinned 93-resource execution/hardware parity and merge readiness are incomplete.
