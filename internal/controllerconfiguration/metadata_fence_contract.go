// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

// Metadata fences reuse the canonical bootstrap serial key. A marker cannot
// convert old unmarked evidence; admission permits only creation or manager
// release of an already marked object.
const MetadataFenceProtocol = "cvk-metadata-fence.v1"
const metadataFenceLabel = "ops.cisco.vk/metadata-fence"
const metadataFenceReleaseAnnotation = "ops.cisco.vk/metadata-fence-release"
