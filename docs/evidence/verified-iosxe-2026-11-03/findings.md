# Non-blocking status-reporting finding

After the pending-install recovery, the Pod was Running/Ready, HTTP returned
release A, and the Deployment reported Available=True and Progressing=True.
The Pod nevertheless retained `reason: ProviderFailed` and a prior mutation
lease warning in `message`.

The qualified candidate's `verifiedPodStatus` in
`internal/drivers/iosxe/verified.go` starts from `pod.DeepCopy()` and replaces
phase/container/condition fields without resetting the prior top-level reason
and message. Authoritative healthy status should clear stale failure text;
blocked status should describe the current observation. A regression test should
seed an old ProviderFailed status and check healthy and blocked results.

Impact: misleading diagnostic text after successful recovery. It did not affect
the observed native lifecycle, immutable identities, no-replay trace, Pod Ready,
HTTP release marker or Deployment availability. This qualification made no
runtime change; the finding remains visible in the retained candidate/evidence.
