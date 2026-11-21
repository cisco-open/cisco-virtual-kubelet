// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package common

// APMaxPods bounds scheduling to the two IOx applications supported by the
// Catalyst AP driver. Each AP Pod contains exactly one application. The shared
// CiscoDevice CRD default (16) must not override this platform limit.
func APMaxPods(configured int32) int64 {
	if configured == 1 {
		return 1
	}
	return 2
}
