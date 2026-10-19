# Active credential default expansion

A dedicated offline credential export now consumes the shared pinned 0.5.0
defaults bundle and strict decoder, supplies missing HTTPS ports and active
SNMPv3 protocol fields, then validates with the ordinary versioned credential
parser. It adds no runtime resolver, transport, writer or mutation authority.
The migration CLI reuses the existing private output/no-overwrite helper.

Focused race checks pass six families/38 subcases across export, existing network
defaults, CLI and public manifest recovery. All six credential types pass separate
real private API persistence in standalone/composed manifests (12 subcases,
20.887s), with native tasks/projection delivery simulated. CLI export regressions
pass seven families/nine subcases (2.219s). The non-secret example resolves three
resources/four fields into a mode-0600 file whose bytes match provenance.

Native SNMP qualification deliberately omitted protocol fields at every revision.
The exporter supplied one active default for AUTHNOPRIV, two for promotion to
AUTHPRIV, then two for authentication rotation. All three original acknowledged
tasks recovered, the UUID stayed stable, and supervised exact-UUID cleanup
restored the non-secret baseline. This run passed in 13.007s with simulated
Kubernetes persistence. HTTPS port export has controlled/API qualification here;
no new installed exporter-driven or physical authentication qualification is
claimed. Earlier installed credential execution evidence retains its own sources.

ND (4.753s), GNOI/SWIM (6.273s) and production build pass. The new build was not
installed. Pinned active defaults only are covered: no raw secret values,
version-counter inference, custom override files, per-site filtering, other
sections, multi-file merge, full nested schema validation or full model parity.
Explicit null/contradictory inactive fields remain subject to the strict existing
execution contract. Native cleanup is separate from declarative pruning.

See the [operator contract](../../controllers/catalyst-center-credential-defaults.md),
[source/provenance/task record](credential-defaults-validation.json),
[focused checks](credential-defaults-focused.log),
[private API](credential-defaults-api.log),
[CLI](credential-defaults-cli.log),
[native](credential-defaults-native.log),
[ND](credential-defaults-nd.log),
[upgrades](credential-defaults-upgrades.log),
[build](credential-defaults-build.log) and
[disabled native compile](credential-defaults-optout.log).
