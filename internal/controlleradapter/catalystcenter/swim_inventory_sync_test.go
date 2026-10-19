// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type preparationTestAuthority struct {
	*testSWIMAuthority
	ready bool
}

func (a *preparationTestAuthority) PreparationReceipt(_ context.Context, _ swimIntent, s swimPhase) (string, error) {
	if !a.ready {
		return "", nil
	}
	return "receipt-" + string(s), nil
}

type syncTestAPI struct {
	*testModernSWIMAPI
	syncs    int
	fail     bool
	complete bool
}

func (a *syncTestAPI) StartInventorySync(context.Context, string) (Task, error) {
	a.syncs++
	if a.fail {
		return Task{}, errors.New("lost response")
	}
	return Task{ID: fmt.Sprintf("sync-%d", a.syncs)}, nil
}
func (a *syncTestAPI) ObserveInventorySync(context.Context, string, string, time.Time, time.Time) (bool, error) {
	return a.complete, nil
}
func TestAutomaticPreparationGatesBothSWIMStages(t *testing.T) {
	e, i, s, a, _, _ := executionFixture(t)
	pa := &preparationTestAuthority{testSWIMAuthority: a}
	api := &syncTestAPI{testModernSWIMAPI: &testModernSWIMAPI{store: s}}
	e.authority = pa
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03.0.5496"
	i.PreparationPolicySHA256 = strings.Repeat("a", 64)
	step(t, e, i)
	step(t, e, i)
	if s.record.Phase != swimPreparing {
		t.Fatal(s.record.Phase)
	}
	if e.Step(context.Background(), i) == nil || api.syncs != 0 || api.distributions != 0 {
		t.Fatal("missing preparation did not block")
	}
	pa.ready = true
	step(t, e, i)
	step(t, e, i)
	if api.syncs != 1 || api.readiness != 0 {
		t.Fatal("readiness ran before inventory completion")
	}
	api.complete = true
	for n := 0; n < 6; n++ {
		step(t, e, i)
	}
	if api.distributions != 1 || api.activations != 0 || s.record.Phase != swimPreparing {
		t.Fatalf("activation preparation missing: %+v", s.record)
	}
	pa.ready = false
	if e.Step(context.Background(), i) == nil || api.syncs != 1 {
		t.Fatal("activation reused distribution preparation")
	}
	pa.ready = true
	for n := 0; n < 9; n++ {
		step(t, e, i)
	}
	if api.syncs != 2 || api.readiness != 2 || api.activations != 1 || s.record.Phase != swimSucceeded {
		t.Fatalf("incomplete flow: %+v", s.record)
	}
	if len(s.record.ReadinessHistory) != 1 || s.record.ReadinessHistory[0].Stage != string(swimReadyToDistribute) {
		t.Fatal("readiness history rebound to wrong stage")
	}
}
func TestInventorySyncUnknownNeverReplayed(t *testing.T) {
	for _, lostWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(lostWrite), func(t *testing.T) {
			e, i, s, a, _, _ := executionFixture(t)
			e.authority = &preparationTestAuthority{testSWIMAuthority: a, ready: true}
			api := &syncTestAPI{testModernSWIMAPI: &testModernSWIMAPI{store: s}, fail: !lostWrite}
			e.api = api
			i.APIContract = swimModernContract
			i.ControllerImageVersion = "17.18.03.0.5496"
			i.PreparationPolicySHA256 = strings.Repeat("a", 64)
			step(t, e, i)
			step(t, e, i)
			if lostWrite {
				s.failWrite = s.writes + 2
			}
			_ = e.Step(context.Background(), i)
			for n := 0; n < 3; n++ {
				step(t, e, i)
			}
			if api.syncs != 1 || s.record.Phase != swimOutcomeUnknown || api.readiness != 0 || a.releases != 0 {
				t.Fatal("ambiguous sync replayed or advanced")
			}
		})
	}
}
func TestInventorySyncRequiresExactFreshChild(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Minute)
	raw := fmt.Sprintf(`{"response":[{"id":"root","rootId":"root","serviceType":"Inventory service","isError":false,"startTime":%d,"endTime":%d},{"id":"child","rootId":"root","parentId":"root","serviceType":"Inventory service","progress":"Synced device: 192.0.2.1 Status: SUCCESS","isError":false,"startTime":%d,"endTime":%d}]}`, start.UnixMilli(), now.UnixMilli(), start.UnixMilli(), now.UnixMilli())
	for name, body := range map[string]string{"valid": raw, "wrong device": strings.ReplaceAll(raw, "192.0.2.1", "192.0.2.2"), "failed": strings.ReplaceAll(raw, `"isError":false`, `"isError":true`), "wrong parent": strings.ReplaceAll(raw, `"parentId":"root"`, `"parentId":"other"`), "missing error": strings.ReplaceAll(raw, `"isError":false,`, "")} {
		ok, err := inventorySyncComplete([]byte(body), "root", "192.0.2.1", start, now)
		if name == "valid" {
			if err != nil || !ok {
				t.Fatal(err)
			}
		} else if ok || err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if ok, _ := inventorySyncComplete([]byte(raw), "root", "192.0.2.1", start.Add(time.Second), now); ok {
		t.Fatal("accepted stale result")
	}
}
func TestInventorySyncPUTDoesNotReplay(t *testing.T) {
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		requests++
		if r.Method != http.MethodPut || r.URL.Path != devicesPath+"/sync" {
			t.Errorf("bad sync request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(401)
	})
	if _, err := c.StartInventorySync(context.Background(), "device-id"); err == nil {
		t.Fatal("401 accepted")
	}
	if requests != 1 {
		t.Fatalf("mutation replayed %d times", requests)
	}
}
