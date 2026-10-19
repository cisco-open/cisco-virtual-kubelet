# Native Multi-PSK SSID manifest qualification

## Qualified model and execution

The existing SSID native harness is reused for a main passphrase plus two ASCII
PSKs at priorities 4 and 0. The new repository example is
`examples/configs/catalyst-center/wireless-ssid-multipsk-credentials-apply.yaml`.
The harness checks that its first model and SecretRefs match the example, changing
only the isolated name/controller reference and later version/order transitions.
All three sources use the common controller-authorized projection paths and
atomic version/value JSON. No credential value is stored in the model or journal.

A random absent Global WPA2_PERSONAL SSID remains disabled and unassociated.
Create completes through the public reconciler, native task and JSON-reloaded
journal. Reordering priorities 4/0 to 0/4, with corresponding SecretRefs/projection
paths, converges without any HTTP mutation. Semantic credential keys remain bound
to priority rather than list position.

Only priority 4 then changes from version 1 to 2; the main passphrase and priority
0 remain at version 1. The old projected version is refused without native
submission. Publishing the selected new projection produces exactly one PUT on
the original native ID. The outbound guard verifies each PSK against its expected
priority without logging values. Repeated convergence does not replay a mutation.
The final receipt records:

| Semantic key | Version | Current source path | Source alias |
| --- | --- | --- | --- |
| `passphrase` | 1 | `/ssids/0/passphrase` | `ssid-passphrase` |
| `multi-psk:0` | 1 | `/ssids/0/multi_psk_settings/0/passphrase_wo` | `multi-0` |
| `multi-psk:4` | 2 | `/ssids/0/multi_psk_settings/1/passphrase_wo` | `multi-4` |

These persisted versions/paths/aliases were independently checked from the final
private journal after native execution. It contains one credential application
and no active operation. Source pruning/reordering cannot change the semantic
version identity merely by moving a row.

## Native evidence

The repository-example run passed in **18.81 seconds** (package 20.935 seconds).
Fixture `CVK_PSK_ab8408aea06f02d6` used native ID
`mdi0q1zl-x1bt-s19H-yJG0-mdHHzweWnMyWmMq2qZbwmuSYxZnq`.
Exactly one POST, PUT and DELETE were recorded. Cleanup task
`01a14143-f6cf-762c-8601-f55b88d953e8` completed and repeated readback proved
absence, with the original profile/SSID baseline restored. No profile references,
site assignment, AP/WLC provisioning or physical device action was submitted.
Private metadata/source/baseline/journal/cleanup evidence is under
`/private/tmp/cvk-ssid-manifest-2824398927/`, directory mode 700 and files 600.

The initial native Multi-PSK run passed in 19.24 seconds. Its fixture
`CVK_PSK_f08270bc7885e424` / native ID
`mdi0q1zl-x1bt-s19M-mdGY-nZbIyZC4odvLndi0qZbwmuSYxZnq` was removed using task
`01a14142-21b0-7353-a6ee-6a5248ca568a`. Private evidence is at
`/private/tmp/cvk-ssid-manifest-3490389156/`. Its final version/path/alias mapping
and exact mutation counts were checked independently as well. Both generated
fixtures are gone; no native resource is retained from this stage.

All generated secret values, including the rotated value, were scanned out of
persisted journal/public metadata throughout execution. Projection files were
temporary and were not copied into recovery evidence. Exact retained cleanup now
validates the shared semantic rotation lineage for either main-only or Multi-PSK
fixtures; it never recreates a definition or reconstructs discarded passwords.

SSID model/projection/version/ownership/restart regression passed in 3.083
seconds with race detection. Production controller/provider/transport logic was
unchanged; the added native scenario and example reuse existing implementation.

## Remaining qualification

The appliance API/tasks, projection files and public reconciler are real;
Kubernetes persistence and adapter restarts are simulated. Real projected-volume
rotation, installed worker/CRD/RBAC behavior and actual client authentication are
not qualified. Task/public metadata/version receipts do not attest write-only
password values on a WLC or AP.

This bounds qualification to main plus two ASCII keys under WPA2_PERSONAL.
Additional key counts/priorities, HEX keys, credential-set removal, mixed auth/
cipher combinations, adoption, pruning and device association remain pending.
Full pinned NetAsCode execution parity is incomplete.

The later [retirement implementation](ssid-credential-retirement.md) closes the
previously pending bounded Multi-PSK remove/return path with retained main
passphrase; full credential/auth-mode transitions remain pending.
