# Default-resolved site/image/AP composition

The explicit site-default exporter now retains planned AP and image declarations.
It reuses the existing split model parsers and declared-geometry/unit checks.
An optional offline ConfigMap snapshot goes through the shared structural decoder
and runtime image asset loader; no model path opens a file or follows a URL.
The CLI still emits only a new private model file and non-secret provenance.
It does not create a CR or change the runtime transport, secret projection,
mutation journal, ownership contracts or other providers.

Snapshot and model validation refuse missing/colliding image bytes, duplicate
indexes/keys/documents, malformed or unused indexed assets, excessive counts,
unknown fields and invalid AP positions. Planned APs are locally syntax/extent
checked; native catalogue/model/antenna validation remains a runtime requirement.
Null image selection stays null in the exported model. Model-only image sources
still refuse without an explicit asset snapshot. Counts and digests are exported;
source/image values and private keys are not printed.

## Qualification boundary

Local public-reconciler fixtures cover ordinary and private-keyed AP/image
composition, source revisions, image updates, acknowledged task recovery and
restart no replay. The same three modes pass against a separate real temporary
Kubernetes API server. Neither claims installed actor qualification.

A native generated area/building/floor design passed the existing public flow:
three site Creates and a floor Update, two synchronous image uploads, AP
Create/Update, then another floor Update with AP correction. Eight original
acknowledged tasks recovered from Unknown; the images correctly minted no task.
Creator evidence remained unchanged and six bounded action rows remained with
no active operation. Cleanup verified the exact planned AP UUID first, then
removed the generated hierarchy. The complete typed site inventory matched its
initial baseline. No switches or existing sites were mutated.

The native wrapper retains generated source, journal and test comparison key in
a private checkpoint and verifies the baseline after all cleanup callbacks.
It borrows no installed credentials or comparison key; no private checkpoint
contents are copied into repository evidence. Completed checkpoints are
historical and must not be replayed. Native storage/actors are simulated; the
real API persistence gate is separate.

Broader regression and final build/documentation gates passed.
Full pinned-model parity remains false. Multi-file/managed-site resolution,
installed default composition, native catalogue/schema parity, remote image
byte readback and destructive design lifecycle remain open.

The [native log](site-defaults-design-native.log),
[temporary API log](site-defaults-design-api.log),
[focused race log](site-defaults-design-race.log) and
[machine-readable evidence](site-defaults-design.json) record the qualified
boundaries. Source fingerprints distinguish the current dirty branch build
from historical component gates. The example snapshot contains a generated
640×480 grid and synthetic model only; no lab metadata or keys were copied.

## Final gates

- Broad regression: 19,447 passing test events across 16 packages, no failures.
- Focused race checks: 66 passing test events, including CLI and runtime image-loader compatibility.
- Real temporary API public design/recovery flow: 4 passing events across the ordinary, image and keyed-image cases.
- Native design flow: eight original-task recoveries, two synchronous image uploads, unchanged site creators, bounded receipts and exact cleanup with typed baseline restoration.
- Production/migration builds, compiled example/private-file checks, strict documentation, formatting, unchanged source fingerprints and all 93 coverage rows passed.

No full-model completion, installed composition, whole native catalogue/schema
parity, remote image byte readback or destructive AP/image authority is claimed.
The completed private checkpoint contains generated-only recovery material and
must not be used to replay this already-cleaned workflow.
