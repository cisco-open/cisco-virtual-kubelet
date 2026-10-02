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

package softwarelifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

const testOperationID = "123e4567-e89b-42d3-a456-426614174000"

type fakeTransport struct {
	kind      configtransport.Kind
	responses map[string][]byte
	fetchErr  error
}

func (f *fakeTransport) Capabilities() configtransport.Capabilities {
	return configtransport.Capabilities{Kind: f.kind}
}

func (f *fakeTransport) Fetch(_ context.Context, path string) ([]byte, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.responses[path], nil
}

func (*fakeTransport) StartTransaction(context.Context) (configtransport.TxHandle, error) {
	return "", configtransport.ErrUnsupported
}
func (*fakeTransport) Mutate(context.Context, configtransport.TxHandle, []configtransport.Op) error {
	return configtransport.ErrUnsupported
}
func (*fakeTransport) Commit(context.Context, configtransport.TxHandle) error {
	return configtransport.ErrUnsupported
}
func (*fakeTransport) Discard(context.Context, configtransport.TxHandle) error {
	return configtransport.ErrUnsupported
}
func (*fakeTransport) SaveStartup(context.Context) error { return configtransport.ErrUnsupported }
func (*fakeTransport) Close() error                      { return nil }

type rpcTransport struct {
	*fakeTransport
	rpcPath    string
	rpcPayload []byte
	rpcErr     error
}

type timedTransport struct {
	*fakeTransport
	deviceObservedAt time.Time
	localObservedAt  time.Time
}

func (f *timedTransport) FetchWithDeviceTime(_ context.Context, path string) ([]byte, time.Time, time.Time, error) {
	body, err := f.Fetch(context.Background(), path)
	return body, f.deviceObservedAt, f.localObservedAt, err
}

func (f *rpcTransport) InvokeRPC(_ context.Context, path string, payload []byte) ([]byte, error) {
	f.rpcPath = path
	f.rpcPayload = append([]byte(nil), payload...)
	return nil, f.rpcErr
}

func TestNewRequiresRESTCONF(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, lifecycle.ErrUnsupported) {
		t.Fatalf("New(nil) error = %v", err)
	}
	if _, err := New(&fakeTransport{kind: configtransport.KindGNMI}); !errors.Is(err, lifecycle.ErrUnsupported) {
		t.Fatalf("New(gNMI) error = %v", err)
	}
	if _, err := New(&fakeTransport{kind: configtransport.KindRESTCONF}); err != nil {
		t.Fatalf("New(RESTCONF): %v", err)
	}
}

