# Embedded and generated source redistribution

This directory supplements the dependency licenses collected by `go-licenses`.
Module-level detection treats this repository as Apache-2.0 and cannot identify
separately licensed data embedded in its packages. `source/` preserves the exact
selected files, without rewriting their notices. `MANIFEST.json` lists their
repository-relative paths, byte sizes and SHA-256 digests. Only material linked
into the selected binary is included; a binary with no such data has only the
project license and this explanation in its source inventory.

- `internal/controlleradapter/sdwan/definitions/` contains the JSON source form
  of embedded SD-WAN metadata derived from MPL-2.0 upstream provider sources.
  Its `LICENSE` and `README.md`, the pinned repositories/commits in
  `internal/controlleradapter/sdwan/testdata/upstream-lock.json`, and available
  generation scripts under `scripts/sdwan/` accompany the data. Upstream source
  is available from the repositories at those exact commits. The native CVK
  interpreter is independently implemented; it does not run Terraform.
- `internal/drivers/iosxe/configdriver/generated/` and
  `internal/drivers/iosxe/models.go` contain generated YANG schema data compiled
  as Go literals. These are not discoverable through `go:embed` alone. Linked
  generated source, local App-Hosting YANG inputs, release provenance and Cisco
  API license attribution are included; see `third_party/cisco-iosxe/README.md`.
- `internal/drivers/iosxe/configdriver/schema/{families,yang-versions}.yaml`
  are CVK-maintained configuration indexes, distributed under the project license.

The project `LICENSE` applies to CVK-authored code, not as a replacement for the
upstream terms and notices described above. Go dependency licenses remain in
the separate `/licenses/third-party` bundle; Go's license and patents remain
under `/licenses/go`.

The image build derives this inventory from its target-specific `go list -deps
-json` output. Unclassified first-party embedded files, absent attribution,
provenance mismatches, symlinks and malformed inventories fail the build.
