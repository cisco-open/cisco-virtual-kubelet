# Acknowledged unowned wireless Create recovery

## Native ownership evidence investigation

A read-only inspection of six previously acknowledged wireless Create tasks found
no created native resource ID in the legacy task response, documented task-detail
response or rooted task tree. The legacy service is Common Setting Service;
task-detail contains progress only. The [structural inspection](wireless-create-task-inspection.json)
retains field names/types and comparison with the independently captured native
ID; no arbitrary task body, failure text or credential material is retained.

The [correlation inspection](wireless-create-correlation-inspection.json) also
found HTTP 204 for audit queries by the original task correlation and generated
name. Existing interface collection/detail responses expose only id,
interfaceName and vlanId, with no creation timestamp or task link. These are
bounded observations of the checked paths, not proof that every possible
controller-internal correlation mechanism is unavailable.

Cisco documents task-detail data as variable task-associated content in the
[2.3.7.6 task-detail API](https://developer.cisco.com/docs/catalyst-center/2-3-7-6/get-task-details-by-id/).
The documented [audit API](https://developer.cisco.com/docs/catalyst-center/2-3-7-5/get-auditlog-records/)
provides context and description filters. Neither documentation nor native
metadata presence is treated as a task-bound created-UUID ownership receipt.

## Implemented recovery boundary

The ordinary wireless definition paths now resume observation of an acknowledged
Create retained as Unknown when its exact selected resource, config/owner/source
revision, original task ID and nonzero submission time remain bound. Recovery
requires the complete rooted task tree to succeed before the existing declared
native-state verifier can complete the operation. No mutation closure is called
again. A taskless or unacknowledged outcome remains fenced.

This is the existing ordinary Retain-only Create convergence contract after an
uncertain observation boundary. It does not establish which native UUID the task
created, fabricate an action/creator receipt, adopt a same-name replacement as
owned, or authorize deletion/pruning. A later same-name object can satisfy
unowned desired state; that fact cannot become creator ownership. Ordinary
accepted Create completion remains unchanged; this new Unknown recovery requires
the stronger full task-tree observation. Existing Update recovery is preserved.
AP/SSID, feature-template ownership and their distinct completion contracts are
unchanged. No common journal schema, transport or provider interface was added.

## Verification

- [Scoped/public tests](wireless-create-recovery-tests.log) pass in 14.978 seconds,
  with exact resource/group selection and explicit no-ownership assertions.
- [Complete wireless regression](wireless-create-recovery-regression.log) passes
  in 112.378 seconds. Public recovery now covers 138 cases across six resources;
  90 direct completion cases require full rooted trees for acknowledged Update
  and recovered Create. A combined six-resource manifest recovers twelve original
  Create/Update tasks with exactly twelve writes and no creator receipts.
- [Real private API-server tests](wireless-create-recovery-api.log) pass all 48
  cases: retained Create serialization, taskless/source/missing-native refusal,
  failed/pending/foreign child trees and unavailable trees. Native services and
  task/worker evidence in these API tests are controlled fixtures. The private
  server is Kubernetes v1.35.0; the installed cluster is not used.
- [Native qualification](wireless-create-recovery-native.log) passes all six
  cases in 82.71 seconds: anchor groups, interfaces, power profiles, 802.11be,
  pre-auth ACLs and RF profiles. Each original acknowledged Create is retained
  Unknown and recovered without another POST; the original Update is likewise
  recovered. Three no-op reconciliation passes add no writes. Each generated
  unused definition receives one Create, one Update and one exact captured-ID
  Delete. All six typed collection baselines are restored, with no per-object
  ownership receipts published. Kubernetes persistence is simulated in this
  native run; no WLC/AP deployment occurs.
- [Foundation checks](wireless-create-recovery-foundations.log) pass for adapter
  registration, ND, common reservations, software lifecycle and upgrades.

Created-UUID ownership, ownership-based source prune/CR deletion, taskless
recovery, installed actors and WLC/AP behavior remain pending. The inspected
appliance did not supply usable Create-result UUID evidence through these paths.
All six resource rows and whole-section parity flags remain partial. This
recovery increment does not close the full 93-resource goal.
