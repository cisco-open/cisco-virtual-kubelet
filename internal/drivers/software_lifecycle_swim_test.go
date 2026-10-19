// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package drivers

import (
	"bytes"
	"context"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"io"
	"testing"
)

type swimBackendStub struct {
	lifecycleBackendStub
	value   string
	removed *int
}

func (s swimBackendStub) ObserveSWIMFlash(context.Context, softwarelifecycle.SWIMFlashRequest) (softwarelifecycle.SWIMFlashSnapshot, error) {
	return softwarelifecycle.SWIMFlashSnapshot{EvidenceHash: s.value}, nil
}
func (s swimBackendStub) ReadRetiredArchive(_ context.Context, _ string, _ int64, w io.Writer) error {
	_, e := io.WriteString(w, s.value)
	return e
}
func (s swimBackendStub) RemoveRetiredArchive(context.Context, string) error {
	*s.removed++
	return nil
}
func (s swimBackendStub) ObserveSWIMBootManifest(context.Context) (string, error) {
	return s.value, nil
}
func TestDynamicLifecycleForwardsSWIMUsingCurrentTransport(t *testing.T) {
	kind := cvk.DeviceDriver("test-swim-forwarding")
	provider := &lifecycleTransportProvider{}
	var selected softwarelifecycle.Backend = lifecycleBackendStub{}
	calls := 0
	RegisterSoftwareLifecycle(kind, func(tr transport.Interface) (softwarelifecycle.Backend, error) {
		calls++
		if tr != provider.current {
			t.Fatal("stale transport")
		}
		return selected, nil
	}, func(string) error { return nil })
	backend, err := NewSoftwareLifecycle(kind, provider)
	if err != nil {
		t.Fatal(err)
	}
	observer, ok := backend.(softwarelifecycle.SWIMPreparationObserver)
	if !ok {
		t.Fatal("missing flash observer")
	}
	access, ok := backend.(softwarelifecycle.RetiredArchiveAccess)
	if !ok {
		t.Fatal("missing archive capability")
	}
	ctx := context.Background()
	if err = access.RemoveRetiredArchive(ctx, "path"); err == nil || calls != 0 {
		t.Fatal("missing transport accepted")
	}
	provider.current = &lifecycleTransportStub{kind: transport.KindRESTCONF}
	if err = access.RemoveRetiredArchive(ctx, "path"); !errors.Is(err, softwarelifecycle.ErrUnsupported) {
		t.Fatal("unsupported adapter accepted")
	}
	removed := 0
	for _, value := range []string{"first", "rotated"} {
		provider.current = &lifecycleTransportStub{kind: transport.KindRESTCONF}
		selected = swimBackendStub{value: value, removed: &removed}
		s, e := observer.ObserveSWIMFlash(ctx, softwarelifecycle.SWIMFlashRequest{RunningVersion: "running"})
		if e != nil || s.EvidenceHash != value {
			t.Fatal("flash capability not forwarded")
		}
		var b bytes.Buffer
		if e = access.ReadRetiredArchive(ctx, "path", 5, &b); e != nil || b.String() != value {
			t.Fatal("archive capability not forwarded")
		}
		if v, e := access.ObserveSWIMBootManifest(ctx); e != nil || v != value {
			t.Fatal("boot capability not forwarded")
		}
		if e = access.RemoveRetiredArchive(ctx, "path"); e != nil {
			t.Fatal(e)
		}
	}
	if removed != 2 {
		t.Fatal("unexpected removal count")
	}
}
