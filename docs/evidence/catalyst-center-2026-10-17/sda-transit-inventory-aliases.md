# SDA control-plane inventory aliases

The staged SDA path now accepts validated wired `inventory.devices` dependency
context alongside `fabric.transits`. This closes the controller-hostname-only
limitation for model aliases: the declared CP name can resolve through its
`fqdn_name` to the same native inventory identity. It does not activate inventory
provisioning or new public SDA manifest execution.

## Pinned model and common structure

The pinned module's
[name-to-FQDN map and provisioned-CP selection](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_device_provision.tf)
and [transit mapping](https://github.com/netascode/terraform-catalystcenter-nac-catalystcenter/blob/a435555fe828f03cf3311d52fe1ab6572a247916/cc_fabric.tf)
use model names, FQDN lookup and explicit PROVISION state. CVK reuses
`parseWiredInventoryIntent` and the existing inventory/physical identity resolver;
there is no second inventory parser, registry, transport, journal or schema.
An indexed name lookup avoids scanning all inventory rows for each CP.

All dependency rows are validated before use. Each selected CP must have an
explicit named PROVISION declaration when context is provided; ASSIGN,
REPROVISION, missing names, malformed later rows and extra inventory collections
refuse. Existing bare transit models retain their direct-name/native prerequisite
behavior. This is read-only dependency context: a PROVISION declaration is not a
request executed by the SDA stage, and an absent native provisioned record still
fences submission.

Optional serial/IP fields constrain physical identity; they cannot serve as an
unrelated physical-only fallback when neither model name nor FQDN matches native
hostname. The existing resolver refuses conflicting/ambiguous name/FQDN/physical
selectors. The declared site must match the native provisioned site and its
assignment; optional role/source must match native state. Duplicate physical CPs
and the existing peer/managed-identity fences remain enforced.

Reference maps, CP lists and optional flags are frozen at planning and owned
stage boundaries. Complete reference values are checked against the declared
source, and ConfigMap UID/resourceVersion or config revision still bind all
context. A caller's mutable map cannot retarget preparation. The model alias
never becomes a Kubernetes device/Node name; managed bindings remain based on
native ID, physical identity and original Kubernetes UIDs. Native payloads carry
only resolved CP IDs. No alias data acquires inventory ownership.

## Verification

- [Race regression](sda-transit-inventory-aliases-regression.log) passes in 27.032
  seconds: 123 SDA subcases (prior 98 plus twenty alias/native cases and five
  source/journal/permit/recovery cases), standalone SDA checks and adjacent wired
  inventory/transit/fabric/group regressions. Coverage includes FQDN and direct
  names, optional/absent anchors, role constraints, site/IP/serial conflicts,
  ambiguous names, physical-only and duplicate-physical refusal, invalid states,
  complete model validation, frozen caller maps and late hostname replacement.
  Alias Accepted/Unknown recovery reuses actual shared all-member permits and
  retains the existing Verifying/worker-read/convergence boundaries. Product
  responses, grants and actors in these cycles are fixtures.
- [Three real private API cases](sda-transit-inventory-aliases-api.log) pass in
  21.648 seconds: existing source/group persistence, alias-context persistence
  and ConfigMap-only FQDN edits, and deliberate group-field dropping/refusal.
  Alias drift leaves the original Prepared journal unchanged with zero product
  requests. No installed admission or device operation is claimed.
- [Live read-only aliases](sda-transit-inventory-aliases-native.log) resolve three
  synthetic model names/FQDN mappings to the exact existing IDs for `.100`,
  `.101` and `.103`. Native provisioned records are absent and preflight refuses.
  Zero product writes are counted; no CLI, worker or SDA creation is executed.
- [Foundations](sda-transit-inventory-aliases-foundations.log): shared controller/
  group, ND, device-read/diagnostic, lifecycle, upgrade and GNOI checks pass;
  unchanged packages may use cached results.
- [Final source/upstream fingerprints](sda-transit-inventory-aliases-sources.json)
  and [strict documentation build](sda-transit-inventory-aliases-docs.log).

## Remaining work

This dependency projection supports the existing wired-inventory parser's subset;
full mixed inventory/fabric/wireless model composition remains pending. It neither
provisions a missing CP nor claims inventory fields outside the selected fabric
scope. New-intent orchestration/provisioning sequencing and automatic Prepared
submission remain gated. Independent semantic transit convergence, final durable
receipts and reservation settlement/release are still required. Updates and
connected-consumer footprints, native creator/adoption/destruction, installed
actors and accepted live SDA execution remain unqualified. Full pinned model
parity is incomplete.
