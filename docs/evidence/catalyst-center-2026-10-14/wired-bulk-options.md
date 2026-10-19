# Bulk module options and public fallback prevention

Pinned NetAsCode 0.5.0 keeps `use_bulk_api`, `bulk_site_provisioning` and
`managed_sites` outside the resolved Catalyst Center model. CVK now imports the
bulk inventory grouping controls through annotations, following the existing
bulk-sites annotation convention, without inserting CVK options into that model.

| CVK annotation | Pinned module concept | Value |
| --- | --- | --- |
| `cisco.vk/catalyst-center-bulk-devices` | `use_bulk_api` for inventory provisioning | `true` or `false`; absent means false |
| `cisco.vk/catalyst-center-bulk-site-provisioning` | `bulk_site_provisioning` | Global hierarchy path, at most five levels; requires bulk true |
| `cisco.vk/catalyst-center-managed-sites` | Bulk grouping using `managed_sites` | JSON list of paths; requires bulk true in this staged inventory path |

This is bulk inventory option import, not implementation of all upstream
managed-site/global-settings selection across other sections. The site API's
existing separate bulk annotation remains unchanged.

## Selection and bounds

Typed options feed the existing source declaration/grouping helper and its
complete supplied site set. Default grouping, root precedence over managed-site
grouping, strict hierarchy boundaries and ten-member homogeneous chunking reuse
the existing implementation. Source bytes remain unchanged. Root existence in
the supplied model site set is checked separately from native site existence.
Null/non-list managed-site values, duplicate paths, bad hierarchy, unknown roots,
overlapping managed roots without explicit root precedence, invalid booleans or
wrong managed sections are refused. Managed annotation input is bounded to
64 KiB and 1000 paths.

## Public behavior

An explicit bulk request can no longer fall through to the singular inventory
compiler. New Apply requests report BulkExecutionUnavailable, because public
all-member preparation, real device-convergence proof and settlement remain
unavailable. Invalid options report InvalidBulkOptions. Both paths stop before
native API access, ownership, journal creation or reservation admission. They do
not pretend that the private staged cycle is production-ready.

Retained acknowledged-task recovery precedes option validation. Removing or
invalidating annotations therefore does not retarget, replay or abandon an
accepted task. Recovery still binds the original source and complete retained
member identity and retains the convergence fence.

## Verification

Fifteen option/source grouping cases and three full public Reconcile cases cover
selection, invalid inputs, preservation of PROVISION/REPROVISION actions and no
native access/ownership/journal side effects. Public acknowledged recovery tests
add removed/invalid option cases, with three simulated restarts and unchanged
task/member scope. Affected Catalyst Center/shared race tests passed in 24.694s
and 7.309s. See [affected tests](wired-bulk-options-regression.log).
The complete race suites also passed: Catalyst Center 242.872s and shared
configuration 31.278s. See [full regression](wired-bulk-options-full-regression.log).
Strict documentation build and `git diff --check` passed.

## Live option selection

Imported annotations selected the Global root for three real lab inventory
identities, using the observed native site catalog as the supplied site set.
Preparation and missing-permit refusal passed in simulated Kubernetes storage,
with final native provisioning records unchanged. The supplied native catalog
is not qualification of a complete resolved model's site composition. No native
mutation permit, task acknowledgement or installed actor was fabricated.

Live test passed in 17.14s (package 17.960s). See
[live evidence](wired-bulk-options-live.log) and
[sanitized access](wired-bulk-options-live-access.log).

## Remaining scope

Public bulk execution still requires full model/site composition, real
all-member preparation and installed actor admission, independent source-bound
device-convergence proof, receipt/completion/settlement wiring and accepted live
mutation/recovery. Full resource/section parity remains false. Option parsing or
negative qualification does not close those requirements.
