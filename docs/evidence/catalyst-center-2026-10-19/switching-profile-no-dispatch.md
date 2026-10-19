# Switching profile automatic preflight recovery

Pinned NetAsCode **0.5.0**, revision
`a435555fe828f03cf3311d52fe1ab6572a247916`, branch
`pr/johalley/catc-support`. Full resource and model parity remain incomplete.

## Execution change

Switching Create/Update plans now wrap their original two fresh native-state
observations with CVK's existing one-use pre-submit proof mechanism. The same
frozen whole-selection checks protect profile UUIDs, template/project lineage,
membership, converged selected peers and inherited attributes. The common wrapper
consumes the closure even on refusal; no separate permit or lifecycle is added.

A failure during these checks occurs before the original mutation function runs.
Only that guard's private one-use capability can retire the exact unsubmitted
journal through existing source/owner/operation CAS checks. Retirement preserves
the section owner and existing receipts; reconciliation then observes current
state and creates a fresh plan. Group composition retains the same proof chain.

Once mutation is invoked, transport errors, lost acknowledgement and invalid
receipts acquire no such capability. Reusing a closure or proof cannot submit
again or retire another operation. Previously uncertain taskless journals do not
become safe merely because the current implementation has a stronger guard.
Acknowledged original-task recovery and full-tree completion remain unchanged.

## Verification

Existing submission-guard cases now additionally assert that changed profile,
project, template, selected peer or inherited state issues a claim-once proof,
whereas lost acknowledgement does not. The shared proof matrix retains coverage
of owner/parent/generation/operation changes, foreign/reused capabilities, status
conflicts and already recorded tasks/receipts.

Public Create and Update tests inject a native inventory read failure only after
the operation is durably Submitting. They require zero mutation, removal of only
the unsubmitted operation, unchanged owner/receipts, fresh automatic planning,
one successful native request, original acknowledged-task recovery and InSync.
The same cases run with real temporary Kubernetes API persistence alongside
existing recovery/refusal cases. This does not relax native parsing or fabricate
successful task responses.

All 38 real temporary-API cases pass. A separate native public-reconciler cycle
qualifies one Create and three Updates: each source revision first encounters an
injected inventory read failure while Submitting, verifies zero mutation and
owner/receipt-preserving journal retirement, then replans and submits exactly
once. All four original tasks recover from Unknown and reach InSync with restarted
no-ops. The cycle includes assignment to an empty generated area and membership
changes afterward. Exact profile/assignment/area, template and project cleanup
completes. No real device or template deployment is targeted. Native Kubernetes
persistence is simulated; real temporary-API persistence is qualified separately.

The focused race gate passes 705 test events. The broad regression passes
19,781 events across sixteen packages, including ND, shared configuration,
maintenance, direct-device and SWIM paths. The production worker build and strict
documentation build pass.

Gate outcomes, native task identifiers, fingerprints and log hashes are in
[the machine-readable evidence](switching-profile-no-dispatch.json).

## Remaining scope

This adds no creator ownership, adoption, prune, deletion authority or native
Create-result correlation. Installed source/worker recovery, broader onboarding/
composite/multi-site and schema/default/catalogue coverage remain pending. Complete
resource, section and 93-resource model parity are not advertised.

Read-only inspection of this captured native Create root and tree finds the
observed profile UUID inside free-form progress, with no structured UUID result
in those two responses. The cached pinned 0.6.1 provider retrieves the collection
and selects by name after POST; that does not establish creator custody for CVK.
The progress grammar/result contract is a lead for further correlation work,
not an exhaustive absence claim or authority to adopt/prune a same-name object.
The sanitized [shape evidence](switching-profile-no-dispatch-create-result-shape.json)
contains field names and UUID locations, never raw progress or inherited values.
