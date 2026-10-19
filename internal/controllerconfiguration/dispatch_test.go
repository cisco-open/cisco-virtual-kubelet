// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type unavailableDispatchReader struct {
	client.Reader
	reads atomic.Int32
}

func (r *unavailableDispatchReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	r.reads.Add(1)
	return errors.New("API read unavailable")
}

func TestDispatchPermitFailureIsConsumedAcrossCopies(t *testing.T) {
	reader := &unavailableDispatchReader{}
	_, r, _, now := claimFixture(t)
	permit := DispatchPermit{state: &dispatchPermitState{reader: reader, reservation: r, authorize: func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Error("authorization reached after failed evidence read")
		return nil
	}}}
	var wg sync.WaitGroup
	for range 16 {
		copy := permit
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := copy.Check(context.Background(), now); err == nil {
				t.Error("unavailable evidence allowed dispatch")
			}
		}()
	}
	wg.Wait()
	if reader.reads.Load() != 1 {
		t.Fatalf("consumed permit reread evidence %d times", reader.reads.Load())
	}
	if err := permit.Check(context.Background(), now); err == nil {
		t.Fatal("failed check was reusable")
	}
}

func TestDispatchPermitCannotBeRestored(t *testing.T) {
	original := DispatchPermit{state: &dispatchPermitState{}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored DispatchPermit
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(context.Background(), time.Now()); err == nil {
		t.Fatal("serialized permit restored authority")
	}
	var absent *DispatchPermit
	if err := absent.Check(context.Background(), time.Now()); err == nil {
		t.Fatal("nil permit allowed dispatch")
	}
}
