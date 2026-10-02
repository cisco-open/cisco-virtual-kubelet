# E10 isolated-link drift and recovery

This record qualifies the controlled physical-link portion of E10 on the
three-switch C9300 lab. It is intentionally sanitized: management addresses,
credentials, Secret references and session tokens remain only in the local
operator archive.

## Exact candidate

The recovery candidate was source commit `740ffd0e`. It includes
`925b8ce2`, which makes an explicit IOS XE `shutdown: false` transition delete
the YANG presence leaf, and `740ffd0e`, which prevents a replacement network
worker from touching a managed configuration object until the manager has
stamped its exact Pod name and UID binding.

The Linux/amd64 image was deployed as `cvk-tas-extentions:740ffd0e` by Helm
revision 143 to the manager and all six app/network workers. Its Docker
manifest-list digest was
`sha256:30713da0f9d5708251326c074725d9ef6cc5040e2ac484a5e9c955ecf864c5a4`;
the platform manifest was
`sha256:500b974f8e8457016e3148cc9338a8ed835ee826eaeb790bf8a2ee3da3f585bf`
and the image config was
`sha256:ac6e856f7624daeb6155075ae110ec4ded73b4693638e29768c51a1d54a857dc`.

## Execution and observations

1. The manager-accepted baseline was complete: five graph nodes, nine
   directional edges and zero diagnostics.
2. A Kubernetes-native `IOSXEConfig` set the isolated test interface on
   `cat9k-lab-101` to shutdown. The management interface remained available.
   Fresh accepted evidence changed the read-only graph to five nodes, seven
   edges and three diagnostics: both directions of the declared physical link
   were missing and the corresponding peer mapping was unused. With
   `--require-complete`, the CLI returned a non-zero result.
3. The first restore attempt exposed a real IOS XE writer defect: a false
   value had been omitted from a merge payload, but IOS XE models `shutdown`
   as an empty/presence leaf and therefore requires an exact-path delete.
   The failed mutation retained its disruptive-mutation Lease for the full
   1,860-second TTL. The lease was not manually deleted or cleared.
4. After the fixed candidate was deployed, all replacement workers reached
   Ready with exact `740ffd0e` images. The live replacement produced zero
   missing-Pod-UID/shared-network-object admission errors. At natural lease
   expiry, the same generation-2 `IOSXEConfig` converged to `InSync` and the
   lease holder cleared.
5. A fresh read-only `DeviceOperation`
   (`roadmap-e10-link-restored-101`, UID
   `aa6bb805-a274-41bc-855a-e3c6319edf03`) reported:

   ```text
   GigabitEthernet1/0/1 is up, line protocol is up (connected)
   Gi1/0/1                         connected    1          a-full a-1000
   C9K-1           Gig 1/0/1         ...              S I   C9300-24P Gig 1/0/1
   ```

6. The next manager-accepted graph was complete again:

   ```text
   complete=true nodes=5 edges=9 diagnostics=0
   evidence=sha256:90d659d68a01259fdc6c1bbaf2cb56ce675a910449fb09a8a74f0d8a646dec30
   provenance=sha256:58c57e66078ecfde3a8c1a48acec4c343e428ec83d4b633b87af0effe9090cf4
   ```

The protected graph-policy ConfigMap remained UID
`525ff87f-b74a-44c4-b54e-408e63a3a2e7`, resourceVersion `9731414`, with
canonical content hash
`sha256:90aaacdb5b2a39f93741aeefe17d5c0f1c28b15bf922dd6c60706a0fb9e63996`
through the change and recovery. Discovery did not rewrite labels, rollout
policy, an approval hash or a software plan. The deliberate `IOSXEConfig` was
the only authority that changed the interface.

## Qualification result

E10-B passes for the isolated physical-link change and restoration. The graph
failed closed on fresh missing adjacency evidence, retained administrator
policy provenance, and cleared only after fresh physical recovery. The event
also qualified the safety behavior of a failed device write: the existing
fence remained authoritative until normal expiry and reconciliation.

Focused E10-A fixtures additionally prove that CDP and OSPF adjacencies on the
same port-channel stay distinct, protocol or VRF mismatches remain unproven
directional links, and explicitly truncated input at exact resource bounds is
incomplete rather than healthy. The graph remains diagnostic/read-only; this
result does not make it independent rollout authority or claim end-to-end
service-path health.
