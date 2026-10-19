# Tag pagination guards and matching-rule clearing

## Implemented

Tag definition submission now requires two complete fresh catalog observations
of every declared tag, each matching the original plan. Both observations replan
and compare the exact frozen request. This extends the existing one-use closure;
refusal, read failure, missing acknowledgement and success all consume it.
Only selected rows are compared, so unrelated catalog activity is tolerated.
Existing shared transport, journal, dispatch authority, actor and ownership
requirements remain in force. No CRD, ND or direct-device/SWIM logic changed.

The catalog API does not offer an atomic snapshot or conditional update here.
Two observations detect the reproduced inter-page race; they do not eliminate
all concurrent changes after the last read or replace durable recovery.

## Reproduction and regression

A 501-row fixture changes an earlier row while page two of the first fresh
observation is fetched. The original writer submitted stale writes in five cases:
protection flip, UUID replacement, changed preserved description, changed later
selection and a concurrent create. See [before-fix output](tag-pagination-before.log).

The fixed regression refuses all five before mutation, permits unchanged and
unrelated changes, and refuses replay without further reads or writes. Existing
frozen-input, preserved-rule, missing-acknowledgement and whole-selection cases
also pass. [Focused race tests](tag-pagination-focused.log): **2.658 s**.
[Affected race suite](tag-pagination-affected.log): **105.485 s**, covering tags,
template/device membership, templates, inventory, discovery, AP, extranet and SWIM.

## Native qualification

Both runs used authenticated Catalyst Center at the existing localhost tunnel,
two repeatedly pinned wired inventory identities and a unique temporary tag.
They used compiled CVK executors and existing transport/task-tree verification,
then checked native definitions, fresh-compiler no-op and membership provenance.
No device configuration or template deployment was performed.

- [Two-condition AND/EQ cycle](tag-positive-and-native.log): first switch to
  second switch to no match to cleared rules. Test **16.77 s**, package **18.702 s**.
- [Unary AND/OR and matching clear/restore](tag-positive-unary-clear-native.log):
  first switch to second switch to rules cleared while matching, then restore the
  first switch, change to no match and clear again. Test **24.02 s**, package
  **26.198 s**. The source alternates unary AND/OR; compiled requests use the
  previously qualified equivalent atomic EQ representation.

The second cycle independently observed rule counts **1, 1, 0, 1, 1, 0** and
DYNAMIC/unfiltered member counts **1, 1, 0, 1, 0, 0**. Cleared native rule lists
were null; selected empty-list comparison still converged. STATIC/MIXED and
template memberships remained empty. Same-tag UUID, exact owned-UUID deletion
and restoration of the complete original tag baseline passed for both runs.

## Remaining scope

Multi-condition OR, LIKE/ILIKE and IN positive evaluation, interface rules,
broader combinations and pagination scale remain unqualified. Tag prune/delete
and system-tag mutation, installed actors and durable template project-incarnation
binding remain incomplete. These results close the direct matching-clear
qualification gap for this exact networkdevice EQ/unary flow; they do not prove
full NetAsCode parity or installed-worker qualification.
