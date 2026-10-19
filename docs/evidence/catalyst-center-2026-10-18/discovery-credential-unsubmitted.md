# Cancellation of unsubmitted credential discovery

The public deletion lifecycle now handles a config removed while its first
credential Create remains Prepared. The existing configuration journal and
ownership-retirement condition retain the original operation atomically with
slot release. The canonical record includes its source/version binding and
credential version labels; no secret bytes or secret hashes are stored.

The worker requires the original source attestation, exact first declared input,
complete pure-credential projection, exclusive inventory ownership and no prior
creator/delivery/cancellation receipts for that owner. Two native absence reads
are bracketed by unchanged source/config/controller/ledger checks. Source and
projection are validated again after the final native read. No native mutation
or fabricated creator/credential receipt is permitted.

After a lost or rejected status acknowledgement, the reconstructed worker repeats
source/projection/native checks using the same retained original operation. It
uses the shared completion writer. The existing manager verifies the canonical
completion and retirement pair using metadata only, releases its discovery
finalizer and preserves unrelated finalizers, inventory ownership and the witness.
Ordinary ownership handoff refuses these records.

Sixteen scenarios run with controlled persistence and with the real Kubernetes
API: inline/ConfigMap success; source, projection or scope loss; native conflict
or read error; source/config/journal races; task or delivery-receipt contamination;
and lost/rejected retirement or completion acknowledgements. Eleven alterations
per successful case verify manager refusal of changed witness, task, phase,
source, digest, completion, owner, audit, controller, spec or scope. Test writers
retain the worker's status-only permissions.

The targeted run passed together with existing public deletion cases. Final frozen-source gates passed; the
[validation manifest](discovery-credential-unsubmitted.json) records source and
binary hashes and package result logs. This does not qualify installed processes or native
credential delivery. Live tests here are enforced read-only refusal/baseline
checks. Full pinned-model parity remains incomplete.

Remaining scope includes general empty/unowned cleanup without an original
preparation, mixed Delete-policy sources, credential ownership handoff,
post-permit no-dispatch recovery, native credential delivery/cleanup and installed
qualification. Submitting and taskless outcomes are never treated as Prepared.


| Verification | Result |
| --- | --- |
| Expanded discovery/configuration race regression with real API tests | Passed; 2113 passing test events, 520.496s |
| Existing adapter, ND, software-upgrade and maintenance race tests | Passed |
| SWIM, reload, XFSU and transfer fallback race tests | Passed |
| Full application build | Passed |
| Live read-only health, inventory and credential-discovery refusal tests | Passed; three devices, two images and one discovery job preserved |
| Strict documentation build | Passed |

The final package snapshot contains 1,608 Go files. Qualification is limited to
the named test families and gates; it is not a whole-repository or full-model
completion claim. Existing installed ordinary-discovery evidence does not qualify
this new credential path.
