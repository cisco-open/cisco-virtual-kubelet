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
	"strings"

	ospb "github.com/openconfig/gnoi/os"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const iosXEDeviceNotProvisionedMessage = "Device has not been provisioned"

// IsDeviceNotProvisioned reports whether err is the exact IOS XE
// FailedPrecondition response indicating that gNXI certificate bootstrap has
// not completed. Other FailedPrecondition errors are intentionally not
// classified as provisioning failures.
func IsDeviceNotProvisioned(err error) bool {
	return isIOSXEDeviceNotProvisioned(err)
}

func isIOSXEDeviceNotProvisioned(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition {
		// IOS XE releases have emitted the same sentence both with and without
		// terminal punctuation. Accept only those two exact spellings.
		if st.Message() == iosXEDeviceNotProvisionedMessage || st.Message() == iosXEDeviceNotProvisionedMessage+"." {
			return true
		}
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range multi.Unwrap() {
			if isIOSXEDeviceNotProvisioned(nested) {
				return true
			}
		}
		return false
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return isIOSXEDeviceNotProvisioned(single.Unwrap())
	}
	return false
}

// StandbyState is the normalized availability state of a standby supervisor.
// It deliberately does not expose the gNOI protobuf enum so callers remain
// independent of the wire representation.
type StandbyState string

const (
	// StandbyStateNotReported means the device omitted standby information.
	StandbyStateNotReported StandbyState = "NOT_REPORTED"
	// StandbyStateUnspecified means the device explicitly returned the gNOI
	// UNSPECIFIED state or a malformed empty standby choice.
	StandbyStateUnspecified StandbyState = "UNSPECIFIED"
	// StandbyStateUnsupported means the target does not support dual supervisors.
	StandbyStateUnsupported StandbyState = "UNSUPPORTED"
	// StandbyStateNonExistent means dual supervisors are supported but no standby
	// supervisor is present.
	StandbyStateNonExistent StandbyState = "NON_EXISTENT"
	// StandbyStateUnavailable means a standby exists but is temporarily unavailable.
	StandbyStateUnavailable StandbyState = "UNAVAILABLE"
	// StandbyStateReady means the standby is present and returned verification data.
	StandbyStateReady StandbyState = "READY"
	// StandbyStateUnknown means the device returned a newer state unknown to this client.
	StandbyStateUnknown StandbyState = "UNKNOWN"
)

// StandbyVerifyResult is the normalized standby portion of gNOI OS.Verify.
// ID, Version, and ActivationFailMessage are populated only when State is
// StandbyStateReady.
type StandbyVerifyResult struct {
	State                 StandbyState
	ID                    string
	Version               string
	ActivationFailMessage string
}

// OSVerifyResult mirrors gNOI OS.Verify without exposing protobuf types.
type OSVerifyResult struct {
	// Version is the currently-running OS version reported by the
	// device. For IOS-XE this is the SPA bundle version (e.g.
	// "17.15.01a").
	Version string

	// ActivationFailMessage carries the device's explanation when the
	// last activate did not yield the requested version. IOS-XE has
	// historically returned this as an empty string even on failure, so
	// callers must also compare Version with the requested target.
	ActivationFailMessage string

	// IndividualSupervisorInstall, when true, signals that each
	// supervisor on a dual-RP device requires its own Install.
	IndividualSupervisorInstall bool

	// Standby describes standby-supervisor availability and, when ready,
	// its running version and last activation outcome.
	Standby StandbyVerifyResult
}

// Verify returns the running OS version. Read-only — used as the OS
// service capability probe.
func (c *Client) Verify(ctx context.Context) (*OSVerifyResult, error) {
	return c.verify(ctx)
}

