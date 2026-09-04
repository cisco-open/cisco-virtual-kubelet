// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gnoi

import (
	"context"
	"errors"
	"fmt"
	"io"

	ospb "github.com/openconfig/gnoi/os"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const iosXEDeviceNotProvisionedMessage = "Device has not been provisioned"

// ErrDeviceNotProvisioned identifies the IOS XE FailedPrecondition response
// returned while gNXI is in Default/Encrypted state and only the gNOI
// Certificate service is available.
type ErrDeviceNotProvisioned struct {
	Cause error
}

func (e *ErrDeviceNotProvisioned) Error() string {
	if e == nil || e.Cause == nil {
		return "IOS XE gNOI device has not been provisioned"
	}
	return "IOS XE gNOI device has not been provisioned: " + e.Cause.Error()
}

func (e *ErrDeviceNotProvisioned) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsDeviceNotProvisioned reports whether err is the exact IOS XE
// FailedPrecondition response (or its typed wrapper) indicating that gNXI
// certificate bootstrap has not completed. Other FailedPrecondition errors are
// intentionally not classified as provisioning failures.
func IsDeviceNotProvisioned(err error) bool {
	var target *ErrDeviceNotProvisioned
	if errors.As(err, &target) {
		return true
	}
	return isIOSXEDeviceNotProvisioned(err)
}

func isIOSXEDeviceNotProvisioned(err error) bool {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return false
	}
	// IOS XE releases have emitted the same sentence both with and without
	// terminal punctuation. Accept only those two exact spellings.
	return st.Message() == iosXEDeviceNotProvisionedMessage || st.Message() == iosXEDeviceNotProvisionedMessage+"."
}

// OSVerifyResult mirrors gNOI OS.Verify.
type OSVerifyResult struct {
	// Version is the currently-running OS version reported by the
	// device. For IOS-XE this is the SPA bundle version (e.g.
	// "17.15.01a").
	Version string

	// ActivationFailMessage carries the device's explanation when the
	// last activate did not yield the requested version. IOS-XE has
	// historically returned this as an empty string even on failure,
	// so callers cross-check via gNMI Get on
	// Cisco-IOS-XE-native:native/version.
	ActivationFailMessage string

	// IndividualSupervisorInstall, when true, signals that each
	// supervisor on a dual-RP device requires its own Install.
	IndividualSupervisorInstall bool
}

// Verify returns the running OS version. Read-only — used as the OS
// service capability probe.
func (c *Client) Verify(ctx context.Context) (*OSVerifyResult, error) {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return nil, err
	}
	resp, err := c.os.Verify(c.authCtx(ctx), &ospb.VerifyRequest{})
	c.cap.Observe(ServiceOS, err)
	if err != nil {
		if isIOSXEDeviceNotProvisioned(err) {
			if c.onDeviceNotProvisioned != nil {
				if provisioningErr := c.onDeviceNotProvisioned(ctx, c); provisioningErr != nil {
					return nil, fmt.Errorf("gnoi OS.Verify: %w", provisioningErr)
				}
			}
			return nil, fmt.Errorf("gnoi OS.Verify: %w", &ErrDeviceNotProvisioned{Cause: err})
		}
		return nil, fmt.Errorf("gnoi OS.Verify: %w", err)
	}
	if c.onOSVerifySuccess != nil {
		c.onOSVerifySuccess()
	}
	return &OSVerifyResult{
		Version:                     resp.Version,
		ActivationFailMessage:       resp.ActivationFailMessage,
		IndividualSupervisorInstall: resp.IndividualSupervisorInstall,
	}, nil
}

// InstallProgress is one event emitted by the Install stream.
//
// One and only one of {TransferReady, TransferProgress, SyncProgress,
// Validated, Err} is non-zero per event.
type InstallProgress struct {
	// TransferReady is set on the first device-side message acknowledging
	// the TransferRequest. Callers begin streaming bytes when this fires.
	TransferReady bool

	// TransferProgress reports cumulative bytes received by the device.
	TransferProgress *InstallTransferProgress

	// SyncProgress (dual-supervisor only) reports percentage of inter-
	// supervisor sync. Irrelevant on single-RP platforms.
	SyncProgress *InstallSyncProgress

	// Validated fires once at the end on a successful install.
	Validated *InstallValidated

	// Err carries device-side InstallError values translated into a
	// typed error.
	Err error
}

// InstallTransferProgress mirrors the device's TransferProgress.
type InstallTransferProgress struct {
	BytesReceived uint64
}

// InstallSyncProgress mirrors SyncProgress.
type InstallSyncProgress struct {
	PercentageTransferred uint32
}

