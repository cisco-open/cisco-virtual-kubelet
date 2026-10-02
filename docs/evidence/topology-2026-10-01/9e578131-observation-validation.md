# Clean candidate observation validation: `9e578131`

Date: 1 October 2026  
Environment: isolated Ubuntu16 lab control plane (`v1.35.8+k3s1`)  
Branch: `pr/johalley/tas-extentions`  
Candidate image: `cvk-tas-extentions:9e578131`  
Local image digest: `sha256:ec44b452e6080955687259afd3fa461fc610e287ba4a2f5f3610bb35075c43fa`

This is a read-only topology/observation qualification. It does not claim a
software upgrade or downgrade, and no device mutation was issued.

## Deployment

1. The candidate was built from committed source `9e578131` for `linux/amd64`.
2. The image was imported into Ubuntu16's local k3s/containerd runtime.
3. The packaged chart was upgraded with Helm using the existing release values;
   the explicit controller and per-device worker image overrides were set to
   `cvk-tas-extentions:9e578131`.
4. Helm revision **112** completed with status `deployed`.
5. The controller Deployment became Ready with one replica and no recent
   error/warning records. Its command and both worker image arguments resolve
   to `cvk-tas-extentions:9e578131`.
6. The native admission policies were updated with the chart and passed the
   controller's compiled-contract preflight. Policy type-check warnings were
   empty.

The first deliberately direct image-only rollout was rejected by the new
controller's fail-closed admission preflight because the cluster policies
were older than the candidate's compiled contract. The old ReplicaSet stayed
available. The release was rolled back, then upgraded through Helm so policy,
controller and worker images converged together. This is retained as a
deployment safety finding, not as a candidate defect.

## Physical-target status

The three intended C9K targets were observed through the Kubernetes API:

| Target | Device phase | Network worker | Observation | Complete | gNOI/topology conditions |
| --- | --- | --- | ---: | --- | --- |
| `198.51.100.100` (`cat9k-live`) | Ready | manager-bound UID matched | advancing | true | GNOIConfigurationReady, NodeIdentityReady, TopologyReady and MaintenanceReady true |
| `198.51.100.101` (`cat9k-lab-101`) | Ready | manager-bound UID matched | advancing | true | GNOIConfigurationReady, NodeIdentityReady, TopologyReady and MaintenanceReady true |
| `198.51.100.103` (`cat9k-lab-103`) | Ready | manager-bound UID matched | 3 after replacement | true | GNOIConfigurationReady, NodeIdentityReady, TopologyReady and MaintenanceReady true |

No current error/warning records were present in the three network-worker log
streams after binding convergence. Transient `binding is not at the desired
revision` messages occurred during the Helm worker rollout and stopped once
the manager recorded the live revision/Pod binding.

## Restart-safe sequence test

The `.103` network Deployment was changed through the manager ServiceAccount's
authorized template-update path (`kubectl set env`, not a direct Pod delete).
The native admission policy correctly denied the initial direct Pod-delete
attempt. The template rollout then created a new worker UID:

```text
before: sequence=18, worker Pod UID=d1a2c451-9508-4b72-b593-da684efdb154
after:  sequence=1,  worker Pod UID=0cc189d0-37bf-45fe-8dba-02a0239065e1
later:  sequence=3,  worker Pod UID=0cc189d0-37bf-45fe-8dba-02a0239065e1
bound Pod UID at each read matched observation.workerPodUID
```

The reset to one is correct for a replacement manager-bound worker
incarnation; the new sequence advanced without replay or a stale-Pod write.
The same-Pod process-restart recovery path is covered by the local regression
test `TestPublishNetworkObservationRestartSequenceRecovery`, which resumes
from a persisted sequence of 10,000 at 10,001.

## Test result and boundary

Passing local gates for this candidate:

```text
GOCACHE=/tmp/cvk-gocache go test -race -count=1 ./internal/provider 
KUBEBUILDER_ASSETS=.../k8s/1.35.0-darwin-arm64 \
  GOCACHE=/tmp/cvk-gocache go test -tags envtest -count=1 \
  ./internal/provider -run '^TestEnvtest_NetworkObservationStatusRoundTrip$'
go test -count=1 ./...
mkdocs build --strict
git diff --check
```

This evidence qualifies clean deployment, native-policy preflight, bounded
observation publication and replacement-worker sequence behavior. It does not
close N2 bound-token admission, N3 timeout/source coverage, E03 claim-time
network enforcement, or the E04 physical preparation/activation boundary.
Those gates must be completed before a disruptive software upgrade or
downgrade is claimed for this candidate.

Direct SSH CLI checks from the workstation could not reach the RFC 5737 lab
addresses; Ubuntu16 has no `sshpass` binary and no configured device SSH key.
The physical validation therefore used the CVK workers' real management
sessions and Kubernetes status/log evidence, not a simulated device.
