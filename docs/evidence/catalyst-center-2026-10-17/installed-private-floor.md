# Installed private floor geometry and accepted-task recovery

The [real installed floor lifecycle](installed-private-floor-native.log) passed
in **97.76 s** (Go command **100.217 s**) on the separate Kubernetes 1.35
qualification cluster. This extends the existing installed site harness;
production adapters, transport, CRDs and worker privileges were unchanged.

The normal manager/worker created an absent generated area, building and floor,
retaining **three private creator receipts**. The worker then changed the floor
width from **10 to 12 metres** and retained one private latest-applied receipt.
The observer independently checked all four original task trees and exact native
site attributes through the existing Catalyst Center client.

The test verified that the new floor's planned-AP inventory and assigned-device
inventory were empty before the width update, after verified completion and
immediately before supervised cleanup. This establishes an **empty-floor** case;
it does not qualify a populated floor, AP coordinate rescaling or physical AP
provisioning.

The exact installed worker process was interrupted after its Update task was
acknowledged. The original task and only the creator rows remained in Kubernetes
after the process stopped. Kubelet restarted the same Pod with a new container
and increased restart count; the real worker recovered the same task and
published the latest-applied receipt after task-tree/private/native verification.
A subsequent actual Pod replacement preserved all four receipt rows and reached
a freshly checked InSync state.

Retain configuration deletion removed intent and kept custody/history. Independent
supervised cleanup verified original creator UUIDs, absence of remaining children,
fresh device/PnP consumer checks, acknowledged cleanup tasks and native absence,
in **floor → building → area** order. The complete typed site inventory returned
to its baseline. No physical operation, device adoption or SWIM action occurred.
This cleanup does not attest public manifest-driven site Delete.

[Source/task audit](installed-private-floor-validation.json) separates the four
installed tasks from the three supervised cleanup tasks. The
[default-off compile gate](installed-private-floor-disabled-gate.log) and
[unknown-shape refusal](installed-private-floor-shape-gate.json) are separate
from native qualification. The existing exact cluster/controller UID, context,
node and observed container checks remain in effect. The prior connector was
normally retired only after its successful cleanup and saved custody evidence;
no finalizer or journal fence was bypassed.

Floor image assets and image-byte observation, populated/physical AP behavior,
private-key migration, custody retirement/handoff, public site prune/Delete and
other pinned-model gaps remain unfinished. The task/journal/native checks do not
independently count HTTP requests with a wire proxy.
