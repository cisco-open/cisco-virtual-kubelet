# Cisco IOS-XE generated schema attribution

Generated IOS-XE schema data originates from Cisco YANG models. The upstream
directory's Cisco API License, Version 1.1 is reproduced byte-for-byte in
`LICENSE.md` from:

https://github.com/YangModels/yang/blob/647250f1c8f59aaf1ecdcf1d908fde96c036b1ba/vendor/cisco/xe/LICENSE.md

SHA-256: `a65d05690c6d4b550ba902884a0091477d77027cd4fe8da30def51da133b08bf`.

The linked `internal/drivers/iosxe/configdriver/generated/<release>/` packages
contain compressed schema data in their Go source. Their corresponding
`internal/drivers/iosxe/configdriver/schema/yang/<release>/.provenance.yaml`
records the exact upstream commit and release directory. The pinned upstream
YANG sources, including per-module copyright and license notices, are available
at `https://github.com/YangModels/yang/tree/<commit>/vendor/cisco/xe/<upstreamRelease>`
using those recorded values. The image includes the exact linked generated
source and provenance files, rather than silently classifying them as Apache-2.0.

`internal/drivers/iosxe/models.go` separately contains the generated App-Hosting
schema. Its header identifies the committed `tests/yang/` input snapshot; the
image includes those exact `.yang` files with their copyright and license
descriptions. No unrecorded upstream commit is asserted for that local snapshot.
Cisco modules retain Cisco's notices; imported IETF modules retain the terms in
their own source. This notice does not replace those per-module terms.

CVK's hand-maintained family/version indexes and generation tools use the project
license. The ygot/goyang runtime dependencies have their own redistribution texts
in the normal Go dependency license bundle.
