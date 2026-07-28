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

package writers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

// ambiguousMutationTransport simulates the most important non-transactional
// recovery case: NX-OS accepted the DME write, but the client lost the
// acknowledgement and therefore received an error. A safe subsequent
// reconcile must Fetch first, observe the intended state, and avoid blindly
// replaying the mutation.
type ambiguousMutationTransport struct {
	vlanName        string
	mutationCalls   int
	injectAckLoss   bool
	unexpectedCalls []string
}

func (t *ambiguousMutationTransport) Capabilities() transport.Capabilities {
	return transport.Capabilities{
		Kind:                    transport.KindNXAPI,
		SupportsWritableRunning: true,
	}
}

func (t *ambiguousMutationTransport) Fetch(_ context.Context, path string) ([]byte, error) {
	if path != nxosschema.PathVLANBrief {
		t.unexpectedCalls = append(t.unexpectedCalls, "Fetch "+path)
		return nil, errors.New("unexpected fetch path")
	}
	return json.Marshal(map[string]any{
		"vlans": []any{map[string]any{"id": 101, "name": t.vlanName}},
	})
}

func (t *ambiguousMutationTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	t.unexpectedCalls = append(t.unexpectedCalls, "StartTransaction")
	return "", transport.ErrUnsupported
}

func (t *ambiguousMutationTransport) Mutate(_ context.Context, tx transport.TxHandle, ops []transport.Op) error {
	t.mutationCalls++
	if tx != "" {
		t.unexpectedCalls = append(t.unexpectedCalls, "Mutate transaction")
		return transport.ErrUnsupported
	}
	if len(ops) != 1 || ops[0].Verb != transport.VerbMerge || ops[0].Path != nxosschema.DNBridgeDomain {
		return errors.New("unexpected recovery mutation")
	}
	body := string(ops[0].Body)
	if !strings.Contains(body, `"fabEncap":"vlan-101"`) || !strings.Contains(body, `"name":"cvk-golden"`) {
		return errors.New("recovery mutation does not contain the intended VLAN")
	}

	// The device applied the write before the response disappeared.
	t.vlanName = "cvk-golden"
	if t.injectAckLoss {
		t.injectAckLoss = false
		return errors.New("injected acknowledgement loss after DME apply")
	}
	return nil
}

func (t *ambiguousMutationTransport) Commit(context.Context, transport.TxHandle) error {
	return nil
}

func (t *ambiguousMutationTransport) Discard(context.Context, transport.TxHandle) error {
	return nil
}

func (t *ambiguousMutationTransport) SaveStartup(context.Context) error {
	t.unexpectedCalls = append(t.unexpectedCalls, "SaveStartup")
	return transport.ErrUnsupported
}

func (t *ambiguousMutationTransport) Close() error { return nil }

func TestNXOSRecoveryConvergesAfterInjectedMutationFailure(t *testing.T) {
	tr := &ambiguousMutationTransport{
		vlanName:      "old-name",
		injectAckLoss: true,
	}
	reconciler := &engine.Engine{
		Platform:      "nxos",
		DeviceVersion: "10.3(9)",
		Transport:     tr,
		Lookup:        GetForRelease,
	}
	resolved := &intent.ResolvedIntent{
		DeviceName:      "leaf-recovery",
		ManagedFamilies: []string{"vlan"},
		Configuration: map[string]any{
			"vlan": map[string]any{
				"vlans": []any{map[string]any{"id": 101, "name": "cvk-golden"}},
			},
		},
		ModelVersion:      pinnedNetAsCodeOracleVersion,
		DriftPolicy:       configv1alpha1.DriftPolicyRevert,
		TargetYangVersion: "10.3(9)",
	}

	first := reconciler.Reconcile(context.Background(), resolved)
	if first.Phase != engine.PhaseFailed || first.Err == nil {
		t.Fatalf("first reconcile phase=%q err=%v, want failed ambiguous apply", first.Phase, first.Err)
	}
	if len(first.FamilyStatuses) != 1 || first.FamilyStatuses[0].State != "ApplyError" {
		t.Fatalf("first family status=%#v, want ApplyError", first.FamilyStatuses)
	}
	if tr.vlanName != "cvk-golden" || tr.mutationCalls != 1 {
		t.Fatalf("injected state name=%q mutationCalls=%d, want applied once before acknowledgement loss", tr.vlanName, tr.mutationCalls)
	}

	second := reconciler.Reconcile(context.Background(), resolved)
	if second.Phase != engine.PhaseInSync || second.Err != nil {
		t.Fatalf("recovery reconcile phase=%q err=%v, want InSync", second.Phase, second.Err)
	}
	if tr.mutationCalls != 1 {
		t.Fatalf("recovery replayed ambiguous mutation: calls=%d, want 1", tr.mutationCalls)
	}
	if len(second.VerifiedFamilies) != 1 || second.VerifiedFamilies[0] != "vlan" {
		t.Fatalf("recovery verification evidence=%v, want vlan", second.VerifiedFamilies)
	}

	third := reconciler.Reconcile(context.Background(), resolved)
	if third.Phase != engine.PhaseInSync || third.Err != nil || tr.mutationCalls != 1 {
		t.Fatalf("steady-state phase=%q err=%v mutationCalls=%d, want stable no-op", third.Phase, third.Err, tr.mutationCalls)
	}
	if len(tr.unexpectedCalls) != 0 {
		t.Fatalf("unexpected non-transactional recovery calls: %v", tr.unexpectedCalls)
	}
}
