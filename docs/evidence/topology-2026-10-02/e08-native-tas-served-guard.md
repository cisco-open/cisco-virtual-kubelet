# E08 native TAS served-object guard

Date: 2 October 2026

Candidate: `12b513b7`

The Kubernetes 1.37 native TAS lane previously proved scheduler behavior, and
unit tests proved CVK's fail-closed `spec.schedulingGroup` inspection through a
fake unstructured reader. Neither result alone proved that the CVK guard could
recognize the field on an object actually served by Kubernetes 1.37 while the
CVK binary used its older typed core/v1 dependency.

The disposable lane now invokes
`TestNativeTASServedSchedulingGroupGuard` against the scheduled
`edge-worker-0` Pod. The test:

1. refuses every context except the exact expected `kind-*` context;
2. fetches the Pod as an unstructured core/v1 object and confirms that the API
   server retained `spec.schedulingGroup`;
3. calls the production rollout drain guard with the Pod's exact UID and
   resourceVersion and requires a fail-closed scheduling-group rejection;
4. performs a harmless metadata update and requires the stale observed object
   to fail the resourceVersion race check; and
5. retries with the current resourceVersion and again requires the group-aware
   rejection.

The full pinned Kubernetes 1.37 lane passed locally with kind v0.33.0 and the
repository-pinned node image. It also passed co-location, member replacement,
maintenance block/recovery, insufficient group capacity and kube-scheduler
process restart. The disposable cluster was deleted after the run.

This closes E08-A's real served-object recognition boundary and strengthens
E08-B. It does not enable grouped eviction. E08-C/D still require a qualified
native workload controller, a portable signed application and physical CVK
service-continuity/group-drain evidence.
