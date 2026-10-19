# Native bulk-site qualification

The pinned NetAsCode 0.5.0 `catalystcenter_areas`, `catalystcenter_buildings` and
`catalystcenter_floors` resources use bulk site creation. The native primitive
now builds a single area/building/floor payload using existing singular-site
validation and numeric normalization. Existing divergent sites are refused:
the [Cisco bulk API](https://developer.cisco.com/docs/catalyst-center/create-sites/)
ignores existing sites and cannot substitute for their update operations.

The primitive validates the whole hierarchy before submission, requires building
country and complete floor geometry, and carries parent hierarchy paths rather
than locally generated planning IDs. Complete site inventory and existing UUIDs
are revalidated before the supplied guard is called. The submission closure is
one-use even after guard denial or an ambiguous task receipt. No retry, journal,
transport or physical mutation mechanism was added.

Local site/bulk race regression passed (5.630s), including full hierarchy
construction, descendant preflight, existing divergence, replaced parent,
missing/denied guards, accepted and ambiguous receipts, and replay refusal.
Live-tag compilation also passed after retaining the existing IPAM qualification
helper interface.

On 2026-10-14, a single native bulk request created
`Global/CVK-Q-20261014-141541-000000000` and its test building/floor. The test
verified task completion and independent readback of all three site identities,
then used the ordinary executor for a floor-width update, verified a no-op,
and deleted only verified test identities in child-before-parent order.
Cleanup passed. The live test took 11.89s (package 13.204s).
See [retained live output](site-bulk-live.log).

This is native bulk-create qualification, not full resource support. Manifest
runtime activation remains gated until durable batch member selection,
ownership, partial-batch failure handling and restart recovery use the existing
configuration journal. Bulk update/delete, maps, AP placement and installed
actor qualification remain open. Singular manifest execution remains unchanged.