func (c *Client) verify(ctx context.Context, opts ...grpc.CallOption) (*OSVerifyResult, error) {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return nil, err
	}
	resp, err := c.os.Verify(c.authCtx(ctx), &ospb.VerifyRequest{}, opts...)
	c.cap.Observe(ServiceOS, err)
	if err != nil {
		return nil, fmt.Errorf("gnoi OS.Verify: %w", err)
	}
	if resp == nil {
		return nil, errors.New("gnoi OS.Verify: empty response")
	}
	return &OSVerifyResult{
		Version:                     resp.Version,
		ActivationFailMessage:       resp.ActivationFailMessage,
		IndividualSupervisorInstall: resp.IndividualSupervisorInstall,
		Standby:                     standbyVerifyResultFromProto(resp.VerifyStandby),
	}, nil
}

func standbyVerifyResultFromProto(standby *ospb.VerifyStandby) StandbyVerifyResult {
	if standby == nil {
		return StandbyVerifyResult{State: StandbyStateNotReported}
	}

	switch state := standby.State.(type) {
	case *ospb.VerifyStandby_StandbyState:
		return StandbyVerifyResult{State: standbyStateFromProto(state.StandbyState)}
	case *ospb.VerifyStandby_VerifyResponse:
		if state.VerifyResponse == nil {
			return StandbyVerifyResult{State: StandbyStateUnspecified}
		}
		return StandbyVerifyResult{
			State:                 StandbyStateReady,
			ID:                    state.VerifyResponse.Id,
			Version:               state.VerifyResponse.Version,
			ActivationFailMessage: state.VerifyResponse.ActivationFailMessage,
		}
	default:
		return StandbyVerifyResult{State: StandbyStateUnspecified}
	}
}

func standbyStateFromProto(state *ospb.StandbyState) StandbyState {
	if state == nil {
		return StandbyStateUnspecified
	}

	switch state.State {
	case ospb.StandbyState_UNSPECIFIED:
		return StandbyStateUnspecified
	case ospb.StandbyState_UNSUPPORTED:
		return StandbyStateUnsupported
	case ospb.StandbyState_NON_EXISTENT:
		return StandbyStateNonExistent
	case ospb.StandbyState_UNAVAILABLE:
		return StandbyStateUnavailable
	default:
		return StandbyStateUnknown
	}
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
// → observe the existing operation without replaying the request).
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
// The caller is responsible for re-entrant cancellation via ctx. Cancellation
// closes the client stream, but the device-side outcome remains indeterminate;
// callers must inspect/reconcile it without assuming uploaded bytes were
// discarded or replaying the request.
//
// Install runs on the bulk-transfer conn (Options.BulkConn) so it
// cannot HOL-block control RPCs.
func (c *Client) Install(ctx context.Context, r io.Reader, opts InstallOpts) (<-chan InstallProgress, error) {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("gnoi OS.Install: image reader is required")
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = 64 * 1024
	}
	if opts.ChunkSize < 0 {
		return nil, fmt.Errorf("gnoi OS.Install: ChunkSize=%d must be positive", opts.ChunkSize)
	}
	if opts.ChunkSize > 1024*1024 {
		return nil, fmt.Errorf("gnoi OS.Install: ChunkSize=%d exceeds 1 MiB cap", opts.ChunkSize)
	}
	osClient, releaseBulk, err := c.bulkOSClient(ctx)
	if err != nil {
		c.cap.Observe(ServiceOS, err)
		return nil, fmt.Errorf("gnoi OS.Install bulk lease: %w", err)
	}
	streamCtx, cancelStream := context.WithCancel(c.authCtx(ctx))
	stream, err := osClient.Install(streamCtx)
	if err != nil {
		cancelStream()
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
		cancelStream()
		_ = stream.CloseSend()
		releaseBulk()
		c.cap.Observe(ServiceOS, err)
		return nil, fmt.Errorf("gnoi OS.Install send TransferRequest: %w", err)
	}

	out := make(chan InstallProgress, 4)
	go func() {
		defer releaseBulk()
		defer cancelStream()
		c.pumpInstall(ctx, cancelStream, stream, r, opts, out)
	}()
	return out, nil
}

