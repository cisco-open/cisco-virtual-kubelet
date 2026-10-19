# Public credential-bearing discovery creation and recovery

Follow-up: [global-only/credential-bearing row composition](discovery-credential-composition.md)
now has its own qualification. This record retains its earlier source boundary.

The public Retain-only Apply path now connects the credential parser/projection,
guarded resource executor and atomic creator/version writer to CVK's existing
configuration loop. It reuses the discovery client, global reference resolver,
shared permits, timeouts, original-task observation, status CAS and slot release.
No alternate transport, credential store, CRD or retry state machine was added.

## Controlled and Kubernetes API qualification

The executor cycle exercises shared prepare/start/submit/Accepted/Verifying/
completion/finish transitions. It verifies exactly one request, restart no-op,
known-task Unknown recovery, lost accepted/permit acknowledgements, ambiguous
create responses, an imported job, a job appearing after planning, changed
projection, revoked ownership and replaced global references. Repeated calls use
the shared dispatcher; the executor has no independent retry permission.

Thirteen public reconciliation scenarios run with controlled native responses and
again with a real Kubernetes API server:

- Inline, ConfigMap and mixed global/inline creation converge with one POST and
  the paired creator/version receipts.
- A natural one-nanosecond configured timeout retains the original task in
  Unknown, then recovers it without a second submission.
- Lost accepted acknowledgement recovers its durable task; lost permit
  acknowledgement and taskless ambiguous creation keep their fences.
- Imported jobs are not adopted.
- After convergence, a missing credential receipt, replaced native UUID, public
  metadata drift or a proposed credential rotation loses InSync without a new
  mutation or changed retained audit evidence.
- Adding an unrelated controller credential alias preserves completed receipts
  and no-op convergence, while current operations keep the common generation
  binding.

Two additional multi-job scenarios run in controlled and API-server persistence.
Two jobs create serially and retain separate task/UUID/version pairs across worker
reconstruction. A later unowned conflict prevents creation of the earlier missing
job. Thirteen unsupported/unauthorized public cases fail before any native I/O or
journal mutation, including extra sections, unsupported later jobs, global-only
row composition, raw credentials, missing/lagged counters, invalid SNMP groups,
unknown destinations, private-comparison use, revoked aliases and Delete policies.
The operator example compiles with synthetic projected inputs.

An initial ConfigMap fixture lacked a UID; it now supplies fixture identity, while
the API-server cases use persisted Kubernetes identity. An initial proposed
endpoint-change fixture was correctly rejected by the existing immutable-endpoint
CRD. The final alias-addition case checks a permitted spec change instead; it does
not weaken that schema rule or treat a changed endpoint as the same controller.

## Native read-only qualification

Three reconstructed public reconciles against the actual lab refused to adopt
the existing `Switches` discovery job. A read-only transport blocked all product
mutations independently of the executor. The discovery baseline (one job) and all
three device UUID/serial/address identities remained unchanged. Fixture Kubernetes
persistence may claim its shared section owner; it must not create an operation,
submission permit, creator receipt or applied-credential receipt.

The initial native assertion incorrectly disallowed harmless shared owner
bookkeeping; the corrected assertion checks those actual mutation/adoption
boundaries. Both logs are retained. Synthetic projected device credentials were
never sent to the appliance. **No live credential delivery, device authentication
or installed worker rollout was qualified.** Authorized device CLI/SNMP inputs
are still needed; the Catalyst Center API account is not assumed to be a switch
account.

## Coverage boundary

This advances `catalystcenter_discovery` execution; it does not complete that
resource or the 93-row model ledger. Rotation/removal history, owned replacement,
Delete/prune, broader composition, source-loss recovery and live/installed
qualification remain open. The previous private-stage and optional-null audits
retain their original source boundaries; no current full-package or hosted-CI
pass is inferred from them.

The [validation record](discovery-credential-public-execution.json) records source
hashes, commands, logs and remaining gaps. See the
[operator guide](../../controllers/catalyst-center-discovery-credentials.md).
