// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package drivers

import (
	"context"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"io"
)

// Resolve the current transport for every operation, including credential
// rotations. Optional SWIM capabilities do not change the base Backend contract.
func (d *dynamicSoftwareLifecycle) ObserveSWIMFlash(ctx context.Context, request softwarelifecycle.SWIMFlashRequest) (softwarelifecycle.SWIMFlashSnapshot, error) {
	backend, err := d.backend()
	if err != nil {
		return softwarelifecycle.SWIMFlashSnapshot{}, err
	}
	observer, ok := backend.(softwarelifecycle.SWIMPreparationObserver)
	if !ok {
		return softwarelifecycle.SWIMFlashSnapshot{}, softwarelifecycle.ErrUnsupported
	}
	return observer.ObserveSWIMFlash(ctx, request)
}
func (d *dynamicSoftwareLifecycle) retiredArchiveAccess() (softwarelifecycle.RetiredArchiveAccess, error) {
	backend, err := d.backend()
	if err != nil {
		return nil, err
	}
	access, ok := backend.(softwarelifecycle.RetiredArchiveAccess)
	if !ok {
		return nil, softwarelifecycle.ErrUnsupported
	}
	return access, nil
}
func (d *dynamicSoftwareLifecycle) ReadRetiredArchive(ctx context.Context, path string, size int64, w io.Writer) error {
	access, err := d.retiredArchiveAccess()
	if err != nil {
		return err
	}
	return access.ReadRetiredArchive(ctx, path, size, w)
}
func (d *dynamicSoftwareLifecycle) RemoveRetiredArchive(ctx context.Context, path string) error {
	access, err := d.retiredArchiveAccess()
	if err != nil {
		return err
	}
	return access.RemoveRetiredArchive(ctx, path)
}
func (d *dynamicSoftwareLifecycle) ObserveSWIMBootManifest(ctx context.Context) (string, error) {
	access, err := d.retiredArchiveAccess()
	if err != nil {
		return "", err
	}
	return access.ObserveSWIMBootManifest(ctx)
}
