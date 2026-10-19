# Catalyst Center alpha publication qualification

Assessment date: 19 October 2026. Branch: `pr/johalley/catc-support`.

This record accompanies the [alpha scope](../../controllers/catalyst-center-alpha.md)
and [full-parity roadmap](../../controllers/catalyst-center-roadmap.md).
It does not establish full NetAsCode parity or production readiness.

## Publication inventory

The initial inventory contained 67 modified tracked files and 4,987 untracked
files (approximately 62 MB total changed/untracked content), including source,
tests, examples, pinned upstream fixtures/licenses and dated qualification
evidence. New alpha documentation and CI wiring are additional changes.
Publication review includes current credential-value matching, encoded-credential,
token/private-key pattern inspection, formatting and whitespace checks. Local
credentials, recovery backups and raw qualification work directories outside the
repository are not publication inputs.

## Checkpoint checks

The implementation is committed as
`bb28e4a568a8ffc3009d241f287a6404abdce551`. Source/test/schema/example changes
are separated from the documentation/evidence/CI commit for review.

| Check | Local result |
| --- | --- |
| All-package production build and vet | Pass, including a final run after generated artifact synchronization |
| First-party Go formatting | Pass, 2,716 files inspected |
| Release contract and workflow syntax | Pass; smoke now covers the ND PR destination |
| Helm lint, topology and configuration-protocol rendering | Pass |
| Generated configuration docs, parity matrix and NX-OS oracle | Pass |
| CRD, DeepCopy, RBAC generation and chart synchronization | Pass after correcting two drift failures; second isolated generation matches the branch |
| Shared real-API admission and runtime identity | Pass; rerun after the generated fixes |
| API/shared/controller/ND/worker race follow-up | Pass after the generated fixes |
| Module integrity | Pass |
| Reachable Go vulnerability scan | Pass: no reachable vulnerabilities found; advisories in imported/required code not called by this build remain reported by the scanner |
| Offline model report | Pass: eleven sections, no unknown sections; explicitly reports incomplete parity |
| Strict full-parity gate | Expected refusal, exit 3; this is not a full-parity pass |
| Broad recursive race regression | Pass: 22,694 passing events (22,626 named test events and 68 package passes); 280 packages have no tests and 70 opt-in/other tests skipped |
| Complete temporary-API Catalyst Center suite | Pass: all 163 selected top-level tests, 3,348 passing events including the package result; seven nonapplicable subcases skipped and listed in the machine-readable record |

The broad suites began before the generated synchronization; the focused
post-generation race/admission reruns cover the affected API and shared runtime.
The generation fixes replace manually appended DeepCopy methods with controller-gen
output and synchronize the Helm CRD's field order/descriptions with its source.
They introduce no new adapter execution capability.

The recursive run completed close to its original 40-minute package deadline.
CI now retains all tests with a 60-minute package budget and a 105-minute smoke
job budget, allowing the remaining build/admission/image/scheduler checks to run.

[Machine-readable gate results, source fingerprint and private-log hashes](alpha-checkpoint.json)
record the exact counts and skipped test names. New publication check logs remain
private; the PR's CI jobs must reproduce the gates on the published head. The
seven temporary-API skips comprise six cross-member alias subcases without a
bulk producer and one partial-bulk subcase without a cohort. These are not
passing qualification of those absent combinations. Two
private installed-composition tests are outside the selected `TestEnvtest`
suite and remain outside this qualification.

Publication checks matched the supplied credential values, Base64/Basic-auth
and URL-encoded forms without printing them, and inspected private-key, token and evidence
credential-literal patterns; no credential contents were found. A broader field
scan produced sixteen `secretKey` matches, reviewed as Secret entry-name metadata
in retained version references, not key material. These are heuristic checks,
not a guarantee that arbitrary secret formats are absent. Three historical
terminal logs retain their original trailing spaces under narrowly scoped Git
attributes so their existing evidence hashes remain valid.

Remote CI, image/platform checks, independent review and the ND dependency remain
mandatory on the actual PR head and merge destination. A local pass does not
close those gates.

The earlier SWIM review also recorded an intermittent restored-manager startup
probe failure with no proven root cause; later exact-head CI passed and failure
diagnostics were retained. Re-exercise that probe on this head and preserve the
diagnostics if it recurs. Historical green runs do not resolve a new failure.

## Earlier native evidence

The latest [switching native-observation qualification](switching-profile-native-observation.md)
passed a bounded native cycle with exact generated-resource cleanup, real
temporary-API cases, build/docs and a sixteen-package regression. It did not
target a real device or publish durable latest-effect receipts; native cycle
Kubernetes persistence was simulated. Older live evidence retains its original
source/environment limitations. No live mutation is required to publish the
documentation checkpoint, and none is implied by the offline checks above.

## Merge dependency

PR #204 currently targets the still-open ND PR #203 branch. Publication does not
resolve that dependency or authorize bypassing required reviews/CI. Verify the
destination and rerun affected gates after any base or source change.