func TestInspectReturnsExactActivatableIdentity(t *testing.T) {
	raw := []byte(`{
	  "Cisco-IOS-XE-install-oper:install-location-information": [
	    {"install-version-info": [
	      {"version":"17.18.04.0.759", "version-extension":"1784396682", "current":"install-version-state-installed", "src-filename":"flash:cat9k_iosxe.17.18.04.SPA.bin"}
	    ]},
	    {"install-version-info": [
	      {"version":"17.18.04.0.759", "version-extension":"1784396682", "current":"install-version-state-installed", "src-filename":"flash:cat9k_iosxe.17.18.04.SPA.bin"}
	    ]}
	  ]
	}`)
	a, err := New(&fakeTransport{
		kind:      configtransport.KindRESTCONF,
		responses: map[string][]byte{installInventoryPath: raw},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	image, err := a.Inspect(context.Background(), "17.18.04")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if image.Version != "17.18.04.0.759.1784396682" {
		t.Errorf("Version = %q", image.Version)
	}
	if image.State != lifecycle.InventoryStateInstalled || !image.State.Activatable() {
		t.Errorf("State = %q, Activatable = %t", image.State, image.State.Activatable())
	}
	if image.SourcePath != "flash:cat9k_iosxe.17.18.04.SPA.bin" {
		t.Errorf("SourcePath = %q", image.SourcePath)
	}
}

func TestInspectMixedInstalledAndPresentFailsClosed(t *testing.T) {
	raw := []byte(`{
	  "Cisco-IOS-XE-install-oper:install-location-information": [
	    {"install-version-info": [
	      {"version":"17.18.04", "current":"install-version-state-installed"}
	    ]},
	    {"install-version-info": [
	      {"version":"17.18.04", "current":"install-version-state-present"}
	    ]}
	  ]
	}`)
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installInventoryPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	image, err := a.Inspect(context.Background(), "17.18.04")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if image.State != lifecycle.InventoryStateUnknown || image.State.Activatable() {
		t.Fatalf("State = %q, Activatable = %t; mixed installed/present must fail closed", image.State, image.State.Activatable())
	}
}

func TestInspectFailsClosedForNonActivatableStates(t *testing.T) {
	for native, want := range map[string]lifecycle.InventoryState{
		"install-version-state-present":                 lifecycle.InventoryStatePresent,
		"install-version-state-in-progress":             lifecycle.InventoryStateInProgress,
		"install-version-state-provisioned-uncommitted": lifecycle.InventoryStateProvisionedUncommitted,
		"install-version-state-provisioned-committed":   lifecycle.InventoryStateProvisionedCommitted,
		"install-version-state-invalid":                 lifecycle.InventoryStateInvalid,
		"install-version-state-unknown":                 lifecycle.InventoryStateUnknown,
	} {
		t.Run(native, func(t *testing.T) {
			raw := inventoryResponse([]map[string]string{{"version": "17.18.04", "current": native}})
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installInventoryPath: raw}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			image, err := a.Inspect(context.Background(), "17.18.04")
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if image.State != want || image.State.Activatable() {
				t.Errorf("State = %q, want non-activatable %q", image.State, want)
			}
		})
	}
}

func TestInspectRejectsAmbiguousPrefix(t *testing.T) {
	raw := inventoryResponse([]map[string]string{
		{"version": "17.18.04.0.759", "version-extension": "100", "current": "install-version-state-installed"},
		{"version": "17.18.04.0.760", "version-extension": "200", "current": "install-version-state-installed"},
	})
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installInventoryPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.Inspect(context.Background(), "17.18.04"); !errors.Is(err, lifecycle.ErrAmbiguousTarget) {
		t.Fatalf("Inspect error = %v, want ErrAmbiguousTarget", err)
	}
	image, err := a.Inspect(context.Background(), "17.18.04.0.760.200")
	if err != nil {
		t.Fatalf("Inspect exact: %v", err)
	}
	if image.Version != "17.18.04.0.760.200" {
		t.Errorf("exact Version = %q", image.Version)
	}
}

func TestInspectTreatsPartialMultiLocationRegistrationAsInProgress(t *testing.T) {
	raw := []byte(`{
	  "Cisco-IOS-XE-install-oper:install-location-information": [
	    {"install-version-info": [
	      {"version":"17.18.04", "current":"install-version-state-installed"}
	    ]},
	    {"install-version-info": [
	      {"version":"17.18.03", "current":"install-version-state-provisioned-committed"}
	    ]}
	  ]
	}`)
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installInventoryPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	image, err := a.Inspect(context.Background(), "17.18.04")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if image.State != lifecycle.InventoryStateInProgress || image.State.Activatable() {
		t.Errorf("State = %q, want non-activatable InProgress", image.State)
	}
}

func TestInspectReportsAbsentAndMalformedInventory(t *testing.T) {
	for name, test := range map[string]struct {
		raw      []byte
		sentinel error
	}{
		"absent":    {inventoryResponse(nil), lifecycle.ErrTargetNotFound},
		"malformed": {[]byte(`{"Cisco-IOS-XE-install-oper:install-location-information":{}}`), nil},
		"missing":   {[]byte(`{"other":[]}`), nil},
	} {
		t.Run(name, func(t *testing.T) {
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installInventoryPath: test.raw}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = a.Inspect(context.Background(), "17.18.04")
			if err == nil {
				t.Fatal("Inspect succeeded")
			}
			if test.sentinel != nil && !errors.Is(err, test.sentinel) {
				t.Fatalf("Inspect error = %v, want %v", err, test.sentinel)
			}
		})
	}
}