// InstallValidated mirrors Validated.
type InstallValidated struct {
	Version     string
	Description string
}

// InstallErrorType mirrors the device-side InstallError.Type enum.
type InstallErrorType string

const (
	InstallErrorUnspecified         InstallErrorType = "UNSPECIFIED"
	InstallErrorIncompatible        InstallErrorType = "INCOMPATIBLE"
	InstallErrorTooLarge            InstallErrorType = "TOO_LARGE"
	InstallErrorParseFail           InstallErrorType = "PARSE_FAIL"
	InstallErrorIntegrityFail       InstallErrorType = "INTEGRITY_FAIL"
	InstallErrorInstallRunPackage   InstallErrorType = "INSTALL_RUN_PACKAGE"
	InstallErrorInstallInProgress   InstallErrorType = "INSTALL_IN_PROGRESS"
	InstallErrorUnexpectedSwitchovr InstallErrorType = "UNEXPECTED_SWITCHOVER"
	InstallErrorSyncFail            InstallErrorType = "SYNC_FAIL"
	InstallErrorNotSupportedBackup  InstallErrorType = "NOT_SUPPORTED_ON_BACKUP"
)

// InstallError wraps a device-side InstallError so reconcilers can
// classify failures (e.g. INTEGRITY_FAIL → terminal; INSTALL_IN_PROGRESS
// → retry after backoff).
type InstallError struct {
	Type   InstallErrorType
	Detail string
}

func (e *InstallError) Error() string {
	return fmt.Sprintf("gnoi OS.Install error %s: %s", e.Type, e.Detail)
}

// InstallOpts carries the inputs for the streaming OS.Install RPC.
type InstallOpts struct {
	// Version is the target package version. Empty forces transfer
	// regardless of what the device already has staged.
	Version string

	// PackageSize is the total byte count of the image. Optional but
	// strongly recommended so the device can pre-allocate flash space.
	PackageSize uint64

	// StandbySupervisor targets the standby RP on dual-RP platforms.
	StandbySupervisor bool

	// ChunkSize bounds each TransferContent message. Zero defaults to
	// 64 KiB. Cap is enforced by gRPC's per-message limit (4 MiB
	// default) — values larger than 1 MiB are rejected.
	ChunkSize int
}

// Install runs the gNOI OS.Install bidi stream, copying image bytes
// from r to the device and surfacing per-event progress through the
// returned channel. The channel closes when the stream terminates
// (success → final InstallProgress carrying Validated, failure →
// final event carrying Err).
//
// The caller is responsible for re-entrant cancellation via ctx; on
// ctx cancel the stream is closed and any in-flight bytes are
// discarded by the device.
//
// Install runs on the bulk-transfer conn (Options.BulkConn) so it
// cannot HOL-block control RPCs.
func (c *Client) Install(ctx context.Context, r io.Reader, opts InstallOpts) (<-chan InstallProgress, error) {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return nil, err
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = 64 * 1024
	}
	if opts.ChunkSize > 1024*1024 {
		return nil, fmt.Errorf("gnoi OS.Install: ChunkSize=%d exceeds 1 MiB cap", opts.ChunkSize)
	}
	osClient, releaseBulk, err := c.bulkOSClient(ctx)
	if err != nil {
		c.cap.Observe(ServiceOS, err)
		return nil, fmt.Errorf("gnoi OS.Install bulk lease: %w", err)
	}
	stream, err := osClient.Install(c.authCtx(ctx))
	if err != nil {
		releaseBulk()
		c.cap.Observe(ServiceOS, err)
		return nil, fmt.Errorf("gnoi OS.Install open: %w", err)
	}

	// Send the TransferRequest header first.
	if err := stream.Send(&ospb.InstallRequest{
		Request: &ospb.InstallRequest_TransferRequest{
			TransferRequest: &ospb.TransferRequest{
				Version:           opts.Version,
				PackageSize:       opts.PackageSize,
				StandbySupervisor: opts.StandbySupervisor,
			},
		},
	}); err != nil {
		_ = stream.CloseSend()
		releaseBulk()
		c.cap.Observe(ServiceOS, err)
		return nil, fmt.Errorf("gnoi OS.Install send TransferRequest: %w", err)
	}

	out := make(chan InstallProgress, 4)
	go func() {
		defer releaseBulk()
		c.pumpInstall(ctx, stream, r, opts, out)
	}()
	return out, nil
}

