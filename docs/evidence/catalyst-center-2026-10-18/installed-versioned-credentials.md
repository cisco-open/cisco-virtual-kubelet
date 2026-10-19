# Installed versioned credential recovery

All six versioned global credential types now have installed qualification of
normal Secret projection, Create, rotation Update and acknowledged Unknown task
recovery. The isolated manager and worker used the unchanged production image
from the [empty-selection milestone](empty-credentials.md). The new code is a live
qualification harness; no product API, transport, writer or Secret resolver was
added. All mutations used generated, unassigned definitions.

## Executed flow

- Created one generated Secret with eight atomic version/value envelopes. Added
  controller-authorized aliases and config references, then waited for the sole
  ready worker Pod to mount the selected per-key projections. The harness never
  fetched a Secret or read a projected file; the normal manager/kubelet/worker
  credential trust boundary remained in use.
- Enabled the existing global network-settings owner and source ConfigMap. Each
  of six Creates reached the production timeout-induced Unknown boundary. The
  first retained Create survived an exact-UID worker Pod deletion/replacement.
  The installed worker recovered the original task and persisted its versioned
  application receipt.
- Updated the same generated Secret from memory with fresh values/version 2,
  then updated model counters in the same ConfigMap UID. All six Updates reached
  Unknown, recovered their original tasks and retained the same native UUIDs.
  Eight version bindings advanced from 1 to 2.
- Switched back to empty report intent and removed config Secret references.
  Separately supervised cleanup deleted only the six verified generated UUIDs;
  each cleanup task tree completed and the full non-secret native credential
  metadata baseline was restored.
- Restored the controller's original alias allow-list, waited for the replacement
  worker without the generated Secret projection, and deleted that exact Secret
  with a UID precondition. Historical versioned receipts remain retained.

The native run passed in **364.782s**. Twelve distinct original operation/task
pairs are captured, and both stages' receipts independently confirm stable UUIDs
and explicit counters. Status/checkpoint serialization rejects generated secret
values. Installed HTTP request counts are not instrumented; task/operation
continuity is the evidence here, with separate controlled/native component
no-replay checks retained in preceding milestones. The `1ns` timeout is deliberate
fault injection, not a recommended production setting.

## Terminal proof and boundaries

Both configurations are InSync at their current generations, the controller is
Ready, the journal is idle and both Pods are ready. All **13** pre-existing action
receipts (12 site receipts and one metadata credential receipt) are unchanged.
Six historical versioned credential receipts remain with empty report intent.
The generated source Secret was removed and original aliases restored.

No credential was assigned to a site/device and physical authentication was not
tested. Native credential metadata is not secret byte readback. Supervised cleanup
does not qualify declarative credential pruning, deletion of a config/controller,
receipt-family migration or ownership handoff. Full pinned-model parity remains
open.

See the [source/image/task/result record](installed-versioned-credentials-validation.json),
[native log](installed-versioned-credentials-native.log) and
[disabled compile gate](installed-versioned-credentials-optout.log). Production
source hashes still match the deployed image's complete frozen boundary; the
unchanged code's race, API, ND and GNOI/SWIM gates are recorded in the
[preceding validation](empty-credentials-validation.json). Those historical gates
are not relabelled as reruns of this new harness.
