// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This measures the real controller's fleet-read substep, not the complete
// sustained campaign/controller scale gate. No synthetic member is healthy
// enough to grant a mutation, and no worker/device transport exists here.
func TestEnvtest_FleetAssessmentFreshSnapshot(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{findControllerCRDPath(t)}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	cfg.QPS, cfg.Burst = 1000, 1000
	c, err := client.New(cfg, client.Options{Scheme: drainTestScheme(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "fleet"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		d, n := fleetReadFixture(i)
		d.UID, n.UID = "", ""
		d.Spec.Driver, d.Spec.Address, d.Spec.Username = "XE", "192.0.2.1", "fixture"
		d.Status = ciskov1.DeviceStatus{}
		if err := c.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := c.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		bindFleetReadFixture(d, n)
		if err := c.Update(ctx, n); err != nil {
			t.Fatal(err)
		}
		n.Status.NodeInfo.MachineID, n.Status.NodeInfo.SystemUUID = d.Spec.PhysicalIdentity, d.Spec.PhysicalIdentity
		if err := c.Status().Update(ctx, n); err != nil {
			t.Fatal(err)
		}
		d.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
			EffectiveLabelHash: "sha256:" + strings.Repeat("a", 64), SourceResourceVersion: d.ResourceVersion,
			LastSuccessfulTime: metav1.Now(),
		}
		if err := c.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	rd := &fleetCountingReader{Reader: c}
	r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: rd}
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}
	for _, targets := range []int{1, 10, 50, 100} {
		t.Run(fmt.Sprintf("targets-%03d", targets), func(t *testing.T) {
			campaign := &ops.IOSXESoftwareRollout{}
			campaign.Status.Targets = make([]ops.IOSXESoftwareRolloutTargetStatus, targets)
			latencies := make([]time.Duration, 20)
			for sample := range latencies {
				rd.requests = 0
				start := time.Now()
				members, _, err := r.currentFleetMembers(ctx, campaign, policy, topologyrollout.Policy{})
				latencies[sample] = time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
				if len(members) != 1000 || rd.requests != 2 {
					t.Fatalf("members=%d API requests=%d", len(members), rd.requests)
				}
				for _, member := range members {
					if member.Healthy || member.HealthKnown {
						t.Fatal("unknown health became healthy")
					}
				}
			}
			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			t.Logf("fleet=1000 targets=%d samples=20 requests=2 fleetRead p50=%s p95=%s p99=%s", targets, latencies[9], latencies[18], latencies[19])
		})
	}
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: "fleet-0999"}, &node); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, &node); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.currentFleetMembers(ctx, &ops.IOSXESoftwareRollout{}, policy, topologyrollout.Policy{}); err == nil {
		t.Fatal("deleted fleet Node was served from a stale snapshot")
	}
}
