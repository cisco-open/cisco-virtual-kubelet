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

package mutationguard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
)

const testBaseTTL = LegacyIOSXEQuarantineTTL

func guardScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add ops scheme: %v", err)
	}
	return scheme
}

func legacyUpgrade(name, namespace, device string) *opsv1alpha1.IOSXESoftwareUpgrade {
	return &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(name + "-uid")},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device},
		},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{Phase: opsv1alpha1.UpgradePhaseActivating},
	}
}

func legacyAction(name, namespace, device string, phase opsv1alpha1.ActionPhase, kind opsv1alpha1.ActionKind, now time.Time) *opsv1alpha1.IOSXEOperationalAction {
	act := &opsv1alpha1.IOSXEOperationalAction{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               types.UID(name + "-uid"),
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
		Spec: opsv1alpha1.IOSXEOperationalActionSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device},
			Action:    opsv1alpha1.ActionRequest{Kind: kind},
		},
		Status: opsv1alpha1.IOSXEOperationalActionStatus{
			Phase:        phase,
			InvocationID: name + "-invocation",
		},
	}
	started := metav1.NewTime(now)
	act.Status.StartTime = &started
	switch kind {
	case opsv1alpha1.ActionKindReboot:
		act.Spec.Action.Reboot = &opsv1alpha1.RebootActionArgs{DelaySeconds: 7 * 24 * 60 * 60}
	case opsv1alpha1.ActionKindFactoryReset:
		act.Spec.Action.FactoryReset = &opsv1alpha1.FactoryResetArgs{}
	case opsv1alpha1.ActionKindFilePut:
		act.Spec.Action.FilePut = &opsv1alpha1.FilePutArgs{Path: "flash:test", ConfigMapName: "payload"}
	}
	return act
}

func TestFindCanonicalRiskIsCrossKindDeterministicAndScoped(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	upgrade := legacyUpgrade("a-upgrade", "default", "dev1")
	action := legacyAction("z-action", "default", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindFilePut, now)
	otherNamespace := legacyAction("a-foreign-namespace", "other", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindFilePut, now)
	otherDevice := legacyUpgrade("a-foreign-device", "default", "dev2")

	for i, objects := range [][]client.Object{
		{upgrade, action, otherNamespace, otherDevice},
		{otherDevice.DeepCopy(), otherNamespace.DeepCopy(), action.DeepCopy(), upgrade.DeepCopy()},
	} {
		c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(objects...).Build()
		risk, err := FindCanonicalRisk(context.Background(), c, "default", "dev1", now)
		if err != nil {
			t.Fatalf("order %d FindCanonicalRisk: %v", i, err)
		}
		if risk == nil || risk.Kind != "IOSXEOperationalAction" || risk.Name != action.Name {
			t.Fatalf("order %d risk=%+v, want deterministic action risk", i, risk)
		}
	}
}

func TestFindCanonicalRiskIncludesDeletingObject(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	upgrade := legacyUpgrade("deleting-upgrade", "default", "dev1")
	deleting := metav1.NewTime(now)
	upgrade.DeletionTimestamp = &deleting
	upgrade.Finalizers = []string{"ops.cisco.vk/test"}
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(upgrade).Build()

	risk, err := FindCanonicalRisk(context.Background(), c, "default", "dev1", now)
	if err != nil {
		t.Fatalf("FindCanonicalRisk: %v", err)
	}
	if risk == nil || risk.Name != upgrade.Name {
		t.Fatalf("deleting risk=%+v, want %s", risk, upgrade.Name)
	}
}