func TestObserveInterruptedInstallCorrelatesStrongNativeEvidence(t *testing.T) {
	started := time.Date(2026, 10, 1, 22, 34, 53, 0, time.UTC)
	raw := interruptedInstallResponse(
		"install-no-activity",
		"install-state-added",
		"install-package-verify-ok",
		"1247897709",
		"install-op-succ",
		"op-complete",
		started.Add(-90*time.Second),
	)
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := a.ObserveInterruptedInstall(context.Background(), lifecycle.InterruptedInstallRequest{
		TargetVersion: "17.18.02",
		SourceSize:    1247897709,
		NotBefore:     started,
		ObservedAt:    started.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ObserveInterruptedInstall: %v", err)
	}
	if got.Image.Version != "17.18.02.0.4112.1766116039" || !got.Image.State.Activatable() || got.CompletedAt.IsZero() {
		t.Fatalf("observation = %+v", got)
	}
}

func TestObserveInterruptedInstallUsesDeviceClockInterval(t *testing.T) {
	localStarted := time.Date(2026, 10, 2, 2, 10, 53, 0, time.UTC)
	localObserved := localStarted.Add(6 * time.Minute)
	deviceStarted := localStarted.Add(-5*time.Minute - 9*time.Second)
	deviceObserved := localObserved.Add(-5*time.Minute - 9*time.Second)
	raw := interruptedInstallResponse(
		"install-no-activity",
		"install-state-added",
		"install-package-verify-ok",
		"1247897709",
		"install-op-succ",
		"op-complete",
		deviceStarted,
	)
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := lifecycle.InterruptedInstallRequest{
		TargetVersion: "17.18.02",
		SourceSize:    1247897709,
		NotBefore:     localStarted,
		ObservedAt:    localObserved,
	}
	if _, err := a.ObserveInterruptedInstall(context.Background(), request); err == nil {
		t.Fatal("local-clock fallback unexpectedly accepted operation outside its bounded skew")
	}
	request.DeviceNotBefore = deviceStarted
	request.DeviceObservedAt = deviceObserved
	got, err := a.ObserveInterruptedInstall(context.Background(), request)
	if err != nil {
		t.Fatalf("ObserveInterruptedInstall with device clock: %v", err)
	}
	if got.Image.Version != "17.18.02.0.4112.1766116039" || !got.Image.State.Activatable() {
		t.Fatalf("observation = %+v", got)
	}
}

func TestObserveInterruptedInstallDerivesDeviceClockFromRESTCONFResponse(t *testing.T) {
	localStarted := time.Date(2026, 10, 2, 2, 10, 53, 0, time.UTC)
	localObserved := localStarted.Add(18 * time.Minute)
	deviceOffset := -5*time.Minute - 32*time.Second
	deviceStarted := localStarted.Add(deviceOffset)
	raw := interruptedInstallResponse(
		"install-no-activity",
		"install-state-added",
		"install-package-verify-ok",
		"1247897709",
		"install-op-succ",
		"op-complete",
		deviceStarted,
	)
	transport := &timedTransport{
		fakeTransport:    &fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}},
		deviceObservedAt: localObserved.Add(deviceOffset),
		localObservedAt:  localObserved,
	}
	a, err := New(transport)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := a.ObserveInterruptedInstall(context.Background(), lifecycle.InterruptedInstallRequest{
		TargetVersion: "17.18.02",
		SourceSize:    1247897709,
		NotBefore:     localStarted,
		ObservedAt:    localObserved,
	})
	if err != nil {
		t.Fatalf("ObserveInterruptedInstall with RESTCONF clock: %v", err)
	}
	if got.Image.Version != "17.18.02.0.4112.1766116039" || !got.Image.State.Activatable() {
		t.Fatalf("observation = %+v", got)
	}
}

