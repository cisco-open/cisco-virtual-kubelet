// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"fmt"
	"io"

	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// VerifiedPackageChecksums binds a device checksum to the SHA256-addressed
// signed artifact. SHA512 is calculated from the SHA256-validated local archive;
// it is never accepted from an untrusted download response or device filename.
type VerifiedPackageChecksums struct {
	Package appartifact.Package
	SHA512  string
}

type VerifiedPackageDelivery interface {
	Stage(context.Context, string, string, io.Reader) error
	Verify(context.Context, string, VerifiedPackageChecksums) error
}

type copyVerifiedDelivery struct {
	driver   *XEDriver
	checksum transport.AppPackageChecksumConfig
}

// CopyVerifiedDelivery reuses the existing RESTCONF copy primitive. It does not
// invoke Standard's destructive fallback or cached-install shortcut. Native
// checksum queries use pinned SSH CLI; no SCP transport/service is introduced.
func (d *XEDriver) CopyVerifiedDelivery(pin string) VerifiedPackageDelivery {
	return &copyVerifiedDelivery{driver: d, checksum: transport.AppPackageChecksumConfig{Address: d.config.Address, Username: d.config.Username, Password: d.config.Password, HostKeySHA256: pin}}
}
func (c *copyVerifiedDelivery) Stage(ctx context.Context, path, source string, _ io.Reader) error {
	if source == "" {
		return fmt.Errorf("missing verified package URL")
	}
	return c.driver.copyRPC(ctx, source, path)
}
func (c *copyVerifiedDelivery) Verify(ctx context.Context, path string, p VerifiedPackageChecksums) error {
	return transport.VerifyAppPackageFile(ctx, c.checksum, path, p.Package.SizeBytes, p.SHA512)
}
