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

## 4 October: native Deployment owner and process restart

A new subtest of `TestNativeTASServedSchedulingGroupGuard` uses the real in-tree
Deployment/ReplicaSet controllers, not manually recreated Pod fixtures. It
creates a two-member Deployment whose template references a pre-created native
PodGroup. It verifies:

1. the served Pod template's group reference survives native controller creation;
2. both members bind within one single-node site domain;
3. each Pod's ReplicaSet UID and its Deployment owner UID match the original
   ownership chain;
4. stopping the actual kube-controller-manager CRI container in the owned kind
   cluster results in a different running container (not just a mirror-Pod
   deletion);
5. after that restart, deleting one exact synthetic Pod UID causes Kubernetes
   to create a new member with the same group and owner identities; and
6. the original PodGroup UID remains unchanged.

The test never creates the replacement Pod itself. It asserts the exact kind
context and Docker cluster label before stopping the controller process. Test
objects and the disposable cluster are cleaned up. The zero-grace deletion is
only a synthetic fixture fault; it is **not** a supported CVK drain operation
or a PDB bypass on a physical device.

The full lane passed with **kind 0.33.0**, **kubectl 1.37.0** and pinned image
`kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5`.
The new owner/restart subtest took 21.30 seconds. The served-field safety guard,
site binding, maintenance/capacity negatives and scheduler-process restart also
passed. Raw capture: `/tmp/cvk-native-owner-restart-qualification.log`;
SHA-256 `86f7ccef51010bf38a0942574046fad7610fe0e16a26501e24c33e9c92713ff9`.

This closes R5's narrow **native owner recreation/restart prerequisite** for
an explicitly supplied PodGroup. It does not qualify automatic creation of
Workload/PodGroup objects, group relocation, rolling updates, app startup,
readiness, PDB drain or physical continuity. The synthetic Nodes run no apps.
CVK still rejects grouped drain. The next implementation must freeze/recheck
group, owner and membership identities; enforce PDB/domain capacity; and wait
for device-clean acknowledgement plus genuine replacement readiness before
physical upgrade/downgrade qualification.
