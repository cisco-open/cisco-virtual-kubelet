# Site retirement dependency prerequisite

The new shared `observeUnusedSiteLeaf` / `stableUnusedSiteLeaf` reader is a
read-only prerequisite for future owned site retirement. It reuses the existing
transport/session, complete site inventory, profile assignment and wireless tag
readers, fabric inventories, IPAM reservations, device assignments, planned APs
and PnP scope reader. It performs no native mutation and grants no public Delete
or prune authority.

The check refuses Global, invalid/missing/replaced targets, child sites, supported
consumers, dangling foreign site references, unknown wireless fields, incomplete
inventories and read failures. Two fresh observations bind the exact typed target
and full native target/ancestor footprint; a late consumer, identity replacement
or private native drift fails closed. Raw native attributes stay in memory and
String/GoString output is redacted. Unrelated valid memberships remain allowed.

The old unpaged legacy profile-summary read is factored into one shared reader.
The switching executor retains its existing namespace filter and duplicate-name
checks. Retirement observes memberships from every returned namespace, including
non-switching profiles, rather than assuming only the pinned switching/wireless
models can consume a site. A legacy wireless alias may share its wireless UUID;
a switching/wireless identity collision is refused.

## Local and native evidence

The [final focused race run](site-retirement-dependencies-focused.log) passes
44 dependency scenarios and the observation-redaction test in **3.009 s**.
The [broader profile regression run](site-retirement-dependencies-regression.log)
passes 19 families / 276 subcases in **113.370 s**. The final focused run adds
legacy wireless alias and redaction coverage beyond the earlier broader run.

The [generated empty-floor lab run](site-retirement-dependencies-native.log)
passes in **102.91 s** (Go **105.392 s**). It creates a new private hierarchy
through the established installed manager/worker, recovers its original geometry
task after a process stop, then performs the new two-observation dependency check
in the qualification process. Subsequent actual Pod replacement, Retain intent
retirement and supervised child-first cleanup pass; the complete typed site
baseline is restored. The connector is Ready with four retained geometry rows,
no active intent and no outstanding operation.

The [source/runtime audit](site-retirement-dependencies-validation.json) records
exact source hashes and this boundary: the new dependency reader executes in the
qualification process, **not in an installed public Delete flow**. The existing
worker image is used for hierarchy preparation; this result does not qualify a
new retirement worker image. Supervised cleanup is independently checked and
must not be counted as automatic source pruning.

## Work still required for public retirement

- Integrate the now [locally/API-qualified staged binding](site-retirement-binding.md)
  for omission, explicit policy, source/owner, private baseline and retained
  creator/latest state into the public compiler and dispatch flow.
- Complete explicit settings/credential ownership and operational-consumer
  protection; this dependency reader alone cannot authorize deletion.
- Use the shared journal for an immutable Delete selector, original acknowledged
  task recovery, complete task-tree success and independent absence verification.
- Publish retirement atomically with retained lineage, then support bounded
  recreation without reusing a predecessor's acknowledgement or custody.
- Qualify source and config deletion, interruption, uncertain responses, late
  consumers and repeated retirement/recreation through the installed worker.

The sites Apply gate still rejects public Delete/prune. No CRD, RBAC, foundation,
ND, direct-device or SWIM writer changes were introduced in this stage. No public
site lifecycle or full 93-resource parity is claimed.
