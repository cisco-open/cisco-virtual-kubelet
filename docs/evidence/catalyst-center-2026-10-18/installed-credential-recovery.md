# Installed metadata-only credential recovery

The isolated qualification cluster now runs the current Linux ARM64 manager and
network-controller worker image, built from the frozen source boundary recorded
in the [validation record](installed-credential-recovery-validation.json).
The existing controller and its site configuration were reused. Neither the main
cluster nor any device worker was changed; this qualification namespace has no
CiscoDevices. The harness never reads a Kubernetes Secret or copies projected
keys to the host.

## Executed flow

1. Created a generated ConfigMap and a new network-settings configuration in
   report mode. The existing site owner remained present.
2. Enabled apply for one generated, unused SNMPv3 NOAUTHNOPRIV definition. A
   deliberately short `1ns` task timeout exercised the production Accepted to
   Unknown transition; the harness did not edit journal phases or task IDs.
3. Observed the acknowledged Unknown Create, saved the exact operation/Pod
   identities, then deleted only that worker Pod with a UID precondition. The
   manager created a replacement worker using the same source-pinned image.
4. The replacement worker recovered the original task and published its verified
   UUID receipt through the normal Kubernetes status path.
5. Changed the username through the same ConfigMap UID. The installed worker
   submitted an Update, naturally retained it as Unknown, then recovered its
   original task and retained the same native credential UUID.
6. Switched the generated configuration to report mode before separately
   supervised exact-UUID cleanup. The cleanup task tree succeeded and the full
   non-secret global credential metadata baseline was restored.

The native test passed in **52.566s**. The original operation/task identity was
checked throughout each recovery. This does not supply an HTTP request counter
inside the installed worker. The existing controlled/native component tests
provide separate duplicate-write checks.

## Terminal state and fixture correction

Final inspection found an unsupported terminal report fixture: an explicit empty
`snmpv3_credentials` array. This did not invalidate the completed task recovery
or native cleanup, but left report status Failed. The source was then corrected
under the same ConfigMap UID to the already supported empty `network_settings`
object. Both configurations were independently verified InSync at their current
generations; the journal is idle, all **12** prior site receipts remain unchanged,
and both manager and replacement worker Pods are ready. One historical metadata
receipt remains, with no desired credential mutation in report mode.

The executed harness hash is preserved separately from the final harness hash.
The final harness uses the supported idle source and requires terminal InSync;
that correction was compiled with live mutations disabled. The native mutation
cycle was not replayed. Explicit empty credential collections remain a model gap;
the terminal correction does not implement them.

## Evidence and limits

See the [native test log](installed-credential-recovery-native.log),
[production build log](installed-credential-recovery-build.log),
[image build](installed-credential-recovery-image-build.log),
[image load](installed-credential-recovery-image-load.log),
[Helm rollout](installed-credential-recovery-helm.log),
[terminal report wait](installed-credential-recovery-terminal-report.log) and
[identity/source/result record](installed-credential-recovery-validation.json).
The earlier [metadata recovery checks](global-metadata-recovery.md) cover the
unchanged production sources with race, private API, ND and GNOI/SWIM gates.

Installed qualification here covers metadata-only credentials. Password-backed
credential projection/rotation recovery in installed actors, physical credential
authentication, receipt-family conversion, credential pruning/handoff and full
NetAsCode parity remain pending. Supervised fixture cleanup is not declarative
credential deletion. The tiny timeout is fault injection for this test, not a
production recommendation.
