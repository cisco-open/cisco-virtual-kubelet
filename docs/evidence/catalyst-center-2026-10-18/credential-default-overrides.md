# Scoped credential default overrides

The offline credential exporter now accepts `--defaults` for supported non-secret
HTTPS port and SNMP protocol fields. The existing strict defaults decoder has
been factored into scope-aware profile/credential entry points; no duplicate YAML
parser or runtime credential resolver was introduced. Pinned fallback, override
and explicit entry precedence use the existing execution parser. Every default,
including unused/shadowed fields, is validated before export. Null, non-finite,
unknown, secret-bearing and unrelated-scope data are refused without payload.

Final race regressions pass **15 families/59 subcases**, including existing
network-default and CLI behavior. Real private Kubernetes API qualification passes
**24 cases** in **37.010s** for pinned and override-expanded standalone/composed
credential manifests. Native task services and projection delivery in these API
tests are simulated. CLI demonstration resolves an HTTPS 8443 port and AES256
privacy override into a new mode-0600 file with matching output/override hashes.

The first native run verified all three original task recoveries but stopped
before deletion: a test cleanup guard still required AES128 instead of the
resolved AES256 declaration. Production code was not changed to bypass this.
The exact retained generated UUID was independently checked against its terminal
journal, declared metadata and successful original task, then removed through
the shared client. Unrelated non-secret native metadata stayed unchanged.

After correcting the guard to compare the declared protocol, a fresh native cycle
passed in **13.531s**: AUTHNOPRIV creation, promotion with AES256 privacy, and
authentication rotation recovered three original tasks on the same UUID, then
supervised exact cleanup restored the non-secret baseline. Both generated
fixtures are removed. Native Kubernetes persistence was simulated and neither
credential was assigned to a site/device. Physical authentication was not tested.

ND (**4.734s**), GNOI/SWIM (**6.026s**) and production build pass. The new binary was
not installed. General default resolution for usernames, modes, secrets/counters,
other model sections, site selection, merging, full schema/parity and destructive
credential lifecycle remain pending. Supervised cleanup does not implement
manifest-driven pruning. Historical failed and passing runs retain separate source
boundaries and logs.

See [operator usage](../../controllers/catalyst-center-credential-defaults.md),
[source/image/provenance/outcome record](credential-default-overrides-validation.json),
[regressions](credential-default-overrides-regression.log),
[private API](credential-default-overrides-api.log),
[final native cycle](credential-default-overrides-native.log),
[initial native guard refusal](credential-default-overrides-initial-native.log),
[retained fixture cleanup](credential-default-overrides-retained-cleanup.log),
[ND](credential-default-overrides-nd.log),
[upgrades](credential-default-overrides-upgrades.log) and
[build](credential-default-overrides-build.log).
