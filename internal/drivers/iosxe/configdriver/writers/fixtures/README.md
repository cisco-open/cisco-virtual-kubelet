# Writer fixtures

Per-release golden-file harness for the version-conditional writers.
Each leaf directory describes one Diff scenario:

    fixtures/<release-tag>/<family>/<case-name>/
        desired.yaml          netascode-shaped desired body
        observed.json         device-shaped observed body (the YANG wire body
                              after the Fetch path's reverse-mapping has run)
        expected_ops.json     the transport.Op slice writer.Diff should emit

`<release-tag>` matches the tag in
`internal/drivers/iosxe/configdriver/schema/yang-versions.yaml`
(e.g. `1716` for IOS-XE 17.16).

The harness in `fixture_test.go` walks the tree, picks the device
version from the release tag via `ReleaseTagForDeviceVersion` in
reverse, sets it on the writer registry, and asserts byte-equivalent
ops. A failing fixture is the canonical signal that either a writer
or an override-table entry changed shape unexpectedly.

Adding a new case is a no-code change: drop three files in a new
`<case-name>/` directory under the appropriate release/family and
the test picks it up automatically.

## YANG schema validation

After asserting op equality, the harness calls `ValidateBody` on each
generated per-family schema package (when one exists) to check that the
op bodies are structurally consistent with the YANG schema.

**Default (lenient) mode** — CI passes even when fixture data contains
deprecated field names or fields from YANG augments outside the module
closure. Unknown fields and type mismatches are silently skipped.

**Strict mode** — set `CVK_SCHEMA_VALIDATION=strict` to turn these
into hard errors, which surfaces discrepancies for investigation:

```bash
CVK_SCHEMA_VALIDATION=strict \
  go test ./internal/drivers/iosxe/configdriver/writers/... -run TestFixtures
```

Strict-mode failures are not CI-blocking by design; they identify
fixtures whose payloads diverge from the YANG model. Fix them by
updating the fixture or expanding the family's `yang_paths` to include
the augmenting module prefix. See `generated/SKIPLIST.md` and
`docs/yang-version-support.md` for the current status.