// pumpInstall is the long-running goroutine that drives the Install
// stream. It alternates between receiving device-side state messages
// and streaming bytes after TransferReady fires.
func (c *Client) pumpInstall(
	ctx context.Context,
	stream ospb.OS_InstallClient,
	r io.Reader,
	opts InstallOpts,
	out chan<- InstallProgress,
) {
	defer close(out)
	emitInstallErr := func(err error) {
		c.cap.Observe(ServiceOS, err)
		emitErr(out, ctx, err)
	}
	emitInstall := func(p InstallProgress) bool {
		if emit(out, ctx, p) {
			return true
		}
		if err := ctx.Err(); err != nil {
			c.cap.Observe(ServiceOS, err)
		}
		return false
	}
	emitValidated := func(v *InstallValidated) {
		if emitInstall(InstallProgress{Validated: v}) {
			c.cap.Observe(ServiceOS, nil)
		}
	}

	// First device response: TransferReady, or — if the device already
	// has the version staged — Validated directly.
	resp, err := stream.Recv()
	if err != nil {
		emitInstallErr(fmt.Errorf("gnoi OS.Install recv first: %w", err))
		return
	}
	switch r := resp.Response.(type) {
	case *ospb.InstallResponse_Validated:
		emitValidated(&InstallValidated{Version: r.Validated.Version, Description: r.Validated.Description})
		return
	case *ospb.InstallResponse_InstallError:
		emitInstallErr(&InstallError{Type: installErrorTypeFromProto(r.InstallError.Type), Detail: r.InstallError.Detail})
		return
	case *ospb.InstallResponse_TransferReady:
		// proceed to stream bytes
	default:
		emitInstallErr(fmt.Errorf("gnoi OS.Install: unexpected first response %T", r))
		return
	}
	if !emitInstall(InstallProgress{TransferReady: true}) {
		return
	}

	// Concurrently: pump bytes upstream, recv device events downstream.
	doneSend := make(chan error, 1)
	go func() {
		buf := make([]byte, opts.ChunkSize)
		for {
			select {
			case <-ctx.Done():
				doneSend <- ctx.Err()
				return
			default:
			}
			n, rerr := r.Read(buf)
			if n > 0 {
				if serr := stream.Send(&ospb.InstallRequest{
					Request: &ospb.InstallRequest_TransferContent{TransferContent: append([]byte(nil), buf[:n]...)},
				}); serr != nil {
					doneSend <- fmt.Errorf("gnoi OS.Install send: %w", serr)
					return
				}
			}
			if errors.Is(rerr, io.EOF) {
				// Terminator.
				if serr := stream.Send(&ospb.InstallRequest{
					Request: &ospb.InstallRequest_TransferEnd{TransferEnd: &ospb.TransferEnd{}},
				}); serr != nil {
					doneSend <- fmt.Errorf("gnoi OS.Install send TransferEnd: %w", serr)
					return
				}
				// IOS XE waits for the client half-close before emitting
				// the terminal Validated response on some releases.
				if cerr := stream.CloseSend(); cerr != nil {
					doneSend <- fmt.Errorf("gnoi OS.Install close send: %w", cerr)
					return
				}
				doneSend <- nil
				return
			}
			if rerr != nil {
				doneSend <- fmt.Errorf("gnoi OS.Install read: %w", rerr)
				return
			}
		}
	}()

	// Recv loop until Validated or InstallError.
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// Wait for the sender to finish before treating as terminal.
			if serr := <-doneSend; serr != nil {
				emitInstallErr(serr)
			} else {
				emitInstallErr(errors.New("gnoi OS.Install: stream closed before Validated"))
			}
			return
		}
		if err != nil {
			_ = stream.CloseSend()
			emitInstallErr(fmt.Errorf("gnoi OS.Install recv: %w", err))
			return
		}
		switch r := resp.Response.(type) {
		case *ospb.InstallResponse_TransferProgress:
			if !emitInstall(InstallProgress{TransferProgress: &InstallTransferProgress{BytesReceived: r.TransferProgress.BytesReceived}}) {
				return
			}
		case *ospb.InstallResponse_SyncProgress:
			if !emitInstall(InstallProgress{SyncProgress: &InstallSyncProgress{PercentageTransferred: r.SyncProgress.PercentageTransferred}}) {
				return
			}
		case *ospb.InstallResponse_Validated:
			// drain sender before signalling success
			if serr := <-doneSend; serr != nil {
				emitInstallErr(serr)
				return
			}
			emitValidated(&InstallValidated{Version: r.Validated.Version, Description: r.Validated.Description})
			return
		case *ospb.InstallResponse_InstallError:
			_ = stream.CloseSend()
			emitInstallErr(&InstallError{Type: installErrorTypeFromProto(r.InstallError.Type), Detail: r.InstallError.Detail})
			return
		default:
			_ = stream.CloseSend()
			emitInstallErr(fmt.Errorf("gnoi OS.Install: unexpected response %T", r))
			return
		}
	}
}

