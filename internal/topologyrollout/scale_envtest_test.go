// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build envtest

package topologyrollout

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestEnvtest_E12LedgerAPIContentionAndLatency complements the pure scale
// benchmark with the boundaries only a real apiserver can exercise:
// resourceVersion conflict/retry behavior and uncached REST latency for each
// predeclared ledger size. It deliberately avoids a throughput assertion;
// envtest is a correctness gate, not a production control-plane benchmark.
func TestEnvtest_E12LedgerAPIContentionAndLatency(t *testing.T) {
	testEnv := &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest start: %v (is KUBEBUILDER_ASSETS set?)", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("envtest stop: %v", err)
		}
	}()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const namespace = "e12-ledger-api"
	if err := apiClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: namespace, Name: "fleet-ledger"}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
	if err := apiClient.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, key, cm); err != nil {
		t.Fatal(err)
	}
	ledgerUID := cm.UID
	writeE12Ledger(t, ctx, apiClient, key, ledgerUID, 0)

	profile := readE12ScaleProfile(t)
	members := e12ScaleMembers(profile.FleetMembers)
	policy := e12ScalePolicy(profile)

	t.Run("resourceVersion contention retries", func(t *testing.T) {
		writeE12Ledger(t, ctx, apiClient, key, ledgerUID, 0)
		gate := newE12PatchBarrier(2)
		var callbacks atomic.Int32
		errors := make(chan error, 2)
		var wg sync.WaitGroup
		for index := 0; index < 2; index++ {
			index := index
			wg.Add(1)
			go func() {
				defer wg.Done()
				patchClient := &e12FirstPatchBarrierClient{Client: apiClient, gate: gate}
				store := Store{Client: patchClient, APIReader: apiClient, Key: key, ExpectedUID: ledgerUID}
				err := store.Mutate(ctx, func(ledger *Ledger) error {
					callbacks.Add(1)
					return Reserve(ledger, policy, members, e12ScaleRequest(index, members[index], 2))
				})
				errors <- err
			}()
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatalf("contended mutation: %v", err)
			}
		}
		if got := callbacks.Load(); got < 3 {
			t.Fatalf("mutation callbacks = %d, want a real conflict retry", got)
		}
		store := Store{Client: apiClient, APIReader: apiClient, Key: key, ExpectedUID: ledgerUID}
		_, ledger, err := store.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(ledger.Reservations); got != 2 {
			t.Fatalf("persisted reservations = %d, want 2", got)
		}
	})

	for _, targets := range profile.CampaignTargets {
		targets := targets
		t.Run(fmt.Sprintf("uncached reads targets-%03d", targets), func(t *testing.T) {
			encodedBytes := writeE12Ledger(t, ctx, apiClient, key, ledgerUID, targets)
			store := Store{Client: apiClient, APIReader: apiClient, Key: key, ExpectedUID: ledgerUID}
			const samples = 20
			latencies := make([]time.Duration, 0, samples)
			for sample := 0; sample < samples; sample++ {
				started := time.Now()
				if _, ledger, err := store.Read(ctx); err != nil {
					t.Fatal(err)
				} else if len(ledger.Reservations) != targets {
					t.Fatalf("reservations = %d, want %d", len(ledger.Reservations), targets)
				}
				latencies = append(latencies, time.Since(started))
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			p50 := latencies[(samples*50+99)/100-1]
			p95 := latencies[(samples*95+99)/100-1]
			p99 := latencies[(samples*99+99)/100-1]
			t.Logf("targets=%d ledgerBytes=%d uncachedRead p50=%s p95=%s p99=%s",
				targets, encodedBytes, p50, p95, p99)
			if p99 > time.Duration(profile.ReconcileLatencyBudgetSeconds.P99*float64(time.Second)) {
				t.Fatalf("uncached API read p99 %s exceeds declared reconcile p99 budget %.3fs",
					p99, profile.ReconcileLatencyBudgetSeconds.P99)
			}
		})
	}
}

func writeE12Ledger(
	t *testing.T,
	ctx context.Context,
	apiClient client.Client,
	key types.NamespacedName,
	uid types.UID,
	targets int,
) int {
	t.Helper()
	profile := readE12ScaleProfile(t)
	members := e12ScaleMembers(profile.FleetMembers)
	policy := e12ScalePolicy(profile)
	ledger, err := NewLedger(string(uid))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < targets; index++ {
		if err := Reserve(ledger, policy, members, e12ScaleRequest(index, members[index], targets)); err != nil {
			t.Fatalf("reserve target %d: %v", index, err)
		}
	}
	encoded, err := Encode(ledger, profile.MaxLedgerBytes)
	if err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := apiClient.Get(ctx, key, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data = map[string]string{LedgerDataKey: string(encoded)}
	if err := apiClient.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	return len(encoded)
}

type e12PatchBarrier struct {
	want    int32
	arrived atomic.Int32
	release chan struct{}
	once    sync.Once
}

func newE12PatchBarrier(want int32) *e12PatchBarrier {
	return &e12PatchBarrier{want: want, release: make(chan struct{})}
}

func (b *e12PatchBarrier) wait(ctx context.Context) error {
	if b.arrived.Add(1) == b.want {
		b.once.Do(func() { close(b.release) })
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type e12FirstPatchBarrierClient struct {
	client.Client
	gate  *e12PatchBarrier
	first atomic.Bool
}

func (c *e12FirstPatchBarrierClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	if c.first.CompareAndSwap(false, true) {
		if err := c.gate.wait(ctx); err != nil {
			return err
		}
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}
