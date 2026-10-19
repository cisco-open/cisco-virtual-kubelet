# Discovery deployment selection

`controller-export-discovery` materializes the pinned NetAsCode 0.5.0
`cc_discovery.tf` deployment predicate before the source reaches CVK. It reuses
the shared managed-site selector and the existing discovery parser. Execution,
credential lookup, ownership, task recovery and pruning remain in the ordinary
inventory provider; no alternate transport or controller scope is added.

| Module options | Jobs selected |
| --- | --- |
| Empty `managed_sites`, global management false | All jobs: single-state mode |
| Global management true, with or without managed sites | All jobs |
| Nonempty `managed_sites`, global management false | Only jobs with `create_per_site: true` |

`manage_specific_sites_only` validates the managed-site selection context. It
does not further filter discovery jobs: the pinned discovery predicate does not
use it. Every requested site must exist in the supplied site declarations.

`create_per_site` controls which deployment state creates a globally named job.
It does **not** assign discovered devices to that site, rename jobs, restrict
address ranges to a site or authorize native mutation. Names must remain globally
unique across independently selected states. Native credential references resolve
through the existing unambiguous name/UUID readers; this export manages neither
credentials nor site definitions.

## Export a resolved source

The example input is
`examples/configs/catalyst-center/discovery-site-selection.yaml`.

```sh
go run ./tools/cvk-netascode-migrate controller-export-discovery \
  --model-version 0.5.0 \
  --managed-sites '["Global/Lab"]' \
  --output /tmp/cvk-lab-discovery-resolved.json \
  examples/configs/catalyst-center/discovery-site-selection.yaml
```

The output must be a new path. The tool creates it with mode `0600`, refuses
overwrites and symlinks, and prints only provenance to stdout. The source contains
only selected `inventory.discovery` jobs; deployment flags and site-selection
context are consumed explicitly. Use the resulting source with the existing
NetworkControllerConfig inventory contract and the reported model version and
exporter provenance. Existing mode, retention, ownership and uncertainty rules
still govern execution. Changing selection changes desired intent; explicit
runtime pruning policy remains responsible for owned removals.

All declarations are validated before selection. Invalid excluded jobs, duplicate
names, unresolved managed sites, inline credentials and unsupported sections fail
without producing a file or partial provenance. Other model sections must be
materialized separately; they are never silently discarded. This exporter expects
already resolved leaf fields/defaults and does not implement full model merging,
schema validation, credential adoption or complete NetAsCode parity. Direct
runtime submission of unresolved `create_per_site: true` remains refused.

## Qualification

The [validation audit](../evidence/catalyst-center-2026-10-17/discovery-selection-validation.json)
records exact source hashes, completed logs and the built CLI demonstration.
Selection/export, full discovery regressions and the complete migration CLI suite
pass under the race detector. A separate real API-server test passes selected-job
creation, acknowledged-task recovery and owned replacement.

The native lab qualifier selected one of two valid declarations, executed only
that generated job against the existing `.101` lab device, then pruned,
reintroduced and pruned it again. Exact name/UUID/credential/target/method limits
observed two Creates, two Deletes and four distinct Unknown-task recoveries with
no replay. The original discovery baseline was restored; device inventory identity
and count checks passed. No assignment, provisioning or software upgrade was
performed. Discovery itself can query devices and refresh inventory.

Native Kubernetes persistence/restart was simulated in that qualifier; installed
selected-export execution remains pending. The real API-server gate is separate.
This does not qualify full discovery credential combinations, unchanged-intent
reruns, initial taskless-uncertain cancellation, mixed-resource deletion or CR
deletion. Reporting and creation ownership remain distinct from discovery job
completion and device authentication outcomes.
