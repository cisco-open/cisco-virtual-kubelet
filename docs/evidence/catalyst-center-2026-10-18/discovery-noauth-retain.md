# NoAuth discovery with Retain/default retention

The applied implementation admits initial SNMPv3 NoAuthNoPriv discovery with explicit
Retain or the standard omitted deletion policy. It uses the same compiler, source
attestation, v4 action-only input, one-use dispatcher, native task/readback and
journal as the enrolled Delete flow. No provider, transport, CRD, operation store
or finalizer was added. The existing Delete enrollment and cleanup fence remain.

A Retain config receives no cleanup finalizer. Deleting it preserves the native job
and old UID's reservation/audit; a replacement UID cannot inherit them. Deleting a
never-submitted Retain config likewise does not invent a cancellation or native
absence proof. Configs carrying discovery/feature cleanup finalizers cannot use
initial Retain admission to bypass their lifecycle. Choose the Delete example when
manifest-driven cleanup is intended; this does not silently adopt a retained job.

The existing scenario runner now covers fourteen Retain cases with controlled
storage and again with a real isolated Kubernetes API. Cases include explicit and
omitted policy, ConfigMap sources, original Unknown task recovery after source loss,
restoration without replay, first projected delivery at version 1, return to metadata,
version-2 reintroduction, stale-version refusal, a versioned-only origin, retained
Prepared work and incompatible-finalizer refusal. These preserve the native job,
immutable receipts and logical-field floors while blocking new-UID takeover. The
existing Delete scenarios and thirteen Retain no-dispatch fault cases per backend
also pass. All apply examples compile, including
`examples/configs/catalyst-center/discovery-noauth-retain-apply.yaml`.

The combined targeted gate passed **303 events**. Live qualification then passed
**eleven read-only cases**: five versioned cancellation, five NoAuth Delete
cancellation and one Retain final-post-permit refusal. Each used an isolated real
Kubernetes API and a transport refusing appliance mutations except authentication.
Every case preserved one native discovery job and three devices with zero mutation
attempts. Main build, fresh foundation/ND/direct-upgrade/maintenance tests and
SWIM/XFSU/transfer fallback checks passed.

The first full regression exposed mixed-family reclassification after a versioned
creator appeared; it is retained as a failed gate. The ordinary-receipt fence
correction passed 209 composition/prune/lifecycle/example events. Pending ordinary
operations now reserve the same family too; expanded correction checks and native
generated-job qualification are running before full regression is repeated. This evidence
qualifies bounded execution, retention and refusal/cancellation behavior; it does
not by itself qualify native creation. The successful native definition lifecycle
is recorded below; device authentication, installed actors, ordinary/typed custody
migration and complete 93-resource parity remain open. See the [source-specific gates and records](discovery-noauth-retain.json).

## Native create/recovery/cleanup qualified

A generated NoAuth job against the approved `.101` lab target now passed one
native Create, original acknowledged Unknown-task recovery, action-only receipt
publication, manifest-driven Delete and manager finalizer release. Exactly one
POST and one DELETE were attempted. Cleanup was automatic, and the one-job,
three-device baseline was restored. The passed live qualifier is retained at
`internal/controlleradapter/catalystcenter/discovery_noauth_execution_live_test.go`;
its function body matches the qualified staging source. The one-off cleanup
helper for the earlier failed fixture was not added to the repository.

The first native attempt exposed a real response-shape difference: the API
returned the NoAuth mode and username, omitted `snmpVersion`, and used `Single`.
Its original-task-bound generated job was supervised-cleaned with one exact
DELETE before a fresh attempt. The failed gate and cleanup remain separate.

The reader now canonicalizes documented mode spellings and derives `v3` only
when a recognised v3 mode and a nonempty SNMP username provide positive evidence.
It preserves explicit versions and does not default unknown or incomplete
metadata. Nine decoder/effect cases cover both positive and refusal behavior;
the combined readback/family/lifecycle/prune/example gate passed 377 events.
The [Cisco response schema](https://developer.cisco.com/docs/catalyst-center/get-discovery-by-id/)
documents the mode/user fields and does not expose a response `snmpVersion`.
No new operation protocol, credential receipt, store or transport was introduced.

The corrected full regression and fresh compatibility checks subsequently passed; see the source-specific terminal evidence below. This
qualifies the controller's NoAuth job definition lifecycle with isolated real
Kubernetes persistence. It does not prove device authentication, installed actors,
native projected credential delivery, broader imports or complete model parity.

## Corrected full regression

The frozen 1,642-file source snapshot `c637f7e6aae625dd4a5f26efe9a8ecf14b8964e59ec26cdd794971c74153222e` completed the full affected discovery/credential/configuration/examples race suite with 2806 passing events in 1,316.824 seconds and exit 0. The earlier failed run remains recorded separately; this result qualifies the corrected implementation. Installed actors, device authentication and full pinned-model parity are still unqualified.
