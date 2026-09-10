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

package engine

import (
	"context"
	"errors"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

func TestMaintenanceBlocksConfigWritesButNotReportReads(t *testing.T) {
	for _, policy := range []configv1alpha1.DriftPolicy{configv1alpha1.DriftPolicyRevert, configv1alpha1.DriftPolicyReport} {
		t.Run(string(policy), func(t *testing.T) {
			w := &fakeWriter{family: "vlan", ops: []transport.Op{{Verb: transport.VerbMerge, Path: "/vlan"}}}
			guardCalls := 0
			e := &Engine{Transport: &stubTransport{}, Lookup: func(string, string) writers.SectionWriter { return w },
				AcquireMutation: func(ctx context.Context) (context.Context, func(error), error) {
					guardCalls++
					return ctx, nil, errors.New("device maintenance held by software upgrade")
				}}
			res := &intent.ResolvedIntent{DeviceName: "switch", ManagedFamilies: []string{"vlan"},
				Configuration: map[string]any{"vlan": map[string]any{}}, DriftPolicy: policy}
			got := e.Reconcile(context.Background(), res)
			if w.applies != 0 {
				t.Fatal("maintenance allowed config write")
			}
			if policy == configv1alpha1.DriftPolicyReport {
				if guardCalls != 0 || w.fetches == 0 || got.Phase != PhaseDrifted {
					t.Fatalf("report read blocked: %+v", got)
				}
			} else if guardCalls != 1 || got.Phase != PhaseLeaseBlocked || got.DeviceTouched {
				t.Fatalf("write did not report maintenance: %+v", got)
			}
		})
	}
}

type maintenanceTransactionTransport struct {
	stubTransport
	t         *testing.T
	held      *bool
	committed bool
}

func (tr *maintenanceTransactionTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	if !*tr.held {
		tr.t.Fatal("transaction started without maintenance lease")
	}
	return "tx", nil
}
func (tr *maintenanceTransactionTransport) Commit(context.Context, transport.TxHandle) error {
	if !*tr.held {
		tr.t.Fatal("maintenance lease released before commit")
	}
	tr.committed = true
	return nil
}

func TestMaintenanceCoversWholeConfigTransaction(t *testing.T) {
	held := false
	tr := &maintenanceTransactionTransport{t: t, held: &held, stubTransport: stubTransport{caps: transport.Capabilities{SupportsTransactions: true}}}
	w := &fakeWriter{family: "vlan", ops: []transport.Op{{Verb: transport.VerbMerge, Path: "/vlan"}}}
	e := &Engine{Transport: tr, Lookup: func(string, string) writers.SectionWriter { return w },
		AcquireMutation: func(ctx context.Context) (context.Context, func(error), error) {
			held = true
			return ctx, func(err error) {
				if !tr.committed || err != nil {
					t.Fatalf("finish before successful commit: %v", err)
				}
				held = false
			}, nil
		}}
	got := e.Reconcile(context.Background(), &intent.ResolvedIntent{DeviceName: "switch", ManagedFamilies: []string{"vlan"},
		Configuration: map[string]any{"vlan": map[string]any{}}, DriftPolicy: configv1alpha1.DriftPolicyRevert, Transactional: true})
	if got.Phase != PhaseInSync || held || !tr.committed {
		t.Fatalf("transaction did not finish safely: %+v held=%v", got, held)
	}
}

func TestMaintenanceRetainsUncertainStartupSave(t *testing.T) {
	e, tr, res := newTxFixture(true, true, true, true)
	tr.saveStartupErr = errors.New("startup save response lost")
	var outcome error
	e.AcquireMutation = func(ctx context.Context) (context.Context, func(error), error) {
		return ctx, func(err error) { outcome = err }, nil
	}
	got := e.Reconcile(context.Background(), res)
	if got.Phase != PhaseInSync || got.Err != nil {
		t.Fatalf("startup save failure changed existing config result: %+v", got)
	}
	if !errors.Is(outcome, tr.saveStartupErr) {
		t.Fatalf("maintenance released uncertain startup save: %v", outcome)
	}
}

func TestMaintenanceSkipsInvalidTransactionalCLI(t *testing.T) {
	e, _, res := newTxFixture(true, false, false, true)
	res.CLIBlocks = []intent.CLIBlock{{TemplateName: "test-cli", CLI: "interface Loopback0\n"}}
	e.AcquireMutation = func(ctx context.Context) (context.Context, func(error), error) {
		t.Fatal("invalid intent acquired a write Lease")
		return ctx, func(error) {}, nil
	}
	if got := e.Reconcile(context.Background(), res); !errors.Is(got.Err, ErrTransactionalCLIUnsupported) {
		t.Fatalf("unexpected validation outcome: %+v", got)
	}
}

func TestMaintenanceReportNeverTransactionsOrSaves(t *testing.T) {
	e, tr, res := newTxFixture(true, true, true, true)
	res.DriftPolicy = configv1alpha1.DriftPolicyReport
	w := &fakeWriter{family: "system"}
	e.Lookup = func(string, string) writers.SectionWriter { return w }
	e.AcquireMutation = func(ctx context.Context) (context.Context, func(error), error) {
		t.Fatal("read-only report acquired a write Lease")
		return ctx, func(error) {}, nil
	}
	got := e.Reconcile(context.Background(), res)
	if got.Phase != PhaseInSync || w.fetches == 0 {
		t.Fatalf("read-only report did not observe device: %+v", got)
	}
	if tr.startCalls.Load() != 0 || tr.commitCalls.Load() != 0 || tr.discardCalls.Load() != 0 || tr.saveStartupCalls.Load() != 0 {
		t.Fatalf("report triggered a transaction or startup save: start=%d commit=%d discard=%d save=%d",
			tr.startCalls.Load(), tr.commitCalls.Load(), tr.discardCalls.Load(), tr.saveStartupCalls.Load())
	}
}

type panickingMaintenanceTransport struct{ stubTransport }

func (*panickingMaintenanceTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	panic("transaction panic")
}

func TestMaintenanceConfigPanicRetainsUncertainOutcome(t *testing.T) {
	e, _, res := newTxFixture(true, false, false, true)
	e.Transport = &panickingMaintenanceTransport{stubTransport{caps: transport.Capabilities{SupportsTransactions: true}}}
	var outcome error
	e.AcquireMutation = func(ctx context.Context) (context.Context, func(error), error) {
		return ctx, func(err error) { outcome = err }, nil
	}
	defer func() {
		if got := recover(); got != "transaction panic" {
			t.Fatalf("panic was suppressed or replaced: %v", got)
		}
		if !errors.Is(outcome, devicecoordination.ErrMutationIncomplete) {
			t.Fatalf("panic passed successful outcome: %v", outcome)
		}
	}()
	_ = e.Reconcile(context.Background(), res)
}