func TestObserveInterruptedInstallRejectsPartialDeviceClockInterval(t *testing.T) {
	started := time.Date(2026, 10, 2, 2, 10, 53, 0, time.UTC)
	raw := interruptedInstallResponse(
		"install-no-activity",
		"install-state-added",
		"install-package-verify-ok",
		"1247897709",
		"install-op-succ",
		"op-complete",
		started,
	)
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = a.ObserveInterruptedInstall(context.Background(), lifecycle.InterruptedInstallRequest{
		TargetVersion:   "17.18.02",
		SourceSize:      1247897709,
		NotBefore:       started,
		ObservedAt:      started.Add(10 * time.Minute),
		DeviceNotBefore: started,
	})
	if err == nil || !strings.Contains(err.Error(), "device-clock interval") {
		t.Fatalf("error = %v, want invalid device-clock interval", err)
	}
}

func TestObserveInterruptedInstallFailsClosedOnPartialEvidence(t *testing.T) {
	started := time.Date(2026, 10, 1, 22, 34, 53, 0, time.UTC)
	tests := []struct {
		name      string
		activity  string
		pkgState  string
		verify    string
		size      string
		opStatus  string
		opDone    string
		operation time.Time
	}{
		{name: "installer active", activity: "install-activity-install", pkgState: "install-state-added", verify: "install-package-verify-ok", size: "1247897709", opStatus: "install-op-succ", opDone: "op-complete", operation: started},
		{name: "package incomplete", activity: "install-no-activity", pkgState: "install-state-adding", verify: "install-package-verify-ok", size: "1247897709", opStatus: "install-op-succ", opDone: "op-complete", operation: started},
		{name: "source unverified", activity: "install-no-activity", pkgState: "install-state-added", verify: "install-package-verify-deferred", size: "1247897709", opStatus: "install-op-succ", opDone: "op-complete", operation: started},
		{name: "source size mismatch", activity: "install-no-activity", pkgState: "install-state-added", verify: "install-package-verify-ok", size: "1247897708", opStatus: "install-op-succ", opDone: "op-complete", operation: started},
		{name: "operation failed", activity: "install-no-activity", pkgState: "install-state-added", verify: "install-package-verify-ok", size: "1247897709", opStatus: "install-op-fail", opDone: "op-complete", operation: started},
		{name: "operation too old", activity: "install-no-activity", pkgState: "install-state-added", verify: "install-package-verify-ok", size: "1247897709", opStatus: "install-op-succ", opDone: "op-complete", operation: started.Add(-6 * time.Minute)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := interruptedInstallResponse(test.activity, test.pkgState, test.verify, test.size,
				test.opStatus, test.opDone, test.operation)
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := a.ObserveInterruptedInstall(context.Background(), lifecycle.InterruptedInstallRequest{
				TargetVersion: "17.18.02", SourceSize: 1247897709, NotBefore: started,
				ObservedAt: started.Add(10 * time.Minute),
			}); err == nil {
				t.Fatal("partial evidence was accepted")
			}
		})
	}
}

