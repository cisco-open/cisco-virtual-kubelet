# Live public bulk creation followed by manifest update

On 2026-10-14, an isolated hierarchy
`Global/CVK-Q-20261014-145802-000000000` was created through the public
`NetworkControllerConfig` reconciler using bulk transport. The same controller
journal, config UID and sites owner were then retained for a new source
generation requesting a floor-width update. This exercised the ordinary
UUID-bound update route with bulk transport still enabled.

Both Create and Update reached InSync across simulated worker restarts. Each
phase cleared only its completed operation, retained the same owner and passed
an additional no-op reconciliation without a new operation. The test verified
the original bulk member/ancestor descriptor and task tree, then independently
verified the updated floor UUID, parent UUID and selected geometry through
native readback. Only verified test identities were removed; cleanup passed.

Live test 17.32s, package 18.031s. See the retained
[live output](site-bulk-public-update-live.log). Live-tag compilation passed
before execution. The changes in this increment are confined to the live
qualification harness; production execution logic is unchanged.

This uses the real Catalyst Center API with **simulated Kubernetes persistence
and worker restarts**. It does not qualify installed manager/worker actors or
real API-server admission. Wider building/floor update combinations, larger
batches, country/address/default semantics and ownership-aware pruning remain
open. The full pinned resource/section capability flags remain false.
