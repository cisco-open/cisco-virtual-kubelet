# Private credential custody publication

This stage adds a private metadata-only ownership CAS after the existing handoff
observation. The inventory owner and a versioned custody link in the existing
retirement condition change together. Controller UID/generation, exact initial
successor UID/name/generation/spec, source UID/version/model digest, resolved-input
digest and predecessor retirement digest are bound. No new CRD, finalizer,
transport, journal or NetAsCode input field is introduced.

Publication checks the original credential snapshot before repeating the full
read-only observation and checks its bytes again before the CAS. Recompilation
cannot silently bless a same-version secret change. The predecessor must be gone,
native UUIDs absent, target names unowned, and the versioned history seal present.
The original creator, retirement and delivered-version rows remain byte-for-byte
unchanged; no native mutation or config/finalizer write is performed.

A separate metadata/readback validator checks the custody link, current reservation,
archived cohort and seal, then reconstructs only the immutable predecessor view
for the existing strict proof validator. It recognizes a committed CAS after a
lost acknowledgement. An old observation cannot repeat publication. Ordinary
handoff and the unqualified foreign-lineage executor still refuse these records.
The decoder is a provenance/readback prerequisite, not a native dispatch permit;
current source/projection/native validation remains mandatory before any action.

Fifteen scenarios run with controlled persistence and a real isolated Kubernetes
API: inline/ConfigMap/same-name success, lost/rejected acknowledgements, source,
config, projection and same-version secret changes, old native UUID return,
ledger change, legacy seal refusal, forged observation, controller-generation
change and unresolved snapshot refusal. Each successful scenario checks twelve
metadata/evidence alterations, unchanged receipt history/config/finalizers and
replay refusal. Rejected CAS leaves the old reservation intact; lost acknowledgement
is independently readable without another ownership write.

Final source-bound gates passed. The [validation manifest](discovery-credential-custody.json)
records source/binary hashes, the exact selected test command, results and logs. Lab tests here
cover existing guarded read-only/cancellation behavior, not actual credential
custody or native device discovery. Full pinned-model parity is incomplete.

Public orchestration and foreign credential execution remain disabled. The record
currently requires the exact initial successor revision and a predecessor that
passes existing single-owner closure verification. Remaining work includes normal
execution/admission of the sealed cohort, safe intent revisions, cancellation and
recovery, subsequent multi-owner retirement, public sequencing and installed/native
qualification. No complete end-to-end handoff is claimed by this evidence.


| Verification | Result |
| --- | --- |
| Private custody, cohort/seal, handoff observation, completion, public deletion, cancellation/no-dispatch and ordinary handoff race/API regression | Passed; 446 passing test events, 145.816s |
| Existing adapter, ND, software-upgrade and maintenance race checks | Passed |
| SWIM, reload, XFSU and transfer fallback checks | Passed |
| Full application build | Passed |
| Existing guarded live/API baseline, cancellation and refusal cases | Passed; three devices, two images and one discovery job preserved; zero native mutation attempts |
| Strict documentation build | Passed |

The frozen package snapshot contains 1,620 Go files. Verification covers the
named gates; it is not a whole-repository or full-model completion claim. Private
custody tests use controlled native reads and real Kubernetes persistence. Public
handoff, native custody transfer and subsequent native execution remain unqualified.
