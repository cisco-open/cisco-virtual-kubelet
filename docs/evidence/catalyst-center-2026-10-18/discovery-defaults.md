# Discovery non-secret defaults qualification

Explicit offline discovery default resolution now composes the pinned Terraform
fallback fields with existing site selection and the ordinary execution parser.
The pinned 0.5.0 bundle has zero discovery defaults; required values are never
invented from provider behavior. Ten existing non-secret execution fields accept
scoped override defaults. Explicit job values win, nulls remain unresolved, and
all defaults—including shadowed/unused fields—and expanded jobs validate before
selection. Identity, credential-list, create-per-site and inline credential
fallbacks are refused.

Model/default documents share the extracted strict single-document decoder;
existing model parsing retains its original root/shape checks. Alias/anchor,
duplicate-key, multiple-document, non-string-key, depth and size restrictions
remain. Expansion is budgeted before serialization under the existing job/output
limits. A large repeated-filter fixture is refused without first materializing a
large expanded JSON result. The existing CLI private/no-overwrite output helper
and runtime discovery writer/ownership/transport are reused.

| Gate | Result / scope |
| --- | --- |
| Race regression | 48 families / 137 subcases; 17.238s; discovery, selection and adjacent default paths |
| Shared parser / CLI | 1.657s / 2.159s; all existing package tests plus new provenance/privacy/refusal cases |
| Real private Kubernetes API | 8.895s; public default-expanded owned lifecycle; native responses and uncertainty faults simulated |
| Native lab | 70.302s; generated .101 job create/delete/recreate/no-op/cleanup; Kubernetes persistence simulated |
| ND | 4.518s |
| Software-upgrade provider | 14.569s |
| Controller GNOI/SWIM | 6.960s |
| Production build | Passed; not installed |
| Checked-in example | Actual CLI exported privately with two applied defaults and one selected job |

Native qualification used the same sparse input for both revisions. The default
override digest changed from SSH to SSH,TELNET, producing a distinct resolved
output and owned replacement through the existing journal. Two Creates and two
Deletes were verified; original task identities were retained without replay.
The harness fault-injected three Accepted-to-Unknown restorations in simulated
Kubernetes status, then used normal observation/receipt publication. This is not
a natural timeout or an installed process-restart test. JSON reloading/reconciler
reconstruction qualified serialized simulated persistence.

Cleanup removed only the generated owned job UUIDs, preserving the existing
Switches job and original discovery definitions. Target UUID/serial and inventory
count checks also passed. Native service reads and
mutations were real; default-expanded type/protocol resolution and protocol
replacement were exercised. Other supported fields have controlled parser/race
coverage, not a broad native settings attestation. Checkpoint source JSON is
minified by the existing harness; pretty-normalized bytes match the exporter's
output digest. Actual CLI output bytes retain the reported format/digest. CLI and
native records identify the same final policy build.

An earlier native pass predates the expansion-budget guard and remains in private
historical checkpoints. Its cleanup was verified before the final-source cycle;
only the final cycle is claimed by the source-bound record here. The pinned
Terraform source and existing defaults bundle have explicit independent digests.

No runtime default merging, native credential creation, source adoption or
transport duplication was added. Full nested schema, whole-model/file merging,
inline credential projection, null clearing, installed qualification and complete
pinned-model parity remain incomplete. See the
[operator contract](../../controllers/catalyst-center-discovery-defaults.md),
[source/task/example record](discovery-defaults-validation.json),
[regression](discovery-defaults-regression.log),
[API](discovery-defaults-api.log), [native](discovery-defaults-native.log),
[parser/CLI](discovery-defaults-cli-parser.log), [ND](discovery-defaults-nd.log),
[provider upgrades](discovery-defaults-provider-upgrades.log),
[controller upgrades](discovery-defaults-controller-upgrades.log) and
[build](discovery-defaults-build.log).
