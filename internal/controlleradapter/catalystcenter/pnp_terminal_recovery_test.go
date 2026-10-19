// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPnPTerminalRecovery(t *testing.T) {
	for _, mode := range []string{"terminal-source-missing", "terminal-invalid-intent", "terminal-config-gone", "terminal-paused", "terminal-generation", "terminal-missing-receipt", "terminal-foreign-owner", "terminal-unknown", "terminal-wrong-request", "terminal-worker-gone", "terminal-phase-race", "terminal-target-race"} {
		t.Run(mode, func(t *testing.T) { exercisePnPRemovalPublicMetadataCycle(t, mode, nil, nil) })
	}
}

// Begin with the real public preparation/submission/absence/manager-settlement
// sequence against the controlled appliance. Only then lose or edit intent.
func exercisePnPTerminalRecovery(t *testing.T, ctx context.Context, a *adapter, c *configapi.NetworkControllerConfig, mode string, deletes *atomic.Int32) {
	t.Helper()
	nc, err := a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration.MetadataHandoff != nil || !pnpRemovedReceiptPresent(nc.Status.Configuration, nc.Status.Configuration.Operation) {
		t.Fatal("terminal fixture lacks settled creator-bound absence")
	}
	request := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}
	switch mode {
	case "terminal-source-missing":
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "missing", Key: "intent.yaml"}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "terminal-invalid-intent":
		c.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"unrelated":true}`)}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "terminal-worker-gone":
		var pod core.Pod
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: a.workerPodName}, &pod); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Delete(ctx, &pod); err != nil {
			t.Fatal(err)
		}
	case "terminal-config-gone":
		if err := a.statusWriter.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "terminal-paused", "terminal-generation":
		if mode == "terminal-paused" {
			nc.Spec.Paused = true
		} else {
			nc.Spec.Endpoint = "https://new-controller.invalid"
			nc.Generation++
		}
		if err := a.statusWriter.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "terminal-missing-receipt":
		nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:2]
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "terminal-foreign-owner":
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "terminal-unknown":
		nc.Status.Configuration.Operation.Phase = "Unknown"
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "terminal-wrong-request":
		request.Name = "unrelated-config"
	}
	nc, err = a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	requests := a.configsForTerminalRecovery(ctx, nc)
	if mode == "terminal-unknown" {
		if len(requests) != 0 {
			t.Fatal("uncertain journal advertised terminal completion")
		}
	} else if len(requests) != 1 || requests[0].Name != c.Name {
		t.Fatal("retained journal did not enqueue original config")
	}
	foreign := nc.DeepCopy()
	foreign.UID = "replacement"
	if len(a.configsForTerminalRecovery(ctx, foreign)) != 0 {
		t.Fatal("foreign controller enqueued recovery")
	}
	if mode == "terminal-phase-race" || mode == "terminal-target-race" {
		a.statusReader = &terminalPnPPhaseRace{Reader: a.statusReader, writer: a.statusWriter, key: a.key, target: mode == "terminal-target-race"}
	}
	nativeDeletes := deletes.Load()
	var requestsMade atomic.Int32
	// Completion uses Kubernetes receipts only. Fail any accidental transport
	// call, including read-only native calls and authentication refresh.
	a.client.mu.Lock()
	a.client.builtAt = time.Time{}
	a.client.wrapTransport = func(http.RoundTripper) http.RoundTripper {
		return metadataSubmitRoundTrip(func(*http.Request) (*http.Response, error) {
			requestsMade.Add(1)
			return nil, errors.New("terminal recovery must not contact appliance")
		})
	}
	a.client.mu.Unlock()
	_, err = (&configReconciler{a: a}).Reconcile(ctx, request)
	refused := mode == "terminal-missing-receipt" || mode == "terminal-foreign-owner" || mode == "terminal-phase-race" || mode == "terminal-target-race"
	if (err != nil) != refused {
		t.Fatal("incorrect terminal recovery result", mode, err)
	}
	after := journalState(t, a)
	want := before.DeepCopy()
	if mode == "terminal-phase-race" {
		want.Operation.Phase = "Prepared"
		want.Operation.SubmittedAt = nil
	}
	if mode == "terminal-target-race" {
		want.Operation.TargetID = "foreign-target"
	}
	if !refused && mode != "terminal-unknown" && mode != "terminal-wrong-request" {
		want.Operation = nil
	}
	if !reflect.DeepEqual(want, after) || deletes.Load() != nativeDeletes || requestsMade.Load() != 0 {
		t.Fatal("terminal recovery changed retained ownership, replayed or incorrectly cleared operation")
	}
	// Config and journal events may be duplicated after restart; completion must
	// remain local, preserve all creator/tombstone rows and never resubmit HTTP.
	if !refused && mode != "terminal-unknown" && mode != "terminal-wrong-request" {
		if handled, err := (&configReconciler{a: a}).recoverTerminalConfiguration(ctx, c.Name); handled || err != nil {
			t.Fatal("completed journal recovered twice", err)
		}
	}
}

// Inject a concurrent journal phase change after route selection and before
// shared finishing reads its authoritative receipt/CAS snapshot.
type terminalPnPPhaseRace struct {
	ctrlclient.Reader
	writer ctrlclient.Client
	key    ctrlclient.ObjectKey
	reads  int
	target bool
}

func (r *terminalPnPPhaseRace) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	n, ok := obj.(*cvk.NetworkController)
	if !ok || key != r.key {
		return nil
	}
	r.reads++
	if r.reads == 2 {
		if r.target {
			n.Status.Configuration.Operation.TargetID = "foreign-target"
		} else {
			n.Status.Configuration.Operation.Phase = "Prepared"
			n.Status.Configuration.Operation.SubmittedAt = nil
		}
		if err := r.writer.Status().Update(ctx, n); err != nil {
			return err
		}
		return r.Reader.Get(ctx, key, obj, opts...)
	}
	return nil
}
