# Durable profile/site application receipts

Switching and wireless profile/site additions now record latest-applied evidence
in the existing controller `ActionApplications` status. The shared association
executor, journal, source/owner/controller checks, CAS writer, transport and task
readers remain the execution path. No CRD field, new controller, external store
or user manifest option is introduced. The upstream mapping remains the pinned
NetAsCode 0.5.0 `catalystcenter_network_profile_for_sites_assignments` resource in
both `cc_network_profiles.tf` and `cc_wireless.tf`.

## Application evidence

New public additions use a versioned operation resource within the existing
`sites/` or `wireless-sites/` namespace. It binds the existing logical pair digest
and the public bookkeeping predecessor. The native request remains an Update
operation with a single POST body containing the selected site UUID. Native
profile and site UUIDs remain ParentID and TargetID. Whole-selection observation
and the common one-use submission guard still establish absence before dispatch.

Completion observes the original task root and complete tree, stable native
presence and UUIDs, and the common source/owner/journal bracket. It atomically
stores the Verified operation and its application row, then checks persisted
readback before releasing the operation. The row retains the exact config owner,
generations, source identity, task, submission time and verification time.
A public row digest detects inconsistent historical edits; an active commit
cohort digest binds the complete public bookkeeping state while Verified.
These are consistency checks, not protection against an authorized status writer
recomputing digests. No native setting value, credential or authentication token
is included in the public ledger.

The existing 256-row limit applies across all application types. Planning checks
capacity for the complete selected association cohort before submitting its
first addition. Reapplication of a removed association replaces its existing
latest-applied slot, including at capacity. Logical aliases, profile/site UUID
replacement, duplicate rows, changed owners and malformed evidence are fenced.
An unrelated history change during an acknowledged task prevents receipt
publication and retains Verifying for investigation rather than replaying.

Matching imported associations produce no application row. Legacy acknowledged
operations keep their original recovery path and gain no new application
provenance. Source deselection retains evidence and performs no native removal.
Unrelated native memberships remain untouched. The shared network-setting digest
implementation was extracted without changing its existing normalization.

## Qualification

[Restored-connection readback](profile-site-applications-connectivity.json)
records successful token, inventory and PnP HTTP 200 responses, three network
devices and an empty PnP inventory; the historical retained test UUID is absent.

[Focused race tests](profile-site-applications-focused.log) exercise 34 cases
across both namespaces. [Kubernetes API tests](profile-site-applications-api.log)
exercise the same 34 cases through actual temporary API objects: normal
Accepted completion, acknowledged Unknown completion, no-op imports, legacy recovery, repair, capacity and replacement at
capacity, missing/corrupt/duplicate evidence, historical row edits, concurrent
history changes, logical aliases, UUID replacement and source deselection.
The API setup seeds retained history after creating fresh API objects; otherwise
the common installer deliberately clears that history. Duplicate-row testing
uses distinct legal list-map keys so the runtime fence is actually exercised.
The focused command passed in 18.921 seconds; the expanded API command passed
in 114.348 seconds with 34 receipt and 102 prior network-application cases.
The prior 34 association API recovery cases passed separately in 16.492 seconds.

[Adjacent race regression](profile-site-applications-regression.log) covers
switching/wireless configuration, profile/task recovery and existing network
applications. The expanded API gate also repeats the network-setting application
cases after the shared digest extraction; [association recovery API regression](profile-site-applications-recovery-api.log)
repeats the prior 34 cases with versioned public additions enabled.
[Foundation checks](profile-site-applications-foundation.log) cover
shared controllers, Nexus Dashboard, admission, software lifecycle and direct
GNOI. Adjacent regression passed in 180.853 seconds; all nine foundation
packages passed, with unchanged packages served from cache.
[Native qualification](profile-site-applications-native.log) reuses generated
switching and disabled-SSID/empty-area wireless single/cohort workflows. Each
eligible mutation retires one proven unsent preflight refusal, recovers its
original acknowledged task as Unknown, verifies native state and reaches public
InSync with repeated no-op. Association completion additionally requires the
exact retained application row and task. Cleanup verifies the switching
profile/site/template/project and restores both prior wireless baselines.
All three workflows passed: switching in 93.52 seconds, wireless single-tag in
259.19 seconds, and wireless policy cohort in 365.22 seconds. Eighteen original
metadata tasks recovered with eighteen proven unsent refusals retired, including
five associations with retained application receipts. All three reached final
InSync/no-op; both wireless baselines were restored. The complete native command
passed in 720.090 seconds.

The native HTTP APIs, task trees, readback and cleanup are real. Kubernetes
actors/storage, interruption and preflight failure injection are simulated;
temporary API tests independently qualify persistence. No physical device action
or installed-worker operation is performed. [Source fingerprints and terminal
counts](profile-site-applications-sources.json) distinguish this milestone from
installed-binary qualification. [Strict documentation check](profile-site-applications-docs.log)
verifies these links.

## Remaining scope

Application evidence establishes that CVK applied an association after observing
its absence. It grants no exclusive creator authority over a profile/site, no
automatic adoption or owner handoff, and no unassignment/prune/delete permission.
Safe manifest-driven removal, dependent-consumer protection, broad hierarchy and
scale behavior, physical WLC/AP provisioning and installed actors remain open.
The 93-resource ledger and full pinned-model goal remain incomplete.