func TestRegisterDeviceFileUsesInstallOnlyRPC(t *testing.T) {
	rpc := &rpcTransport{fakeTransport: &fakeTransport{kind: configtransport.KindRESTCONF}}
	a, err := New(rpc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	registration, err := a.RegisterDeviceFile(context.Background(), lifecycle.DeviceFileRequest{
		Path:        "flash:images/cat9k_iosxe.17.18.04.SPA.bin",
		OperationID: testOperationID,
	})
	if err != nil {
		t.Fatalf("RegisterDeviceFile: %v", err)
	}
	if registration.OperationID != testOperationID || rpc.rpcPath != installRPCPath {
		t.Fatalf("registration = %+v, path = %q", registration, rpc.rpcPath)
	}
	var payload installRPCEnvelope
	if err := json.Unmarshal(rpc.rpcPayload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Input.UUID != testOperationID || payload.Input.Path != "flash:images/cat9k_iosxe.17.18.04.SPA.bin" || payload.Input.OneShot {
		t.Errorf("payload = %+v", payload.Input)
	}
}

func TestRegisterDeviceFileRequiresRPCCapability(t *testing.T) {
	a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = a.RegisterDeviceFile(context.Background(), lifecycle.DeviceFileRequest{
		Path:        "flash:image.bin",
		OperationID: testOperationID,
	})
	if !errors.Is(err, lifecycle.ErrUnsupported) {
		t.Fatalf("RegisterDeviceFile error = %v, want ErrUnsupported", err)
	}
}

func TestValidateDevicePath(t *testing.T) {
	for _, path := range []string{
		"flash:cat9k_iosxe.17.18.04.SPA.bin",
		"bootflash:/images/release.pkg",
		"harddisk:packages.conf",
	} {
		if err := ValidateDevicePath(path); err != nil {
			t.Errorf("ValidateDevicePath(%q): %v", path, err)
		}
	}
	tooLong := "flash:" + strings.Repeat("a", 123)
	for _, path := range []string{
		"",
		"usbflash:image.bin",
		"flash:",
		"flash:../image.bin",
		"flash:images/../image.bin",
		"flash:user@host/image.bin",
		"flash:image name.bin",
		"flash:image%2ebin",
		"flash:image.bin?x=y",
		"flash:image.bin\nother",
		"flash://image.bin",
		"flash:images/",
		tooLong,
	} {
		if err := ValidateDevicePath(path); !errors.Is(err, lifecycle.ErrInvalidDevicePath) {
			t.Errorf("ValidateDevicePath(%q) error = %v", path, err)
		}
	}
}

func TestValidateOperationID(t *testing.T) {
	for _, id := range []string{
		testOperationID,
		"123E4567-E89B-72D3-B456-426614174000",
	} {
		if err := ValidateOperationID(id); err != nil {
			t.Errorf("ValidateOperationID(%q): %v", id, err)
		}
	}
	for _, id := range []string{
		"",
		"upgrade-name",
		"123e4567-e89b-02d3-a456-426614174000",
		"123e4567-e89b-42d3-7456-426614174000",
		"123e4567e89b42d3a456426614174000",
		"123e4567-e89b-42d3-a456-42661417400z",
	} {
		if err := ValidateOperationID(id); !errors.Is(err, lifecycle.ErrInvalidOperationID) {
			t.Errorf("ValidateOperationID(%q) error = %v", id, err)
		}
	}
}

func TestObserveDeviceFileCorrelatesOperationAndInventory(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation map[string]string
		inventory map[string]string
		wantState lifecycle.OperationState
		wantImage bool
		wantErr   error
	}{
		{
			name:      "in progress",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-in-progress", "op-done": "op-not-complete"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-in-progress"},
			wantState: lifecycle.OperationStateInProgress,
			wantImage: true,
		},
		{
			name:      "success requires activatable inventory",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-succ", "op-done": "op-complete"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantState: lifecycle.OperationStateSucceeded,
			wantImage: true,
		},
		{
			name:      "success waits for inventory convergence",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-succ", "op-done": "op-complete"},
			wantState: lifecycle.OperationStatePending,
		},
		{
			name:      "success status without completion stays unknown",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-succ"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantState: lifecycle.OperationStateUnknown,
			wantImage: true,
		},
		{
			name:      "success status while not complete stays in progress",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-succ", "op-done": "op-not-complete"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantState: lifecycle.OperationStateInProgress,
			wantImage: true,
		},
		{
			name:      "contradictory in-progress status and completion stays unknown",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-in-progress", "op-done": "op-complete"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantState: lifecycle.OperationStateUnknown,
			wantImage: true,
		},
		{
			name:      "failure",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-fail-revert", "op-done": "op-complete"},
			wantState: lifecycle.OperationStateFailed,
		},
		{
			name:      "operation failure wins over leftover inventory",
			operation: map[string]string{"op-uuid": testOperationID, "op-status": "install-op-fail", "op-done": "op-complete"},
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantState: lifecycle.OperationStateFailed,
			wantImage: true,
		},
		{
			name:      "inventory cannot replace missing operation correlation",
			inventory: map[string]string{"version": "17.18.04", "current": "install-version-state-installed"},
			wantErr:   lifecycle.ErrOperationNotFound,
		},
		{
			name:    "operation and target absent",
			wantErr: lifecycle.ErrOperationNotFound,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := fullOperResponse(test.operation, test.inventory)
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: raw}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := a.ObserveDeviceFile(context.Background(), testOperationID, "17.18.04")
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ObserveDeviceFile error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ObserveDeviceFile: %v", err)
			}
			if got.State != test.wantState || (got.Image != nil) != test.wantImage {
				t.Errorf("observation = %+v, want state %s image=%t", got, test.wantState, test.wantImage)
			}
			if test.wantState == lifecycle.OperationStateSucceeded && (got.Image == nil || !got.Image.State.Activatable()) {
				t.Errorf("successful observation lacks activatable image: %+v", got)
			}
		})
	}
}