func emit(out chan<- InstallProgress, ctx context.Context, p InstallProgress) bool {
	select {
	case out <- p:
		return true
	case <-ctx.Done():
		return false
	}
}

func emitErr(out chan<- InstallProgress, ctx context.Context, err error) {
	select {
	case out <- InstallProgress{Err: err}:
	case <-ctx.Done():
	}
}

func installErrorTypeFromProto(t ospb.InstallError_Type) InstallErrorType {
	switch t {
	case ospb.InstallError_INCOMPATIBLE:
		return InstallErrorIncompatible
	case ospb.InstallError_TOO_LARGE:
		return InstallErrorTooLarge
	case ospb.InstallError_PARSE_FAIL:
		return InstallErrorParseFail
	case ospb.InstallError_INTEGRITY_FAIL:
		return InstallErrorIntegrityFail
	case ospb.InstallError_INSTALL_RUN_PACKAGE:
		return InstallErrorInstallRunPackage
	case ospb.InstallError_INSTALL_IN_PROGRESS:
		return InstallErrorInstallInProgress
	case ospb.InstallError_UNEXPECTED_SWITCHOVER:
		return InstallErrorUnexpectedSwitchovr
	case ospb.InstallError_SYNC_FAIL:
		return InstallErrorSyncFail
	case ospb.InstallError_NOT_SUPPORTED_ON_BACKUP:
		return InstallErrorNotSupportedBackup
	}
	return InstallErrorUnspecified
}

// ActivateOpts carries inputs for OS.Activate.
type ActivateOpts struct {
	// Version is the target package version to activate. Required.
	Version string

	// StandbySupervisor targets the standby RP on dual-RP platforms.
	StandbySupervisor bool

	// NoReboot, when true, instructs the device to set the activate
	// bit but NOT reboot. The default — and the spec-defined behaviour —
	// is that Activate reboots the device itself.
	NoReboot bool
}

// ActivateErrorType mirrors the device-side ActivateError.Type enum.
type ActivateErrorType string

const (
	ActivateErrorUnspecified          ActivateErrorType = "UNSPECIFIED"
	ActivateErrorNonExistentVersion   ActivateErrorType = "NON_EXISTENT_VERSION"
	ActivateErrorNotSupportedOnBackup ActivateErrorType = "NOT_SUPPORTED_ON_BACKUP"
)

// ActivateError wraps a device-side ActivateError so reconcilers can
// classify failures (NON_EXISTENT_VERSION → retry alternate version
// spelling; NOT_SUPPORTED_ON_BACKUP → operator config issue).
type ActivateError struct {
	Type   ActivateErrorType
	Detail string
}

func (e *ActivateError) Error() string {
	return fmt.Sprintf("gnoi OS.Activate error %s: %s", e.Type, e.Detail)
}

// Activate sets the target version as the next-boot image and (unless
// NoReboot is set) reboots the device. Per the gNOI OS spec the
// device returns ActivateResponse before the reboot occurs; callers
// must subsequently poll System.RebootStatus or simply re-establish
// reachability before issuing Verify.
func (c *Client) Activate(ctx context.Context, opts ActivateOpts) error {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return err
	}
	if opts.Version == "" {
		return errors.New("gnoi OS.Activate: Version is required")
	}
	resp, err := c.os.Activate(c.authCtx(ctx), &ospb.ActivateRequest{
		Version:           opts.Version,
		StandbySupervisor: opts.StandbySupervisor,
		NoReboot:          opts.NoReboot,
	})
	c.cap.Observe(ServiceOS, err)
	if err != nil {
		return fmt.Errorf("gnoi OS.Activate: %w", err)
	}
	switch r := resp.Response.(type) {
	case *ospb.ActivateResponse_ActivateOk:
		return nil
	case *ospb.ActivateResponse_ActivateError:
		return &ActivateError{Type: activateErrorTypeFromProto(r.ActivateError.Type), Detail: r.ActivateError.Detail}
	}
	return fmt.Errorf("gnoi OS.Activate: unexpected response %T", resp.Response)
}

func activateErrorTypeFromProto(t ospb.ActivateError_Type) ActivateErrorType {
	switch t {
	case ospb.ActivateError_NON_EXISTENT_VERSION:
		return ActivateErrorNonExistentVersion
	case ospb.ActivateError_NOT_SUPPORTED_ON_BACKUP:
		return ActivateErrorNotSupportedOnBackup
	default:
		return ActivateErrorUnspecified
	}
}