func TestLegacyActionQuarantineWindows(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sevenDays := 7 * 24 * time.Hour
	tests := []struct {
		name string
		act  *opsv1alpha1.IOSXEOperationalAction
		want time.Duration
		ok   bool
	}{
		{name: "running delayed reboot", act: legacyAction("running-reboot", "default", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindReboot, now), want: sevenDays + testBaseTTL, ok: true},
		{name: "failed delayed reboot", act: legacyAction("failed-reboot", "default", "dev1", opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionKindReboot, now), want: sevenDays + testBaseTTL, ok: true},
		{name: "succeeded delayed reboot", act: legacyAction("succeeded-reboot", "default", "dev1", opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionKindReboot, now), want: sevenDays + testBaseTTL, ok: true},
		{name: "succeeded factory reset", act: legacyAction("succeeded-reset", "default", "dev1", opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionKindFactoryReset, now), want: testBaseTTL, ok: true},
		{name: "failed file put", act: legacyAction("failed-put", "default", "dev1", opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionKindFilePut, now), want: testBaseTTL, ok: true},
		{name: "harmless succeeded file put", act: legacyAction("succeeded-put", "default", "dev1", opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionKindFilePut, now), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := actionQuarantineTTL(tt.act, now)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("actionQuarantineTTL=(%s,%t), want (%s,%t)", got, ok, tt.want, tt.ok)
			}
		})
	}

	elapsed := legacyAction("elapsed", "default", "dev1", opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionKindFilePut, now.Add(-testBaseTTL-time.Second))
	if ttl, ok := actionQuarantineTTL(elapsed, now); ok || ttl != 0 {
		t.Fatalf("elapsed action remains risky: ttl=%s ok=%t", ttl, ok)
	}

	latest := legacyAction("latest-anchor", "default", "dev1", opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionKindFilePut, now.Add(-time.Hour))
	completed := metav1.NewTime(now)
	latest.Status.CompletionTime = &completed
	if ttl, ok := actionQuarantineTTL(latest, now); !ok || ttl != testBaseTTL {
		t.Fatalf("completion-time anchor ttl=(%s,%t), want (%s,true)", ttl, ok, testBaseTTL)
	}

	future := legacyAction("future-anchor", "default", "dev1", opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionKindFilePut, now)
	futureCompletion := metav1.NewTime(now.Add(100 * 365 * 24 * time.Hour))
	future.Status.CompletionTime = &futureCompletion
	if ttl, ok := actionQuarantineTTL(future, now); !ok || ttl != testBaseTTL {
		t.Fatalf("future completion ttl=(%s,%t), want capped (%s,true)", ttl, ok, testBaseTTL)
	}
}

func TestLegacyUpgradeQuarantineWindows(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	terminal := func(name string, phase opsv1alpha1.UpgradePhase, completed time.Time) *opsv1alpha1.IOSXESoftwareUpgrade {
		up := legacyUpgrade(name, "default", "dev1")
		up.CreationTimestamp = metav1.NewTime(completed.Add(-time.Hour))
		up.Status.Phase = phase
		completion := metav1.NewTime(completed)
		up.Status.CompletionTime = &completion
		return up
	}
	tests := []struct {
		name string
		up   *opsv1alpha1.IOSXESoftwareUpgrade
		want time.Duration
		ok   bool
	}{
		{name: "failed", up: terminal("failed", opsv1alpha1.UpgradePhaseFailed, now), want: testBaseTTL, ok: true},
		{name: "validation failed", up: terminal("validation", opsv1alpha1.UpgradePhaseValidationFailed, now), want: testBaseTTL, ok: true},
		{name: "reboot timeout", up: terminal("reboot", opsv1alpha1.UpgradePhaseRebootTimeout, now), want: testBaseTTL, ok: true},
		{name: "preflight failed", up: terminal("preflight", opsv1alpha1.UpgradePhasePreflightFailed, now), ok: false},
		{name: "succeeded", up: terminal("succeeded", opsv1alpha1.UpgradePhaseSucceeded, now), ok: false},
		{name: "rolled back", up: terminal("rolled-back", opsv1alpha1.UpgradePhaseRolledBack, now), ok: false},
		{name: "cancelled", up: terminal("cancelled", opsv1alpha1.UpgradePhaseCancelled, now), ok: false},
		{name: "unknown markerless phase", up: terminal("future", opsv1alpha1.UpgradePhase("FutureMutating"), now), want: testBaseTTL, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := upgradeQuarantineTTL(tt.up, now)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("upgradeQuarantineTTL=(%s,%t), want (%s,%t)", got, ok, tt.want, tt.ok)
			}
		})
	}

	elapsed := terminal("elapsed", opsv1alpha1.UpgradePhaseFailed, now.Add(-testBaseTTL-time.Second))
	if ttl, ok := upgradeQuarantineTTL(elapsed, now); ok || ttl != 0 {
		t.Fatalf("elapsed terminal upgrade remains risky: ttl=%s ok=%t", ttl, ok)
	}

	future := terminal("future-anchor", opsv1alpha1.UpgradePhaseFailed, now.Add(100*365*24*time.Hour))
	if ttl, ok := upgradeQuarantineTTL(future, now); !ok || ttl != testBaseTTL {
		t.Fatalf("future completion ttl=(%s,%t), want capped (%s,true)", ttl, ok, testBaseTTL)
	}
}