// pumpInstall is the long-running goroutine that drives the Install
// stream. It alternates between receiving device-side state messages
// and streaming bytes after TransferReady fires.
func (c *Client) pumpInstall(
	ctx context.Context,
	cancelStream context.CancelFunc,
	stream ospb.OS_InstallClient,
	r io.Reader,
	opts InstallOpts,
	out chan<- InstallProgress,
) {
	defer close(out)
	var doneSend <-chan error
	waitSender := func() error {
		if doneSend == nil {
			return nil
		}
		err := <-doneSend
		doneSend = nil
		return err
	}
	stopSender := func() error {
		if doneSend == nil {
			return nil
		}
		cancelStream()
		return waitSender()
	}
	// SendMsg and CloseSend may not run concurrently on a gRPC client stream.
	// Cancel the RPC first so a blocked SendMsg wakes, join the sole sender,
	// and only then half-close before the bulk connection lease can unwind.
	defer func() {
		_ = stopSender()
		_ = stream.CloseSend()
	}()
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
	emitValidated := func(v *ospb.Validated) {
		if v == nil || strings.TrimSpace(v.Version) == "" {
			emitInstallErr(errors.New("gnoi OS.Install: Validated response has empty version"))
			return
		}
		if emitInstall(InstallProgress{Validated: &InstallValidated{Version: v.Version, Description: v.Description}}) {
			c.cap.Observe(ServiceOS, nil)
		}
	}

	// The first response is one of three successful states: TransferReady
	// requests bytes, SyncProgress means a peer supervisor is supplying the
	// image, and Validated means the target already has it.
	resp, err := stream.Recv()
	if err != nil {
		emitInstallErr(fmt.Errorf("gnoi OS.Install recv first: %w", err))
		return
	}
	if resp == nil {
		emitInstallErr(errors.New("gnoi OS.Install: empty first response"))
		return
	}

	switch response := resp.Response.(type) {
	case *ospb.InstallResponse_Validated:
		emitValidated(response.Validated)
		return
	case *ospb.InstallResponse_InstallError:
		if response.InstallError == nil {
			emitInstallErr(errors.New("gnoi OS.Install: empty InstallError response"))
			return
		}
		emitInstallErr(&InstallError{Type: installErrorTypeFromProto(response.InstallError.Type), Detail: response.InstallError.Detail})
		return
	case *ospb.InstallResponse_TransferReady:
		if response.TransferReady == nil {
			emitInstallErr(errors.New("gnoi OS.Install: empty TransferReady response"))
			return
		}
		if !emitInstall(InstallProgress{TransferReady: true}) {
			return
		}
		doneSend = sendInstallContent(stream.Context(), stream, r, opts.ChunkSize)
	case *ospb.InstallResponse_SyncProgress:
		if response.SyncProgress == nil {
			emitInstallErr(errors.New("gnoi OS.Install: empty SyncProgress response"))
			return
		}
		if !emitInstall(InstallProgress{SyncProgress: &InstallSyncProgress{PercentageTransferred: response.SyncProgress.PercentageTransferred}}) {
			return
		}
		// Scenario 4 in the gNOI OS protocol: the target copies the image
		// from its peer supervisor, so the client has no content to send.
		if err := stream.CloseSend(); err != nil {
			emitInstallErr(fmt.Errorf("gnoi OS.Install close send during supervisor sync: %w", err))
			return
		}
	default:
		emitInstallErr(fmt.Errorf("gnoi OS.Install: unexpected first response %T", response))
		return
	}

	// Recv loop until Validated or InstallError.
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// EOF is terminal even if the peer closed while content was still
			// being produced, so stop and join the sender before reporting it.
			if serr := stopSender(); serr != nil && !errors.Is(serr, context.Canceled) {
				emitInstallErr(serr)
				return
			}
			emitInstallErr(errors.New("gnoi OS.Install: stream closed before Validated"))
			return
		}
		if err != nil {
			emitInstallErr(fmt.Errorf("gnoi OS.Install recv: %w", err))
			return
		}
		if resp == nil {
			emitInstallErr(errors.New("gnoi OS.Install: empty response"))
			return
		}
		switch response := resp.Response.(type) {
		case *ospb.InstallResponse_TransferProgress:
			if doneSend == nil {
				emitInstallErr(errors.New("gnoi OS.Install: unexpected TransferProgress during supervisor sync"))
				return
			}
			if response.TransferProgress == nil {
				emitInstallErr(errors.New("gnoi OS.Install: empty TransferProgress response"))
				return
			}
			if !emitInstall(InstallProgress{TransferProgress: &InstallTransferProgress{BytesReceived: response.TransferProgress.BytesReceived}}) {
				return
			}
		case *ospb.InstallResponse_SyncProgress:
			if response.SyncProgress == nil {
				emitInstallErr(errors.New("gnoi OS.Install: empty SyncProgress response"))
				return
			}
			if !emitInstall(InstallProgress{SyncProgress: &InstallSyncProgress{PercentageTransferred: response.SyncProgress.PercentageTransferred}}) {
				return
			}
		case *ospb.InstallResponse_Validated:
			// Drain the sender before signalling transfer success.
			if serr := waitSender(); serr != nil {
				emitInstallErr(serr)
				return
			}
			emitValidated(response.Validated)
			return
		case *ospb.InstallResponse_InstallError:
			if response.InstallError == nil {
				emitInstallErr(errors.New("gnoi OS.Install: empty InstallError response"))
				return
			}
			emitInstallErr(&InstallError{Type: installErrorTypeFromProto(response.InstallError.Type), Detail: response.InstallError.Detail})
			return
		default:
			emitInstallErr(fmt.Errorf("gnoi OS.Install: unexpected response %T", response))
			return
		}
	}
}

