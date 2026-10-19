// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package softwarelifecycle

import (
	"context"
	"io"
	"time"
)

// SWIMFlashSnapshot is the qualified single-RP XE observer. Only root archive
// candidates derived from retired receipts can be tested for eligibility.
type SWIMFlashSnapshot struct {
	// ArchiveOnlyVersions distinguishes auto-discovered IMG archives from installed packages.
	ArchiveOnlyVersions map[string]string
	FreeBytes           uint64
	Files               map[string]uint64
	NativeReferences    string
	EvidenceHash        string
}

// SWIMFlashRequest permits staged-target corroboration only after a successful
// controller distribution. It never authorizes retirement of that target.
type SWIMFlashRequest struct {
	RunningVersion        string
	StagedTargetVersion   string
	DistributionStartedAt time.Time
}
type SWIMPreparationObserver interface {
	ObserveSWIMFlash(context.Context, SWIMFlashRequest) (SWIMFlashSnapshot, error)
}

// RetiredArchiveAccess is an optional platform capability; it never determines
// eligibility or acquires authority. The device worker provides both.
type RetiredArchiveAccess interface {
	ReadRetiredArchive(context.Context, string, int64, io.Writer) error
	RemoveRetiredArchive(context.Context, string) error
	ObserveSWIMBootManifest(context.Context) (string, error)
}
