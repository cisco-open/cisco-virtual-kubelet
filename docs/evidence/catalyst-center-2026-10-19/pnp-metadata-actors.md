# Authenticated metadata handoff actors

The managed chart now renders metadata-handoff admission alongside the existing
topology protections. It applies only when old/new controller status contains
staged metadata custody, leaving legacy and ND objects without custody on their
existing path. The handoff now seals an immutable worker ServiceAccount username
as well as Pod name/UID; this adds no Secret access or native API permission.

Only the configured manager may seal a current preparation, acknowledge release
from retained refusal or original verified absence evidence, and retire released
custody. The exact worker ServiceAccount must also present single matching
Pod-UID and Pod-name authentication extras. Other accounts, stale Pods and
missing binding claims cannot advance the operation or publish refusal.

The worker must preserve the original operation/source/target identity and move
only forward. It cannot reset a submitted preparation, convert Unknown into an
acknowledgement/refusal, rewrite ownership, fabricate an effect while refusing,
publish a second absence row for the same operation, acknowledge manager release
or drop a verified slot while custody remains. Unreleased deletion is denied.
Original verified absence and certified-refusal publication remain distinct.

## Exact installed-policy observation

ReadMetadataHandoffProtection verifies the full compiled policy Spec, protocol,
Deny binding, current compiler observation, shared namespace and object
incarnations through uncached reads and final rechecks. It rejects weakened CEL,
Audit-only binding, future protocol, missing namespace, stale/uncompiled/warning
status and replacement during observation. The shared retained-admission reader
now also backs the existing bootstrap API through a compatible type alias;
the bootstrap policy and serialized evidence contract are unchanged.

The metadata policy digest is
ba8230ee487122beb493f92af6dce4e67a4bd07c31f7d93569186e1e639fdf66,
normalizing only the existing whole installation identity literals. Observed
protection is not a native permit, compatible worker-binary proof or actual
fence-settlement evidence.

## Qualification

The real isolated API run uses impersonated manager, current worker, stale Pod
and unrelated ServiceAccount identities, all with broad test RBAC. The named
admission policy supplies the denials. Both certified-refusal and original
verified-absence state paths pass, including manager release/retirement; Unknown
remains fenced. Producer transitions and identity extras are staged API fixtures;
no worker container, native effect or actual retained fence is involved.

Policy and binding Specs/enforcement are real. Envtest lacks a background policy
type-checking controller: runtime inspection first rejects its unobserved status,
then only that controller's status publication is simulated explicitly before
verifying the live policy/binding/namespace identities.

Final actor/protection/bootstrap API gates pass 55 test/package events. Existing
PnP provider/API persistence gates pass 129, seven shared API/ND/controller/upgrade
packages pass 3302 and selected regressions pass 1049. Main build and the complete
managed chart render contract pass. Chart expectations now count and explicitly
name all 31 policy pairs, including existing bootstrap and new metadata custody.
[Results and fingerprints](pnp-metadata-actors.json) record the gates.

Two broad checks initially exhausted scratch storage. Old agent-owned Go cache
entries and disposable prior build/render outputs were reclaimed; source,
backups, credentials and evidence were preserved. Affected checks passed on
rerun. Current runtime/CRD/policy source stayed frozen through final qualification.

## Remaining runtime integration

No installed-cluster rollout or Catalyst Center mutation occurred. Manager seal
writing, actual fence acquisition/worker claim and restart-safe release remain
unwired. The existing v1 bootstrap policy still prohibits fence modification and
deletion, so this actor policy alone cannot activate settlement or public
dispatch. A compatible retained-fence settlement contract and worker-version
activation must be qualified before enabling the full runtime.

Full consumer protection, missing-source/CR deletion context, finalization,
installed/native public lifecycle and other pinned 93-resource execution and
hardware gaps remain incomplete.
