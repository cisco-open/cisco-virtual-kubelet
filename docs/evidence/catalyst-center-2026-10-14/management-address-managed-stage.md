# Management-address managed preflight and write staging

Management-address preflight now reuses
`observeConfigurationManagedTargets`, the existing CiscoDevice/Node identity
rules, cross-namespace physical alias detection and immutable group-target
construction. Native observations bracket repeated Kubernetes binding reads.
The result retains native UUID, current address, requested address and the
current CiscoDevice/Node UIDs. A no-op still requires a valid managed binding;
it requests no authority and produces no write.

Private managed write staging requires this preflight and a separately supplied
reservation/endpoint guard. It repeats native and managed observations on both
sides of that guard, refusing changed managed incarnations or declarations.
It then uses the existing guarded one-use native mutation primitive. This does
not create or grant a reservation, patch a CiscoDevice or enable the public
compiler.

Fixture tests cover a valid change/no-op, old-address preservation, missing
device/node, replaced Node UID, cross-namespace alias, unsupported driver,
native address drift, guard denial, guarded native submission, premature
declaration changes and Node replacement during endpoint proof, plus replay
refusal. Native execution and existing managed identity regressions passed in
the same race run (3.211s); see
[retained output](management-address-managed-stage.log).

The current common reservation candidate records `ManagementAddress`,
`DeviceGeneration` and `DeviceWorkerRevision` alongside device/Node/worker
identity. The migration bridge must coordinate controller endpoint convergence
and subsequent device/worker address alignment without invalidating that retained
binding or relaxing exact managed address agreement. The helper does not solve
post-mutation rollout or safe settlement by itself.

Full source/owner/task persistence, real dispatch permits, new-endpoint physical
reachability, CVK address/worker synchronization, failed/unknown recovery,
installed actors and an accepted isolated live transition remain pending. No
lab mutation was performed in this increment; prior live guard-refusal evidence
remains separate from accepted mutation qualification.
