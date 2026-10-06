# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0
# Test-only image: API trust/identity arrive via the projected ServiceAccount.
FROM scratch
COPY --chmod=0555 october-manager /october-manager
USER 65532:65532
ENTRYPOINT ["/october-manager"]
