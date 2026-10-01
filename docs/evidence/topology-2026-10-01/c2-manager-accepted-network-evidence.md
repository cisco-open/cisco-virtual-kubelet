# C2 manager-accepted network evidence qualification

Date: 2026-10-01 UTC

Source commit: `90bc690c` (`feat(topology): authenticate accepted network evidence`)

Branch: `pr/johalley/tas-extentions`

## Outcome

The C2 trust-boundary increment passed local, native-API and read-only physical
qualification. The network worker publishes a bounded, untrusted sample at
`status.healthObservation.network`. The manager validates the current
CiscoDevice incarnation, physical-identity hash, network-worker revision, Pod
UID, positive sequence, collection interval and duplicate-free identities
before copying the sample to the separately protected
`status.healthObservation.acceptedNetwork`. Software-rollout admission now
uses only the accepted copy. The original collection start remains the
freshness lower bound; publication, reconciliation and manager restart cannot
make old device evidence look new.

This is not closure of all C2/E02 work. Idle physical interface and adjacency
state was compared with IOS XE CLI, but no independent traffic generator,
declared loaded-path scenario or predeclared rate tolerance was available.
Directional rate accuracy under load and redundant-supervisor qualification
therefore remain open.

## Candidate identity and deployment

- container tag: `cvk-tas-extentions:90bc690c`
- OCI index digest: `sha256:8c2b8627bcbc6842588341ebe72cc15ef647d130d95ea971efc825e9689fceeb`
- transferred archive SHA-256:
  `66156074d084ef77e8a7c83879b7e1d57815f790e62e08c37bcb3cd84711c7ad`
- Ubuntu16 Helm release: `cisco-vk`, revision 120
- migration order: additive CiscoDevice CRD, then chart admission policy,
  manager and workers
- manager image and all six physical-C9K app/network worker images converged
  to `90bc690c`; the out-of-scope NX-OS compatibility worker was unchanged
- no software rollout or device mutation was started; all three maintenance
  sessions were `Settled` and disruptive Lease holders were empty before and
  after the rotation

The Recreate transition produced expected short-lived denial logs from old
app-worker tokens after the manager changed exact Pod bindings. Once the new
bindings converged, a later 60-second steady-state window contained no manager
errors or worker admission denials.

## Local and native API acceptance

The following passed against the committed source:

- repository-wide `go test -race -count=1 ./...`;
- Kubernetes 1.35 envtest CRD/schema matrix;
- Helm lint and topology render/embedded-contract checks;
- two pinned generator passes with identical output digest;
- disposable Kubernetes 1.35 shared-worker admission suite using real
  API-server-issued Pod-bound tokens; and
- focused manager acceptance and oldest-source freshness tests.

The native admission suite proved that a genuine, otherwise authorized bound
network-worker token can update the raw sample but cannot create or alter
`acceptedNetwork`. It also retained the earlier wrong-device, replaced-Pod,
missing-manager-proof, forged-revision, replayed-sequence and invalid-interval
denials.

## Physical accepted observations

The following final snapshot was taken after the manager restart test. In
every row, raw and accepted sequences were equal, and accepted Pod UID and
producer revision exactly matched manager-owned `networkWorkerRevision`.

| CiscoDevice | Network worker Pod UID | Revision | raw / accepted sequence | interfaces | adjacencies |
| --- | --- | --- | ---: | ---: | ---: |
| `cat9k-live` | `ad656269-2a1f-478e-a5cd-b379443dba21` | `sha256:2b74c0916391289a058eb3350b56bda4540c28fabe66ab87f4f7c7990c670e1c` | 10 / 10 | 46 | 5 |
| `cat9k-lab-101` | `4afb5e54-ab80-4e72-8205-8ef8b1ef9d30` | `sha256:fd86b30fbe6cd60bf460dd7c3eafc52fe74f82cf3ed5b9783edcf8973f362278` | 11 / 11 | 46 | 2 |
| `cat9k-lab-103` | `40330f2f-b3d1-45f8-9a2d-c415e9fce1e3` | `sha256:cb6e37e5980a842aa8dc729858461cd8be8edfcc9c5807558844370b84f47a1e` | 10 / 10 | 44 | 2 |

All three accepted observations were complete. All three Nodes were Ready and
untainted; their IOS XE versions remained 17.18.2, 17.18.2 and 17.18.3
respectively.

## Device-side comparison

Three fresh read-only `DeviceOperation` objects executed `show clock`,
`show interfaces status`, `show cdp neighbors`, `show ip ospf neighbor` and
`show platform software status control-processor brief` through the exact
manager-bound network workers:

| Operation | Device | UID | Result |
| --- | --- | --- | --- |
| `c2-observation-audit-100` | `cat9k-live` | `a76c44d6-c0e4-4f91-967a-83c0f0c1b494` | Succeeded |
| `c2-observation-audit-101` | `cat9k-lab-101` | `c0171190-68bc-4a09-8fc4-1bf758c728af` | Succeeded |
| `c2-observation-audit-103` | `cat9k-lab-103` | `a8b38dbf-42d1-47db-88b9-55e9e59eb4b3` | Succeeded |

IOS XE interface state and CDP adjacency count/identity matched each accepted
observation. Examples include `cat9k-live` connected Gi1/0/1, Gi1/0/2,
Gi1/0/22 and Gi1/0/23 with five CDP adjacencies; `cat9k-lab-101` connected
Gi1/0/1, Te1/1/1 and Te1/1/2 with two CDP adjacencies; and
`cat9k-lab-103` connected Gi1/0/23 with two CDP adjacencies. OSPF output was
empty and the accepted observation contained no OSPF neighbors. Each device
reported `1-RP0 Healthy`. Supervisor health is not yet an accepted CVK field,
so that CLI result is qualification evidence only and is not silently treated
as an automated health grant.

## Restart and remaining gates

The manager Deployment was restarted without rotating device workers. After
restart, accepted sequences advanced while retaining the same exact
Pod/revision bindings. No device mutation, acceptance replay or freshness reset
occurred.

Remaining C2 work is explicit:

1. declare real service paths, a controlled idle/loaded traffic source and
   ingress/egress tolerance before testing directional rate accuracy;
2. qualify supported redundant-supervisor hardware, otherwise keep the signal
   Unknown;
3. finish the old/new manager-worker-chart rollback matrix and concurrent/lost
   response API cases; and
4. archive a measured-load run before C3 may depend on headroom for disruption
   authorization.
