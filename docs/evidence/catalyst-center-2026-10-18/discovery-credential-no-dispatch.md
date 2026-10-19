# Credential discovery no-dispatch recovery and lab qualification

The credential executors now separate their complete final preflight from the
native request and use the existing `guardConfigurationPreflight` capability.
The shared configuration receipt path claims a guard-bound, one-use proof and
retires the exact operation with the existing status CAS. Create, replacement,
prune and source-committed v2 config cleanup retain their typed source/secret,
owner, complete-cohort and capacity checks. No new transport, API, CRD, finalizer,
controller or credential store is introduced.

Only certified refusal before entering the native request can retire the slot.
Taskless/API failures, ambiguous responses, foreign/reused proofs, changed owners
or operations and already recorded effects cannot infer permission to reset.
Creator, retirement and delivered-version evidence is preserved. Fresh public
planning uses a new operation ID and current validated intent; an old closure or
proof cannot be reused. Old source-uncommitted cleanup formats remain blocked.

The four-family proof matrix has 13 cases per family, under controlled and real
Kubernetes API persistence: refusal, read error, status conflicts/exhaustion,
lost clear acknowledgement, owner/operation changes, task/credential contamination,
ordinary request error, ambiguity, acceptance and foreign proof. Delete fixtures
include valid creator/credential lineage so preparation retains its normal gate.
Public Create and config cleanup also inject a final read refusal and require a
later successful operation with a different ID. Existing uncertainty/cleanup
cases retain their separate expected fences.

Live qualification uses the lab API and an isolated Kubernetes API server,
with an enforced mutation-blocking native transport. Four cases cover inline,
ConfigMap and lost retirement/completion acknowledgements during unsubmitted
cancellation. A fifth case follows normal Prepared retirement into fresh planning
and injects a post-permit native read failure before any mutation closure is
entered. It stops after proven retirement; successful live credential creation is
not claimed. The original discovery job and device inventory must remain unchanged.

Final gates passed. The [validation manifest](discovery-credential-no-dispatch.json)
records source/binary hashes, package results, logs and the five structured live
qualification records. Full pinned-model parity is incomplete. Remaining scope
includes empty/unowned cleanup without a retained preparation, mixed Delete-policy
sources, credential handoff, native credential delivery/replacement/prune/cleanup
and installed actors. Taskless uncertainty continues to require investigation;
proof-of-no-dispatch support does not remove that safety boundary.


| Verification | Result |
| --- | --- |
| Expanded discovery/configuration race regression with real API persistence | Passed; 2223 passing test events, 535.169s |
| Existing adapter, ND, software-upgrade and maintenance race checks | Passed |
| SWIM, reload, XFSU and transfer fallback checks | Passed |
| Full application build | Passed |
| Guarded lab/API qualification | Passed; four cancellation cases and one injected post-permit read-failure case; zero native mutation attempts |
| Live health and existing-job/composition refusals | Passed; three devices, two images and one native discovery job preserved |
| Strict documentation build | Passed |

The frozen package snapshot contains 1,611 Go files. These are selected,
explicitly named gates rather than a whole-repository completion claim. Live
cancellation uses actual native reads and an isolated real Kubernetes API server;
it is not deployed-manager/worker or successful credential-mutation evidence.
