# Installed private site lifecycle

Qualification uses the dedicated `cvk-catc-qualification-20261017` kind cluster,
the existing Helm release and a source-pinned Linux/arm64 worker image. It never
selects the user's default kubeconfig or the demo cluster. Native observation
uses a read-only transport; creation and deletion execute through the installed
worker from ordinary NetworkControllerConfig and ConfigMap updates. The normal
worker Secret projection supplies API credentials and the private comparison
key. The qualifier does not read a Kubernetes Secret or copy that key to the host.

## Production build correction

The first production build failed because recreation referenced
`siteCreatorForPath`, which existed only in a test file. Test binaries included
that helper and had hidden the dependency. Recreation now uses the existing
production `siteCreatorForResource` lookup, which accepts a validated site path
and shares the same path-digest matching used by ownership checks. No duplicate
production helper, provider abstraction or transport was introduced.

The corrected Linux/arm64 production binary builds. Production source hashes,
binary hash and local image identity are recorded separately from test-harness
hashes. Historical component qualification hashes are not rewritten by this fix.

## Isolated ownership preparation

A second identity for the existing endpoint was correctly fenced by the manager's
DuplicateEndpoint protection. The old idle qualification controller's four
receipts were preserved in private metadata records. No referring configs or
active operation remained, and generated native site identities were absent.
The exact old controller UID was retired through ordinary finalization, without
forcing finalizers or deleting Secrets. The fresh `catc-lifecycle` identity then
became Ready. This does not implement cross-controller ownership adoption.

The first installed fixture was rejected by API admission because its drift
interval was 10 seconds, below the 30-second minimum. No configuration or
operation was admitted. The corrected fixture uses 30 seconds; the original
refusal log and test-source boundary are retained separately.

## Qualification scope

The [validation record](installed-site-lifecycle-validation.json) pins the image,
binary and source hashes, original task IDs and completed gates. The installed
native test passed in **357.629 seconds**. It retained twelve distinct verified
tasks (six Creates and six Deletes), three archived predecessor cohorts, twelve
receipt rows and one section owner. The final journal had no active operation,
and typed site inventory matched its initial baseline.

The interrupted floor retirement retained task
`01a14bb7-00c5-70d7-83ef-af10bd8cece4` across a real worker process restart in the
same Pod. The restarted worker published that same task's verified retirement.
Subsequent building and floor recreation authenticated replacement parent
receipts. A final Pod replacement produced a refreshed InSync check while
preserving the complete journal. The configuration remained at generation 2;
the original ConfigMap UID remained unchanged throughout source-only transitions.
Unlike the earlier component qualifiers, Kubernetes persistence and worker
execution were real for this run. Verified action counts are journal/task counts;
the qualifier does not instrument the worker's raw HTTP request stream.

Race-enabled regression gates passed: 11 recreation/manifest families with 109
subcases (25.051 seconds), five real API-server families with 28 named subcases
plus complete hierarchy cycles (58.038 seconds), Nexus Dashboard (4.749 seconds),
selected GNOI/SWIM controller tests (6.438 seconds) and four endpoint-ownership
families (2.882 seconds). These regression gates do not claim new physical-device
or SWIM hardware qualification.

The installed qualifier uses a generated empty area, building and floor. It
retains one configuration UID and generation through ConfigMap-only source
changes. Its sequence is creation, leaf-first pruning, parent-first recreation
with replacement UUIDs and parent receipt handoff, and final leaf-first cleanup.
It interrupts the exact isolated worker process after an acknowledged floor
Delete, then verifies completion through the same retained task. It also replaces
the terminal worker Pod and checks refreshed InSync without journal changes.

This scope does not establish cleanup of retained floor images, planned APs or
policy effects, populated operational references, configuration deletion, physical
device upgrades or complete pinned NetAsCode parity. Those remain pending.
