// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"testing"
)

type legacyReservationParticipant struct {
	calls     int
	namespace string
	err       error
}

func (p *legacyReservationParticipant) ConfigureConfigurationReservations(namespace string, _ func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	p.calls++
	p.namespace = namespace
	return p.err
}

type groupReservationParticipant struct {
	legacyReservationParticipant
	groupCalls int
}

func (p *groupReservationParticipant) ConfigureConfigurationGroupReservations(namespace string, _ func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	p.groupCalls++
	p.namespace = namespace
	return p.err
}

func TestControllerReservationProtocolSelection(t *testing.T) {
	authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("setup executed operation authorization")
		return nil
	}
	for _, protocol := range []string{contract.Protocol, contract.GroupReservationProtocol, "", "unknown"} {
		t.Run(protocol, func(t *testing.T) {
			p := &groupReservationParticipant{}
			err := configureControllerReservationProtocol(p, protocol, "leases", authorize)
			supported := protocol == contract.Protocol || protocol == contract.GroupReservationProtocol
			if (err == nil) != supported {
				t.Fatalf("protocol selection: %v", err)
			}
			if (p.calls == 1) != (protocol == contract.Protocol) || (p.groupCalls == 1) != (protocol == contract.GroupReservationProtocol) {
				t.Fatal("incorrect setup path")
			}
			if supported && p.namespace != "leases" {
				t.Fatal("lease namespace changed")
			}
		})
	}
	legacy := &legacyReservationParticipant{}
	if err := configureControllerReservationProtocol(legacy, contract.GroupReservationProtocol, "leases", authorize); err == nil || legacy.calls != 0 {
		t.Fatal("group silently used legacy setup")
	}
	expected := errors.New("setup rejected")
	p := &groupReservationParticipant{legacyReservationParticipant: legacyReservationParticipant{err: expected}}
	if err := configureControllerReservationProtocol(p, contract.GroupReservationProtocol, "leases", authorize); !errors.Is(err, expected) {
		t.Fatal("setup error lost", err)
	}
}