func TestEnsureCanonicalQuarantineUsesCanonicalDelayedActionTTL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	action := legacyAction("delayed-reboot", "default", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindReboot, now)
	upgrade := legacyUpgrade("new-upgrade", "default", "dev1")
	upgrade.Status.ExecutionModel = opsv1alpha1.UpgradeExecutionModelAtMostOnceV1
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(action, upgrade).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "default", TTL: testBaseTTL}
	deviceKey := devicecoordination.DeviceKey("default", "dev1")

	result, err := EnsureCanonicalQuarantine(context.Background(), c, leaser, "default", "dev1", deviceKey, UpgradeHolderIdentity(upgrade), now)
	if err != nil {
		t.Fatalf("EnsureCanonicalQuarantine: %v", err)
	}
	if result.Risk == nil || result.Risk.Name != action.Name || result.CallerOwnsRisk {
		t.Fatalf("result=%+v, want delayed action owned on its own behalf", result)
	}
	var lease coordv1.Lease
	name := engine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &lease); err != nil {
		t.Fatalf("get quarantine Lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != ActionHolderIdentity(action) {
		t.Fatalf("holder=%v, want %q", lease.Spec.HolderIdentity, ActionHolderIdentity(action))
	}
	wantSeconds := int32((7*24*time.Hour + testBaseTTL) / time.Second)
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds != wantSeconds {
		t.Fatalf("lease duration=%v, want %d", lease.Spec.LeaseDurationSeconds, wantSeconds)
	}
}

type failingListReader struct {
	client.Reader
}

func (f failingListReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("injected list failure")
}

func TestEnsureCanonicalQuarantineFailsClosedOnAPIReaderError(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "default", TTL: testBaseTTL}
	_, err := EnsureCanonicalQuarantine(context.Background(), failingListReader{Reader: c}, leaser,
		"default", "dev1", devicecoordination.DeviceKey("default", "dev1"), "caller", now)
	if err == nil || !strings.Contains(err.Error(), "injected list failure") {
		t.Fatalf("error=%v, want API reader failure", err)
	}
}

func TestConcurrentGuardCallersChooseOneCanonicalHolder(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	action := legacyAction("action", "default", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindFilePut, now)
	upgrade := legacyUpgrade("upgrade", "default", "dev1")
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(action, upgrade).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "default", TTL: testBaseTTL}
	deviceKey := devicecoordination.DeviceKey("default", "dev1")

	identities := []string{ActionHolderIdentity(action), UpgradeHolderIdentity(upgrade)}
	var wg sync.WaitGroup
	errs := make(chan error, len(identities))
	for _, identity := range identities {
		identity := identity
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := EnsureCanonicalQuarantine(context.Background(), c, leaser, "default", "dev1", deviceKey, identity, now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent guard: %v", err)
		}
	}
	var lease coordv1.Lease
	name := engine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &lease); err != nil {
		t.Fatalf("get quarantine Lease: %v", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != ActionHolderIdentity(action) {
		t.Fatalf("holder=%v, want canonical %q", lease.Spec.HolderIdentity, ActionHolderIdentity(action))
	}
}

func TestDeletionQuarantineBlocksLiveAndTakesOverExpiredForeignLease(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		name      string
		renewTime time.Time
		owned     bool
	}{
		{name: "live", renewTime: now, owned: false},
		{name: "expired", renewTime: now.Add(-2 * time.Hour), owned: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action := legacyAction("deleting-action", "default", "dev1", opsv1alpha1.ActionPhaseRunning, opsv1alpha1.ActionKindFilePut, now)
			deleting := metav1.NewTime(now)
			action.DeletionTimestamp = &deleting
			action.Finalizers = []string{"ops.cisco.vk/test"}
			deviceKey := devicecoordination.DeviceKey("default", "dev1")
			leaseName := engine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily)
			duration := int32(time.Hour / time.Second)
			holder := "foreign-holder"
			renew := metav1.NewMicroTime(tt.renewTime)
			lease := &coordv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "default"},
				Spec: coordv1.LeaseSpec{
					HolderIdentity:       &holder,
					LeaseDurationSeconds: &duration,
					RenewTime:            &renew,
				},
			}
			c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithObjects(action, lease).Build()
			leaser := &engine.FamilyLeaser{Client: c, Namespace: "default", TTL: testBaseTTL}
			result, err := EnsureCanonicalQuarantineForDeletion(context.Background(), c, leaser,
				"default", "dev1", deviceKey, ActionHolderIdentity(action), now)
			if err != nil {
				t.Fatalf("EnsureCanonicalQuarantineForDeletion: %v", err)
			}
			if result.CallerOwnsRisk != tt.owned {
				t.Fatalf("CallerOwnsRisk=%t, want %t (result=%+v)", result.CallerOwnsRisk, tt.owned, result)
			}
			var got coordv1.Lease
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: leaseName}, &got); err != nil {
				t.Fatalf("get Lease: %v", err)
			}
			wantHolder := holder
			if tt.owned {
				wantHolder = ActionHolderIdentity(action)
			}
			if got.Spec.HolderIdentity == nil || *got.Spec.HolderIdentity != wantHolder {
				t.Fatalf("holder=%v, want %q", got.Spec.HolderIdentity, wantHolder)
			}
		})
	}
}