func TestFindOperationRejectsUUIDReusedAcrossActiveAndHistory(t *testing.T) {
	root := map[string]any{
		"install-oper": []any{
			map[string]any{"op-uuid": testOperationID, "op-status": "install-op-in-progress"},
		},
		"install-oper-hist": []any{
			map[string]any{"op-uuid": testOperationID, "op-status": "install-op-succ"},
		},
	}
	_, _, err := findOperation(root, testOperationID)
	if !errors.Is(err, lifecycle.ErrAmbiguousOperation) {
		t.Fatalf("findOperation error = %v, want ErrAmbiguousOperation", err)
	}
}

func inventoryResponse(versions []map[string]string) []byte {
	if versions == nil {
		versions = []map[string]string{}
	}
	return mustJSON(map[string]any{
		"Cisco-IOS-XE-install-oper:install-location-information": []any{
			map[string]any{"install-version-info": versions},
		},
	})
}

func fullOperResponse(operation, inventory map[string]string) []byte {
	operations := []map[string]string{}
	if operation != nil {
		operations = append(operations, operation)
	}
	versions := []map[string]string{}
	if inventory != nil {
		versions = append(versions, inventory)
	}
	return mustJSON(map[string]any{
		"Cisco-IOS-XE-install-oper:install-oper-data": map[string]any{
			"install-oper":      operations,
			"install-oper-hist": []map[string]string{},
			"install-location-information": []any{
				map[string]any{"install-version-info": versions},
			},
		},
	})
}

func interruptedInstallResponse(activity, packageState, verify, size, opStatus, opDone string, operationTime time.Time) []byte {
	const sourceName = "gNOI_iosxe_17.18.02.0.4112.1766116039.bin"
	return mustJSON(map[string]any{
		"Cisco-IOS-XE-install-oper:install-oper-data": map[string]any{
			"install-oper": []any{},
			"install-oper-hist": []any{map[string]any{
				"op-uuid":    testOperationID,
				"op-status":  opStatus,
				"op-done":    opDone,
				"start-time": operationTime.Format(time.RFC3339Nano),
				"end-time":   operationTime.Add(2 * time.Minute).Format(time.RFC3339Nano),
				"add-param":  map[string]any{"dest-filename": sourceName},
			}},
			"install-location-information": []any{map[string]any{
				"oper-state": map[string]any{"sys-activity": activity},
				"install-packages": []any{map[string]any{
					"pkg-name": sourceName,
					"pkg-data": map[string]any{"verify-status": verify, "pkg-size": size},
				}},
				"install-version-info": []any{map[string]any{
					"version":           "17.18.02.0.4112",
					"version-extension": "1766116039",
					"current":           "install-version-state-in-progress",
					"src-filename":      "/mnt/sd3/user/" + sourceName,
					"install-package-state-info": []any{
						map[string]any{"pkg-name": "cat9k-rpbase.17.18.02.SPA.pkg", "package-state": packageState},
					},
				}},
			}},
		},
	})
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