func sendInstallContent(
	ctx context.Context,
	stream ospb.OS_InstallClient,
	r io.Reader,
	chunkSize int,
) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, chunkSize)
		for {
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			default:
			}
			n, readErr := r.Read(buf)
			if err := ctx.Err(); err != nil {
				done <- err
				return
			}
			if n > 0 {
				if err := stream.Send(&ospb.InstallRequest{
					Request: &ospb.InstallRequest_TransferContent{TransferContent: append([]byte(nil), buf[:n]...)},
				}); err != nil {
					done <- fmt.Errorf("gnoi OS.Install send: %w", err)
					return
				}
			}
			if errors.Is(readErr, io.EOF) {
				if err := stream.Send(&ospb.InstallRequest{
					Request: &ospb.InstallRequest_TransferEnd{TransferEnd: &ospb.TransferEnd{}},
				}); err != nil {
					done <- fmt.Errorf("gnoi OS.Install send TransferEnd: %w", err)
					return
				}
				// IOS XE waits for the client half-close before emitting the
				// terminal Validated response on some releases.
				if err := stream.CloseSend(); err != nil {
					done <- fmt.Errorf("gnoi OS.Install close send: %w", err)
					return
				}
				done <- nil
				return
			}
			if readErr != nil {
				done <- fmt.Errorf("gnoi OS.Install read: %w", readErr)
				return
			}
		}
	}()
	return done
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

// ActivateError wraps a device-side ActivateError so reconcilers can classify
// failures without depending on protobuf types.
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
	if resp == nil {
		return errors.New("gnoi OS.Activate: empty response")
	}
	switch r := resp.Response.(type) {
	case *ospb.ActivateResponse_ActivateOk:
		if r.ActivateOk == nil {
			return errors.New("gnoi OS.Activate: empty ActivateOK response")
		}
		return nil
	case *ospb.ActivateResponse_ActivateError:
		if r.ActivateError == nil {
			return errors.New("gnoi OS.Activate: empty ActivateError response")
		}
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
