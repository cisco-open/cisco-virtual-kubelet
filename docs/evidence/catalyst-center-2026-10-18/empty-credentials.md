# Explicit empty credential selections

The global credential parser now accepts empty lists for all six known families
and an empty `device_credentials` object. This matches zero-instance selection in
the pinned 0.5.0 module's credential `for_each` expressions. Null, non-list,
unknown-family and invalid companion declarations remain rejected. The change
reuses the ordinary report compiler, adapter-owned credential plan and journal;
it adds no transport, writer or deletion behavior.

With `prunePolicy: Retain`, zero selection performs no credential writes and
retains ownership and application receipts. An outstanding Accepted, Unknown or
Verifying operation from the earlier intent remains fenced. Empty credential
intent cannot enter the IPAM deletion executor. Credential deletion and ownership
handoff remain separate, unimplemented lifecycle capabilities; zero-instance
selection does not establish Terraform destruction parity.

| Gate | Result |
| --- | --- |
| Credential race regressions | 17 families / 166 subcases; 11.204s |
| Additional deletion-path refusal checks | Passed; 2.402s |
| Real private Kubernetes API | Four idle/Accepted/Unknown/Verifying cases; 9.033s |
| Installed manager/worker plus native API | Passed; 3.915s |
| ND regression | Passed; 4.559s |
| GNOI/SWIM regression | Passed; 6.378s |
| Linux ARM64 production image | Built, loaded and rolled out in isolated cluster |

Installed qualification reused the settled metadata credential owner, its source
ConfigMap and the same controller UID. It exercised all six empty lists together,
then an empty credential object, in both apply and report modes. All four revisions
reached InSync with zero represented resources. The complete journal was unchanged
on every observation, including all 12 prior site receipts and the retained
metadata receipt. Full non-secret native credential metadata was unchanged. Both
configurations finished InSync with an idle journal. No native credential was
created, updated or deleted by this qualification, and the host API client was
restricted to reads/authentication. No Kubernetes Secret was read by the harness.

The earlier installed recovery test's rejected empty-list fixture is now supported
by a new source-pinned build. Its historical log and later fixture correction are
preserved rather than relabelled as qualification of this code.

Use the [resolved empty model example](https://github.com/cisco-open/cisco-virtual-kubelet/blob/pr/johalley/catc-support/examples/configs/catalyst-center/empty-credentials.yaml)
with the existing global network-settings configuration and explicit Retain
policy. Keep outstanding operations settled before changing credential intent or
its projected references. This milestone does not qualify installed password
projection/rotation recovery, physical authentication, receipt-family migration,
pruning/handoff or complete NetAsCode parity.

See the [source, image and result record](empty-credentials-validation.json),
[regression log](empty-credentials-regression.log),
[deletion fence](empty-credentials-deletion-fence.log),
[API log](empty-credentials-api.log),
[installed/native log](empty-credentials-installed.log),
[ND log](empty-credentials-nd.log),
[upgrade log](empty-credentials-upgrades.log),
[build](empty-credentials-build.log),
[image build](empty-credentials-image-build.log),
[image load](empty-credentials-image-load.log) and
[rollout](empty-credentials-helm.log).
