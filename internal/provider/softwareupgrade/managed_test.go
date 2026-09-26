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

package softwareupgrade

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

const (
	managedTestDeviceNamespace  = "default"
	managedTestDeviceName       = "dev1"
	managedTestDeviceUID        = "device-uid-1"
	managedTestNodeName         = "edge-node-1"
	managedTestNodeUID          = "node-uid-1"
	managedTestLeafUID          = "leaf-uid-1"
	managedTestWorkerUsername   = "system:serviceaccount:default:cisco-vk-dev1-uid"
	managedTestCampaignUID      = "campaign-uid-1"
	managedTestLedgerUID        = "ledger-uid-1"
	managedTestReservationID    = "reservation-1"
	managedTestDeviceGeneration = int64(7)
	managedTestWorkerRevision   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	managedTestWorkerPodUID     = "pod-uid-1"
	managedTestPhysicalIdentity = "serial-1"
)

var managedTestTime = time.Unix(1_700_000_000, 0).UTC()

type podListErrorReader struct {
	client.Reader
}

func (r podListErrorReader) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	if _, ok := list.(*corev1.PodList); ok {
		return errors.New("pod authorization unavailable")
	}
	return r.Reader.List(ctx, list, opts...)
}

type upgradeListErrorReader struct {
	client.Reader
}

func (r upgradeListErrorReader) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	if _, ok := list.(*opsv1alpha1.IOSXESoftwareUpgradeList); ok {
		return errors.New("upgrade list authorization unavailable")
	}
	return r.Reader.List(ctx, list, opts...)
}

func managedTestNode() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: managedTestNodeName,
		UID:  types.UID(managedTestNodeUID),
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:                "true",
			managedprotocol.AnnotationDeviceNamespace:        managedTestDeviceNamespace,
			managedprotocol.AnnotationDeviceName:             managedTestDeviceName,
			managedprotocol.AnnotationDeviceUID:              managedTestDeviceUID,
			managedprotocol.AnnotationNodeUID:                managedTestNodeUID,
			managedprotocol.AnnotationWorkerUsername:         managedTestWorkerUsername,
			managedprotocol.AnnotationWorkerProtocol:         managedprotocol.Version,
			managedprotocol.AnnotationWorkerObservedRevision: managedTestWorkerRevision,
		},
	}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{
		MachineID: managedTestPhysicalIdentity, SystemUUID: managedTestPhysicalIdentity,
	}}}
}

func managedTestDevice() *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  managedTestDeviceNamespace,
			Name:       managedTestDeviceName,
			UID:        types.UID(managedTestDeviceUID),
			Generation: managedTestDeviceGeneration,
		},
		Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, PhysicalIdentity: managedTestPhysicalIdentity},
		Status: ciskov1.DeviceStatus{
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				NodeName: managedTestNodeName, NodeUID: managedTestNodeUID, DeviceUID: managedTestDeviceUID,
				PhysicalIdentity: managedTestPhysicalIdentity,
			},
			WorkerRevision: &ciskov1.DeviceWorkerRevisionStatus{
				DesiredRevision:      managedTestWorkerRevision,
				ObservedRevision:     managedTestWorkerRevision,
				DeploymentUID:        "deployment-uid-1",
				DeploymentGeneration: 1,
				PodUID:               managedTestWorkerPodUID,
				PodStartTime:         &metav1.Time{Time: managedTestTime.Add(-time.Minute)},
				ReadyHeartbeatTime:   &metav1.Time{Time: managedTestTime},
				ObservedAt:           metav1.Time{Time: managedTestTime},
			},
		},
	}
}

func managedTestLeaf(name string) *opsv1alpha1.IOSXESoftwareUpgrade {
	controlRevision := int64(7)
	up := newUpgrade(name, func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.UID = types.UID(managedTestLeafUID + "-" + name)
		up.Annotations = map[string]string{
			managedprotocol.AnnotationManaged:          "true",
			managedprotocol.AnnotationWorkerUsername:   managedTestWorkerUsername,
			managedprotocol.AnnotationCampaignUID:      managedTestCampaignUID,
			managedprotocol.AnnotationPlanHash:         "sha256:" + strings.Repeat("a", 64),
			managedprotocol.AnnotationLedgerUID:        managedTestLedgerUID,
			managedprotocol.AnnotationReservationID:    managedTestReservationID,
			managedprotocol.AnnotationDeviceNamespace:  managedTestDeviceNamespace,
			managedprotocol.AnnotationDeviceName:       managedTestDeviceName,
			managedprotocol.AnnotationDeviceUID:        managedTestDeviceUID,
			managedprotocol.AnnotationDeviceGeneration: strconv.FormatInt(managedTestDeviceGeneration, 10),
			managedprotocol.AnnotationNodeName:         managedTestNodeName,
			managedprotocol.AnnotationNodeUID:          managedTestNodeUID,
			managedprotocol.AnnotationWorkerProtocol:   managedprotocol.Version,
		}
		up.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
			ProtocolVersion:       opsv1alpha1.ManagedUpgradeProtocolVersion(managedprotocol.Version),
			State:                 opsv1alpha1.UpgradeManagerAdmissionGranted,
			CampaignUID:           managedTestCampaignUID,
			PlanHash:              up.Annotations[managedprotocol.AnnotationPlanHash],
			PolicyUID:             "policy-uid-1",
			PolicyResourceVersion: "17",
			PolicyEpoch:           1,
			LedgerUID:             managedTestLedgerUID,
			ReservationID:         managedTestReservationID,
			TopologyLockID:        strings.Repeat("1", 32),
			LeafUID:               string(up.UID),
			DeviceUID:             managedTestDeviceUID,
			DeviceGeneration:      managedTestDeviceGeneration,
			PhysicalIdentity:      managedTestPhysicalIdentity,
			NodeUID:               managedTestNodeUID,
			ControlRevision:       &controlRevision,
			UpdatedAt:             metav1.Time{Time: managedTestTime},
		}
		up.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
			Revision:  controlRevision,
			UpdatedAt: metav1.Time{Time: managedTestTime},
		}
	})
	return up
}

func newManagedTestReconciler(
	t *testing.T,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	node *corev1.Node,
	extra ...client.Object,
) *Reconciler {
	t.Helper()
	if node == nil {
		node = managedTestNode()
	}
	objects := []client.Object{up, node, managedTestDevice()}
	objects = append(objects, extra...)
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objects...).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(object client.Object) []string {
			pod := object.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}).
		Build()
	return &Reconciler{
		Client:          c,
		Reader:          c,
		DeviceName:      managedTestDeviceName,
		DeviceNamespace: managedTestDeviceNamespace,
		DeviceUID:       managedTestDeviceUID,
		NodeName:        managedTestNodeName,
		ManagedTopology: true,
		WorkerRevision:  managedTestWorkerRevision,
		WorkerPodUID:    managedTestWorkerPodUID,
		DevicePodLister: func(context.Context) ([]*corev1.Pod, error) { return nil, nil },
		Now:             func() time.Time { return managedTestTime },
	}
}

func TestManagedLeafRejectsPredecessorPodUsingReplacementProof(t *testing.T) {
	up := managedTestLeaf("predecessor-pod")
	r := newManagedTestReconciler(t, up, nil)
	r.WorkerPodUID = "terminating-predecessor-pod-uid"

	decision := r.evaluateManagedLeaf(context.Background(), up)
	if decision.allowProgress || decision.allowClaim ||
		!strings.Contains(decision.message, "ready worker proof") {
		t.Fatalf("predecessor Pod reused replacement proof: %+v", decision)
	}
}

func TestManagedLeafGateAcknowledgesExactBinding(t *testing.T) {
	up := managedTestLeaf("gate-ready")
	r := newManagedTestReconciler(t, up, nil)

	decision, updated, err := r.syncManagedLeafGate(context.Background(), up, managedTestTime)
	if err != nil {
		t.Fatalf("syncManagedLeafGate() error = %v", err)
	}
	if !updated || !decision.applies || !decision.allowProgress || !decision.allowClaim {
		t.Fatalf("decision = %+v, updated=%t; want ready acknowledgement", decision, updated)
	}
	if up.Status.WorkerControl == nil ||
		up.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady ||
		up.Status.WorkerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionGranted ||
		up.Status.WorkerControl.ObservedControlRevision != 7 {
		t.Fatalf("worker acknowledgement = %#v", up.Status.WorkerControl)
	}

	_, updated, err = r.syncManagedLeafGate(context.Background(), up, managedTestTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("second syncManagedLeafGate() error = %v", err)
	}
	if updated {
		t.Fatal("unchanged manager control rewrote worker acknowledgement")
	}
}

func TestManagedLeafRequiresEveryManagedAnnotation(t *testing.T) {
	keys := []string{
		managedprotocol.AnnotationManaged,
		managedprotocol.AnnotationWorkerUsername,
		managedprotocol.AnnotationCampaignUID,
		managedprotocol.AnnotationPlanHash,
		managedprotocol.AnnotationLedgerUID,
		managedprotocol.AnnotationReservationID,
		managedprotocol.AnnotationDeviceNamespace,
		managedprotocol.AnnotationDeviceName,
		managedprotocol.AnnotationDeviceUID,
		managedprotocol.AnnotationDeviceGeneration,
		managedprotocol.AnnotationNodeName,
		managedprotocol.AnnotationNodeUID,
		managedprotocol.AnnotationWorkerProtocol,
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			up := managedTestLeaf("missing-annotation")
			delete(up.Annotations, key)
			r := newManagedTestReconciler(t, up, nil)
			if err := r.validateManagedLeafBinding(context.Background(), up); err == nil {
				t.Fatalf("missing annotation %q was accepted", key)
			}
		})
	}
}

func TestManagedLeafRejectsEveryManagedAnnotationMismatch(t *testing.T) {
	keys := []string{
		managedprotocol.AnnotationManaged,
		managedprotocol.AnnotationWorkerUsername,
		managedprotocol.AnnotationCampaignUID,
		managedprotocol.AnnotationPlanHash,
		managedprotocol.AnnotationLedgerUID,
		managedprotocol.AnnotationReservationID,
		managedprotocol.AnnotationDeviceNamespace,
		managedprotocol.AnnotationDeviceName,
		managedprotocol.AnnotationDeviceUID,
		managedprotocol.AnnotationDeviceGeneration,
		managedprotocol.AnnotationNodeName,
		managedprotocol.AnnotationNodeUID,
		managedprotocol.AnnotationWorkerProtocol,
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			up := managedTestLeaf("mismatched-annotation")
			up.Annotations[key] = "wrong"
			r := newManagedTestReconciler(t, up, nil)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if decision.allowProgress || decision.allowClaim {
				t.Fatalf("mismatched annotation %q was accepted: %+v", key, decision)
			}
		})
	}
}

func TestManagedLeafRejectsAdmissionAndNodeIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*opsv1alpha1.IOSXESoftwareUpgrade, *corev1.Node)
	}{
		{name: "leaf namespace", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Namespace = "other"
		}},
		{name: "leaf device ref", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Spec.DeviceRef.Name = "other"
		}},
		{name: "missing admission", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission = nil
		}},
		{name: "protocol", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.ProtocolVersion = "future"
		}},
		{name: "leaf UID", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.LeafUID = "recreated-leaf"
		}},
		{name: "device UID", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.DeviceUID = "recreated-device"
		}},
		{name: "device generation", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.DeviceGeneration++
		}},
		{name: "node UID", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.NodeUID = "recreated-node"
		}},
		{name: "campaign", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.CampaignUID = "other-campaign"
		}},
		{name: "plan", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.PlanHash = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "ledger", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.LedgerUID = "other-ledger"
		}},
		{name: "reservation", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.ReservationID = "other-reservation"
		}},
		{name: "missing policy UID", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.PolicyUID = ""
		}},
		{name: "missing physical identity", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.PhysicalIdentity = ""
		}},
		{name: "missing admitted revision", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerAdmission.ControlRevision = nil
		}},
		{name: "negative admitted revision", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			revision := int64(-1)
			up.Status.ManagerAdmission.ControlRevision = &revision
		}},
		{name: "stale control", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, _ *corev1.Node) {
			up.Status.ManagerControl.Revision = 6
		}},
		{name: "node device binding", mutate: func(_ *opsv1alpha1.IOSXESoftwareUpgrade, node *corev1.Node) {
			node.Annotations[managedprotocol.AnnotationDeviceUID] = "recreated-device"
		}},
		{name: "node protocol", mutate: func(_ *opsv1alpha1.IOSXESoftwareUpgrade, node *corev1.Node) {
			node.Annotations[managedprotocol.AnnotationWorkerProtocol] = "future"
		}},
		{name: "node username", mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade, node *corev1.Node) {
			node.Annotations[managedprotocol.AnnotationWorkerUsername] = "system:serviceaccount:other:worker"
			up.Annotations[managedprotocol.AnnotationWorkerUsername] = node.Annotations[managedprotocol.AnnotationWorkerUsername]
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := managedTestLeaf("binding-" + strings.ReplaceAll(tt.name, " ", "-"))
			node := managedTestNode()
			tt.mutate(up, node)
			r := newManagedTestReconciler(t, up, node)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if decision.allowProgress || decision.allowClaim ||
				decision.effectiveState != opsv1alpha1.UpgradeWorkerControlDenied {
				t.Fatalf("mismatched managed identity was accepted: %+v", decision)
			}
		})
	}
}

func TestManagedLeafRejectsLiveCiscoDeviceSpecDrift(t *testing.T) {
	for _, drift := range []string{"UID", "generation", "driver"} {
		t.Run(drift, func(t *testing.T) {
			up := managedTestLeaf("device-drift-" + strings.ToLower(drift))
			r := newManagedTestReconciler(t, up, nil)
			var device ciskov1.CiscoDevice
			if err := r.Client.Get(context.Background(), client.ObjectKey{
				Namespace: managedTestDeviceNamespace, Name: managedTestDeviceName,
			}, &device); err != nil {
				t.Fatal(err)
			}
			switch drift {
			case "UID":
				device.UID = "replacement-device-uid"
			case "generation":
				device.Generation++
			case "driver":
				device.Spec.Driver = ciskov1.DeviceDriverNXOS
			}
			if err := r.Client.Update(context.Background(), &device); err != nil {
				t.Fatal(err)
			}
			if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress || decision.allowClaim {
				t.Fatalf("live CiscoDevice %s drift was accepted: %+v", drift, decision)
			}
		})
	}
}

func TestManagedLeafRequiresUncachedAPIReader(t *testing.T) {
	up := managedTestLeaf("missing-api-reader")
	r := newManagedTestReconciler(t, up, nil)
	r.Reader = nil
	decision := r.evaluateManagedLeaf(context.Background(), up)
	if decision.allowProgress || decision.allowClaim ||
		!strings.Contains(decision.message, "uncached Kubernetes API reader") {
		t.Fatalf("managed leaf without APIReader was not denied: %+v", decision)
	}
}

func TestManagedLeafSourceSecretIncarnation(t *testing.T) {
	newSource := func(name, annotatedUID string) (*opsv1alpha1.IOSXESoftwareUpgrade, *corev1.Secret) {
		up := managedTestLeaf(name)
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:          "sftp://images.example/cat9k.bin",
			SHA256:       strings.Repeat("a", 64),
			URLSecretRef: &corev1.LocalObjectReference{Name: "image-source"},
		}
		if annotatedUID != "" {
			up.Annotations[managedprotocol.AnnotationSourceSecretUID] = annotatedUID
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace:       managedTestDeviceNamespace,
			Name:            "image-source",
			UID:             types.UID("secret-uid-1"),
			ResourceVersion: "23",
			Labels:          map[string]string{URLSecretPurposeLabel: URLSecretPurposeValue},
		}, Data: map[string][]byte{
			URLSecretAllowedSchemeKey: []byte("sftp"),
			URLSecretAllowedHostKey:   []byte("images.example"),
			URLSecretAllowedPortKey:   []byte("22"),
		}}
		return up, secret
	}

	t.Run("exact live incarnation", func(t *testing.T) {
		up, secret := newSource("secret-exact", "secret-uid-1")
		r := newManagedTestReconciler(t, up, nil, secret)
		if decision := r.evaluateManagedLeaf(context.Background(), up); !decision.allowClaim {
			t.Fatalf("exact Secret incarnation denied: %+v", decision)
		}
	})
	t.Run("missing UID annotation", func(t *testing.T) {
		up, secret := newSource("secret-missing-uid", "")
		r := newManagedTestReconciler(t, up, nil, secret)
		if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress {
			t.Fatalf("missing Secret UID annotation accepted: %+v", decision)
		}
	})
	t.Run("recreated Secret", func(t *testing.T) {
		up, secret := newSource("secret-recreated", "old-secret-uid")
		r := newManagedTestReconciler(t, up, nil, secret)
		if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress {
			t.Fatalf("recreated Secret accepted: %+v", decision)
		}
	})
	t.Run("same UID credential rotation", func(t *testing.T) {
		up, secret := newSource("secret-rotated", "secret-uid-1")
		secret.ResourceVersion = "24"
		secret.Data["password"] = []byte("rotated")
		r := newManagedTestReconciler(t, up, nil, secret)
		if decision := r.evaluateManagedLeaf(context.Background(), up); !decision.allowClaim {
			t.Fatalf("same-UID Secret rotation denied: %+v", decision)
		}
	})
	t.Run("same UID endpoint authorization change", func(t *testing.T) {
		up, secret := newSource("secret-endpoint-change", "secret-uid-1")
		secret.ResourceVersion = "24"
		secret.Data[URLSecretAllowedHostKey] = []byte("attacker.example")
		r := newManagedTestReconciler(t, up, nil, secret)
		if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress ||
			!strings.Contains(decision.message, "does not authorize") {
			t.Fatalf("same-UID endpoint authorization change accepted: %+v", decision)
		}
	})
	t.Run("uncached reader is authoritative", func(t *testing.T) {
		up, cachedSecret := newSource("secret-uncached", "secret-uid-1")
		r := newManagedTestReconciler(t, up, nil, cachedSecret)
		recreated := cachedSecret.DeepCopy()
		recreated.UID = "replacement-secret-uid"
		recreated.ResourceVersion = "24"
		r.Reader = fake.NewClientBuilder().
			WithScheme(newScheme(t)).
			WithObjects(managedTestNode(), managedTestDevice(), recreated).
			Build()
		if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress {
			t.Fatalf("stale cached Secret UID won over APIReader: %+v", decision)
		}
	})
	t.Run("HTTPS omits Secret binding", func(t *testing.T) {
		up := managedTestLeaf("https-no-secret")
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{
			URL:    "https://images.example/cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		}
		r := newManagedTestReconciler(t, up, nil)
		if decision := r.evaluateManagedLeaf(context.Background(), up); !decision.allowClaim {
			t.Fatalf("HTTPS leaf without Secret denied: %+v", decision)
		}
		up.Annotations[managedprotocol.AnnotationSourceSecretUID] = "unexpected"
		if decision := r.evaluateManagedLeaf(context.Background(), up); decision.allowProgress {
			t.Fatalf("orphan source Secret UID annotation accepted: %+v", decision)
		}
	})
}

func TestManagedLeafFreshRuntimeSecretRevisionFence(t *testing.T) {
	up := managedTestLeaf("runtime-secret-rotation")
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: managedTestDeviceNamespace, Name: "device-credentials",
		UID: "credential-secret-uid", ResourceVersion: "24",
	}}
	r := newManagedTestReconciler(t, up, nil, secret)
	var device ciskov1.CiscoDevice
	key := client.ObjectKey{Namespace: managedTestDeviceNamespace, Name: managedTestDeviceName}
	if err := r.Client.Get(context.Background(), key, &device); err != nil {
		t.Fatal(err)
	}
	device.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: secret.Name}
	if err := r.Client.Update(context.Background(), &device); err != nil {
		t.Fatal(err)
	}

	r.CredentialSecretRevision = "23"
	decision := r.evaluateManagedLeaf(context.Background(), up)
	if decision.allowProgress || decision.allowClaim ||
		!strings.Contains(decision.message, "does not match runtime revision") {
		t.Fatalf("old worker accepted after credential rotation: %+v", decision)
	}

	r.CredentialSecretRevision = secret.ResourceVersion
	decision = r.evaluateManagedLeaf(context.Background(), up)
	if !decision.allowProgress || !decision.allowClaim {
		t.Fatalf("replacement worker with exact Secret revision denied: %+v", decision)
	}
}

func TestManagedLeafRuntimeSecretUsesUncachedReader(t *testing.T) {
	up := managedTestLeaf("runtime-secret-uncached")
	cachedSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: managedTestDeviceNamespace, Name: "device-credentials",
		UID: "credential-secret-uid", ResourceVersion: "23",
	}}
	r := newManagedTestReconciler(t, up, nil, cachedSecret)
	var cachedDevice ciskov1.CiscoDevice
	key := client.ObjectKey{Namespace: managedTestDeviceNamespace, Name: managedTestDeviceName}
	if err := r.Client.Get(context.Background(), key, &cachedDevice); err != nil {
		t.Fatal(err)
	}
	cachedDevice.Spec.CredentialSecretRef = &corev1.LocalObjectReference{Name: cachedSecret.Name}
	if err := r.Client.Update(context.Background(), &cachedDevice); err != nil {
		t.Fatal(err)
	}
	freshDevice := cachedDevice.DeepCopy()
	freshSecret := cachedSecret.DeepCopy()
	freshSecret.ResourceVersion = "24"
	r.Reader = fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
		managedTestNode(), freshDevice, freshSecret,
	).Build()
	r.CredentialSecretRevision = cachedSecret.ResourceVersion
	decision := r.evaluateManagedLeaf(context.Background(), up)
	if decision.allowProgress || decision.allowClaim ||
		!strings.Contains(decision.message, "resourceVersion \"24\"") {
		t.Fatalf("cached Secret revision won over APIReader: %+v", decision)
	}
}

func TestManagedControlBlocksNewClaimsButAllowsClaimObservation(t *testing.T) {
	tests := []struct {
		name      string
		state     opsv1alpha1.UpgradeManagerAdmissionState
		pause     bool
		cancel    bool
		effective opsv1alpha1.UpgradeWorkerControlState
	}{
		{
			name:      "paused",
			state:     opsv1alpha1.UpgradeManagerAdmissionGranted,
			pause:     true,
			effective: opsv1alpha1.UpgradeWorkerControlPaused,
		},
		{
			name:      "cancelled",
			state:     opsv1alpha1.UpgradeManagerAdmissionGranted,
			cancel:    true,
			effective: opsv1alpha1.UpgradeWorkerControlCancelled,
		},
		{
			name:      "revoked",
			state:     opsv1alpha1.UpgradeManagerAdmissionRevoked,
			effective: opsv1alpha1.UpgradeWorkerControlDenied,
		},
		{
			name:      "revoked cancellation",
			state:     opsv1alpha1.UpgradeManagerAdmissionRevoked,
			cancel:    true,
			effective: opsv1alpha1.UpgradeWorkerControlCancelled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/before claim", func(t *testing.T) {
			slug := strings.ReplaceAll(tt.name, " ", "-")
			up := managedTestLeaf("control-" + slug + "-new")
			up.Status.ManagerAdmission.State = tt.state
			up.Status.ManagerControl.Pause = tt.pause
			up.Status.ManagerControl.Cancel = tt.cancel
			r := newManagedTestReconciler(t, up, nil)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if decision.allowProgress || decision.allowClaim || decision.effectiveState != tt.effective {
				t.Fatalf("new claim fence = %+v, want effective %q", decision, tt.effective)
			}
		})

		t.Run(tt.name+"/after claim", func(t *testing.T) {
			slug := strings.ReplaceAll(tt.name, " ", "-")
			up := managedTestLeaf("control-" + slug + "-claimed")
			up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
			up.Status.PrimarySupervisorInstallRequested = true
			up.Status.ManagerAdmission.State = tt.state
			up.Status.ManagerControl.Pause = tt.pause
			up.Status.ManagerControl.Cancel = tt.cancel
			up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
				Stage:           opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
				ReservationID:   managedTestReservationID,
				PolicyEpoch:     1,
				ControlRevision: up.Status.ManagerControl.Revision,
				ClaimedAt:       metav1.Time{Time: managedTestTime},
			}}
			r := newManagedTestReconciler(t, up, nil)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if !decision.allowProgress || decision.allowClaim ||
				decision.effectiveState != opsv1alpha1.UpgradeWorkerControlClaimed {
				t.Fatalf("existing claim observation fence = %+v", decision)
			}
		})
	}
}

func TestManagedTerminalClaimRequiresConclusiveSettlementAcrossRestart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		claimed   bool
		settled   bool
		wantState opsv1alpha1.UpgradeWorkerControlState
	}{
		{name: "no mutation claim", wantState: opsv1alpha1.UpgradeWorkerControlSettled},
		{name: "claimed ambiguous", claimed: true, wantState: opsv1alpha1.UpgradeWorkerControlClaimed},
		{name: "claimed explicit false", claimed: true, wantState: opsv1alpha1.UpgradeWorkerControlClaimed},
		{name: "claimed conclusively settled", claimed: true, settled: true, wantState: opsv1alpha1.UpgradeWorkerControlSettled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := managedTestLeaf("terminal-" + strings.ReplaceAll(tc.name, " ", "-"))
			up.Status.Phase = opsv1alpha1.UpgradePhaseFailed
			up.Status.CompletionTime = &metav1.Time{Time: managedTestTime}
			if tc.claimed {
				up.Status.PrimarySupervisorInstallRequested = true
				up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
					Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, ReservationID: managedTestReservationID, PolicyEpoch: 1,
					ControlRevision: 7, ClaimedAt: metav1.Time{Time: managedTestTime},
				}}
				status := metav1.ConditionFalse
				if tc.settled {
					status = metav1.ConditionTrue
				}
				if tc.settled || strings.Contains(tc.name, "explicit false") {
					up.Status.Conditions = append(up.Status.Conditions, metav1.Condition{
						Type: conditionTypeMutationSettled, Status: status, Reason: "Test", LastTransitionTime: metav1.Time{Time: managedTestTime},
					})
				}
			}
			r := newManagedTestReconciler(t, up, nil)
			decision, updated, err := r.syncManagedLeafGate(context.Background(), up, managedTestTime.Add(time.Minute))
			if err != nil || !updated || decision.effectiveState != tc.wantState || decision.allowClaim || !decision.allowProgress {
				t.Fatalf("terminal decision=%+v updated=%t error=%v, want state %s", decision, updated, err, tc.wantState)
			}

			// Reconstruct the reconciler with no process-local state and prove the
			// persisted claim/evidence yields the same decision after restart.
			var persisted opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &persisted); err != nil {
				t.Fatal(err)
			}
			restarted := *r
			decision, updated, err = restarted.syncManagedLeafGate(context.Background(), &persisted, managedTestTime.Add(2*time.Minute))
			if err != nil || updated || decision.effectiveState != tc.wantState {
				t.Fatalf("restart decision=%+v updated=%t error=%v, want state %s", decision, updated, err, tc.wantState)
			}
		})
	}
}

func TestManagedPendingAdmissionAcknowledgesIdentityWithoutAdvancing(t *testing.T) {
	up := managedTestLeaf("pending-admission")
	up.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionPending
	r := newManagedTestReconciler(t, up, nil)

	decision, updated, err := r.syncManagedLeafGate(context.Background(), up, managedTestTime)
	if err != nil {
		t.Fatalf("syncManagedLeafGate() error = %v", err)
	}
	if !updated || !decision.applies || decision.allowProgress || decision.allowClaim ||
		decision.effectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		t.Fatalf("pending admission handshake = %+v, updated=%t", decision, updated)
	}
	if up.Status.WorkerControl == nil ||
		up.Status.WorkerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		up.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		t.Fatalf("pending admission acknowledgement = %#v", up.Status.WorkerControl)
	}
}

func TestManagedAndStandaloneLeafModesFailClosed(t *testing.T) {
	t.Run("managed worker ignores ordinary leaf without claiming status", func(t *testing.T) {
		up := newUpgrade("ordinary-on-managed", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
			up.UID = "ordinary-leaf-uid"
			up.Status.Phase = opsv1alpha1.UpgradePhasePending
		})
		r := newManagedTestReconciler(t, up, nil)
		decision, updated, err := r.syncManagedLeafGate(context.Background(), up, managedTestTime)
		if err != nil {
			t.Fatalf("syncManagedLeafGate() error = %v", err)
		}
		if updated || !decision.applies || decision.allowProgress ||
			up.Status.WorkerControl != nil {
			t.Fatalf("ordinary leaf on managed worker decision=%+v status=%#v updated=%t",
				decision, up.Status.WorkerControl, updated)
		}
		result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)})
		if err != nil || result != (reconcile.Result{}) {
			t.Fatalf("Reconcile() result=%+v error=%v, want an ignored leaf", result, err)
		}
		var persisted opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &persisted); err != nil {
			t.Fatalf("read ignored leaf: %v", err)
		}
		if persisted.Status.WorkerControl != nil || len(persisted.Finalizers) != 0 {
			t.Fatalf("managed worker mutated ordinary leaf: status=%#v finalizers=%v",
				persisted.Status.WorkerControl, persisted.Finalizers)
		}
	})

	t.Run("standalone worker rejects explicitly managed leaf", func(t *testing.T) {
		up := managedTestLeaf("managed-on-standalone")
		r := newManagedTestReconciler(t, up, nil)
		r.ManagedTopology = false
		decision := r.evaluateManagedLeaf(context.Background(), up)
		if !decision.applies || decision.allowProgress ||
			decision.effectiveState != opsv1alpha1.UpgradeWorkerControlDenied {
			t.Fatalf("managed leaf on standalone worker decision=%+v", decision)
		}
	})

	t.Run("standalone ordinary leaf is unchanged", func(t *testing.T) {
		up := newUpgrade("ordinary-on-standalone", nil)
		r := newManagedTestReconciler(t, up, nil)
		r.ManagedTopology = false
		decision, updated, err := r.syncManagedLeafGate(context.Background(), up, managedTestTime)
		if err != nil {
			t.Fatalf("syncManagedLeafGate() error = %v", err)
		}
		if updated || decision.applies || !decision.allowProgress || !decision.allowClaim ||
			up.Status.WorkerControl != nil {
			t.Fatalf("standalone compatibility changed: decision=%+v status=%#v updated=%t",
				decision, up.Status.WorkerControl, updated)
		}
	})
}

func TestManagedReconcilePersistsAcknowledgementBeforePhaseAdvance(t *testing.T) {
	up := managedTestLeaf("ack-before-phase")
	up.Finalizers = []string{upgradeFinalizer}
	up.Status.Phase = opsv1alpha1.UpgradePhasePending
	r := newManagedTestReconciler(t, up, nil)

	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(up),
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter != time.Second {
		t.Fatalf("RequeueAfter=%s, want acknowledgement barrier of 1s", result.RequeueAfter)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
		t.Fatalf("get leaf: %v", err)
	}
	if got.Status.Phase != opsv1alpha1.UpgradePhasePending ||
		got.Status.WorkerControl == nil ||
		got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		t.Fatalf("phase advanced before acknowledgement barrier: %+v", got.Status)
	}
}

func TestManagedDurableClaimCASRecordsEveryMutationStage(t *testing.T) {
	tests := []struct {
		stage  opsv1alpha1.UpgradeManagedMutationStage
		phase  opsv1alpha1.UpgradePhase
		invoke func(context.Context, *Reconciler, *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error)
	}{
		{
			stage: opsv1alpha1.UpgradeManagedMutationStaging,
			phase: opsv1alpha1.UpgradePhaseResolving,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimStaging(ctx, up, "stage image", managedTestTime)
				return claimed, err
			},
		},
		{
			stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
			phase: opsv1alpha1.UpgradePhaseTransferring,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimInstallAttempt(ctx, up, false, managedTestTime)
				return claimed, err
			},
		},
		{
			stage: opsv1alpha1.UpgradeManagedMutationStandbyInstall,
			phase: opsv1alpha1.UpgradePhaseTransferring,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimInstallAttempt(ctx, up, true, managedTestTime)
				return claimed, err
			},
		},
		{
			stage: opsv1alpha1.UpgradeManagedMutationStandbyActivation,
			phase: opsv1alpha1.UpgradePhaseActivating,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimActivation(ctx, up, true, "StandbyActivationRequested", "activate standby", managedTestTime)
				return claimed, err
			},
		},
		{
			stage: opsv1alpha1.UpgradeManagedMutationPrimaryActivation,
			phase: opsv1alpha1.UpgradePhaseActivating,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimActivation(ctx, up, false, "ActivationRequested", "activate primary", managedTestTime)
				return claimed, err
			},
		},
		{
			stage: opsv1alpha1.UpgradeManagedMutationRollbackActivation,
			phase: opsv1alpha1.UpgradePhaseRollingBack,
			invoke: func(ctx context.Context, r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) (bool, error) {
				claimed, _, err := r.claimRollbackActivation(ctx, up, "17.12.04", "rollback", managedTestTime)
				return claimed, err
			},
		},
	}

	for i, tt := range tests {
		t.Run(string(tt.stage), func(t *testing.T) {
			up := managedTestLeaf("claim-" + string(rune('a'+i)))
			up.Status.Phase = tt.phase
			r := newManagedTestReconciler(t, up, nil)
			var before opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &before); err != nil {
				t.Fatalf("get leaf before claim: %v", err)
			}
			*up = *before.DeepCopy()
			if !upgradeStatusCASMatches(up, &before) {
				t.Fatalf("initial CAS snapshot mismatch: uid=%q/%q generation=%d/%d managedMatch=%t\nexpected admission=%#v control=%#v annotations=%v\ncurrent admission=%#v control=%#v annotations=%v",
					up.UID, before.UID, up.Generation, before.Generation, managedStatusCASMatches(up, &before),
					up.Status.ManagerAdmission, up.Status.ManagerControl, up.Annotations,
					before.Status.ManagerAdmission, before.Status.ManagerControl, before.Annotations)
			}
			claimed, err := tt.invoke(context.Background(), r, up)
			if err != nil {
				t.Fatalf("claim %s: %v", tt.stage, err)
			}
			if !claimed {
				t.Fatalf("%s was not claimed", tt.stage)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatalf("get claimed leaf: %v", err)
			}
			if !managedMutationMarkers(&got)[tt.stage] {
				t.Fatalf("%s durable marker not persisted: %+v", tt.stage, got.Status)
			}
			if len(got.Status.ManagedMutationClaims) != 1 {
				t.Fatalf("managed claims = %#v, want exactly one", got.Status.ManagedMutationClaims)
			}
			claim := got.Status.ManagedMutationClaims[0]
			if claim.Stage != tt.stage || claim.ReservationID != managedTestReservationID ||
				claim.ControlRevision != 7 || !claim.ClaimedAt.Time.Equal(managedTestTime) ||
				claim.ClaimedAt.IsZero() {
				t.Fatalf("managed claim = %#v", claim)
			}
			if got.Status.WorkerControl == nil ||
				got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlClaimed ||
				got.Status.WorkerControl.ObservedControlRevision != 7 {
				t.Fatalf("claim acknowledgement = %#v", got.Status.WorkerControl)
			}
		})
	}
}

func TestManagedClaimFailsClosedOnCampaignPeerFailure(t *testing.T) {
	tests := []struct {
		name            string
		peerCampaign    string
		peerPhase       opsv1alpha1.UpgradePhase
		listFailure     bool
		wantClaim       bool
		wantReadyReason string
	}{
		{name: "same campaign failed", peerCampaign: managedTestCampaignUID, peerPhase: opsv1alpha1.UpgradePhaseFailed, wantReadyReason: "CampaignTargetFailed"},
		{name: "same campaign cancelled", peerCampaign: managedTestCampaignUID, peerPhase: opsv1alpha1.UpgradePhaseCancelled, wantReadyReason: "CampaignTargetFailed"},
		{name: "same campaign succeeded", peerCampaign: managedTestCampaignUID, peerPhase: opsv1alpha1.UpgradePhaseSucceeded, wantClaim: true},
		{name: "different campaign failed", peerCampaign: "other-campaign", peerPhase: opsv1alpha1.UpgradePhaseFailed, wantClaim: true},
		{name: "peer list unavailable", peerCampaign: managedTestCampaignUID, peerPhase: opsv1alpha1.UpgradePhasePending, listFailure: true, wantReadyReason: "CampaignTargetFailed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := managedTestLeaf("campaign-fence-target-" + strings.ReplaceAll(tt.name, " ", "-"))
			up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			peer := managedTestLeaf("campaign-fence-peer-" + strings.ReplaceAll(tt.name, " ", "-"))
			peer.Annotations[managedprotocol.AnnotationCampaignUID] = tt.peerCampaign
			peer.Status.ManagerAdmission.CampaignUID = tt.peerCampaign
			peer.Status.Phase = tt.peerPhase
			r := newManagedTestReconciler(t, up, nil, peer)
			if tt.listFailure {
				r.Reader = upgradeListErrorReader{Reader: r.Client}
			}
			var snapshot opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &snapshot); err != nil {
				t.Fatal(err)
			}
			*up = *snapshot.DeepCopy()
			claimed, _, err := r.claimActivation(
				context.Background(), up, false, "ActivationRequested", "activate", managedTestTime,
			)
			if err != nil {
				t.Fatalf("claimActivation() error = %v", err)
			}
			if claimed != tt.wantClaim {
				t.Fatalf("claimed=%t, want %t", claimed, tt.wantClaim)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatal(err)
			}
			if tt.wantClaim {
				if !got.Status.PrimarySupervisorActivationRequested || len(got.Status.ManagedMutationClaims) != 1 {
					t.Fatalf("permitted claim was not recorded: %+v", got.Status)
				}
				return
			}
			if got.Status.PrimarySupervisorActivationRequested || len(got.Status.ManagedMutationClaims) != 0 ||
				got.Status.WorkerControl == nil || got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlDenied ||
				readyReason(got.Status.Conditions) != tt.wantReadyReason {
				t.Fatalf("campaign failure fence status = %+v", got.Status)
			}
		})
	}
}

func TestManagedClaimBlockIfRunning(t *testing.T) {
	tests := []struct {
		name     string
		phase    corev1.PodPhase
		deleting bool
		blocked  bool
	}{
		{name: "pending", phase: corev1.PodPending, blocked: true},
		{name: "running", phase: corev1.PodRunning, blocked: true},
		{name: "unknown", phase: corev1.PodUnknown, blocked: true},
		{name: "succeeded", phase: corev1.PodSucceeded},
		{name: "failed", phase: corev1.PodFailed},
		{name: "deleting", phase: corev1.PodRunning, deleting: true, blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := managedTestLeaf("workload-" + tt.name)
			up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "workloads", Name: "app-" + tt.name},
				Spec:       corev1.PodSpec{NodeName: managedTestNodeName},
				Status:     corev1.PodStatus{Phase: tt.phase},
			}
			if tt.deleting {
				pod.Finalizers = []string{"test.example/retain"}
				pod.DeletionTimestamp = &metav1.Time{Time: managedTestTime}
			}
			r := newManagedTestReconciler(t, up, nil, pod)
			var snapshot opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &snapshot); err != nil {
				t.Fatalf("get leaf before claim: %v", err)
			}
			*up = *snapshot.DeepCopy()
			claimed, _, err := r.claimActivation(
				context.Background(), up, false, "ActivationRequested", "activate", managedTestTime,
			)
			if err != nil {
				t.Fatalf("claimActivation() error = %v", err)
			}
			if claimed == tt.blocked {
				t.Fatalf("claimed=%t, blocked=%t", claimed, tt.blocked)
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatalf("get leaf: %v", err)
			}
			if tt.blocked {
				if got.Status.PrimarySupervisorActivationRequested ||
					len(got.Status.ManagedMutationClaims) != 0 ||
					got.Status.WorkerControl == nil ||
					got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlDenied ||
					readyReason(got.Status.Conditions) != "WorkloadsRunning" {
					t.Fatalf("blocked claim status = %+v", got.Status)
				}
			} else if !got.Status.PrimarySupervisorActivationRequested ||
				len(got.Status.ManagedMutationClaims) != 1 {
				t.Fatalf("terminal/deleting workload blocked claim: %+v", got.Status)
			}
		})
	}
}

func TestManagedClaimRequiresEmptyDeviceWorkloadInventory(t *testing.T) {
	tests := []struct {
		name   string
		list   func(context.Context) ([]*corev1.Pod, error)
		reason string
	}{
		{name: "missing lister", reason: "DeviceWorkloadCheckFailed"},
		{name: "list failure", list: func(context.Context) ([]*corev1.Pod, error) {
			return nil, errors.New("device inventory unavailable")
		}, reason: "DeviceWorkloadCheckFailed"},
		{name: "device app remains", list: func(context.Context) ([]*corev1.Pod, error) {
			return []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "orphaned-app"}}}, nil
		}, reason: "DeviceWorkloadsRunning"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			up := managedTestLeaf("device-workload-" + strings.ReplaceAll(test.name, " ", "-"))
			up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			r := newManagedTestReconciler(t, up, nil)
			r.DevicePodLister = test.list
			var snapshot opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &snapshot); err != nil {
				t.Fatal(err)
			}
			*up = *snapshot.DeepCopy()
			claimed, _, err := r.claimActivation(
				context.Background(), up, false, "ActivationRequested", "activate", managedTestTime,
			)
			if err != nil {
				t.Fatalf("claimActivation() error = %v", err)
			}
			if claimed {
				t.Fatal("mutation was claimed without an empty device workload inventory")
			}
			var got opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.PrimarySupervisorActivationRequested || len(got.Status.ManagedMutationClaims) != 0 ||
				got.Status.WorkerControl == nil || got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlDenied ||
				readyReason(got.Status.Conditions) != test.reason {
				t.Fatalf("device workload gate status = %+v", got.Status)
			}
		})
	}
}

func TestManagedClaimFailsClosedWhenWorkloadsCannotBeListed(t *testing.T) {
	up := managedTestLeaf("workload-list-error")
	up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
	r := newManagedTestReconciler(t, up, nil)
	r.Reader = podListErrorReader{Reader: r.Client}
	var snapshot opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &snapshot); err != nil {
		t.Fatalf("get leaf before claim: %v", err)
	}
	*up = *snapshot.DeepCopy()

	claimed, _, err := r.claimActivation(
		context.Background(), up, false, "ActivationRequested", "activate", managedTestTime,
	)
	if err != nil {
		t.Fatalf("claimActivation() error = %v", err)
	}
	if claimed {
		t.Fatal("mutation was claimed without an authoritative workload list")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &got); err != nil {
		t.Fatalf("get leaf: %v", err)
	}
	if got.Status.PrimarySupervisorActivationRequested ||
		len(got.Status.ManagedMutationClaims) != 0 ||
		got.Status.WorkerControl == nil ||
		got.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlDenied ||
		readyReason(got.Status.Conditions) != "WorkloadCheckFailed" {
		t.Fatalf("workload list failure did not fail closed: %+v", got.Status)
	}
}

func TestManagedClaimCASRejectsStaleManagerControl(t *testing.T) {
	up := managedTestLeaf("stale-control")
	up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
	r := newManagedTestReconciler(t, up, nil)

	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &current); err != nil {
		t.Fatalf("get leaf: %v", err)
	}
	stale := current.DeepCopy()
	current.Status.ManagerControl.Revision++
	current.Status.ManagerControl.Pause = true
	current.Status.ManagerControl.UpdatedAt = metav1.Time{Time: managedTestTime.Add(time.Second)}
	if err := r.Client.Status().Update(context.Background(), &current); err != nil {
		t.Fatalf("pause leaf: %v", err)
	}

	claimed, _, err := r.claimActivation(
		context.Background(), stale, false, "ActivationRequested", "activate", managedTestTime,
	)
	if err != nil {
		t.Fatalf("claimActivation() error = %v", err)
	}
	if claimed {
		t.Fatal("stale grant/control snapshot won a mutation claim")
	}
	if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), &current); err != nil {
		t.Fatalf("get leaf after claim: %v", err)
	}
	if current.Status.PrimarySupervisorActivationRequested ||
		len(current.Status.ManagedMutationClaims) != 0 {
		t.Fatalf("stale claim mutated status: %+v", current.Status)
	}
}

func TestManagedClaimCoverageRejectsOldOrCorruptWorkerStatus(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*opsv1alpha1.IOSXESoftwareUpgrade)
	}{
		{
			name: "marker without managed claim",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.PrimarySupervisorInstallRequested = true
			},
		},
		{
			name: "claim without marker",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
					Stage:           opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
					ReservationID:   managedTestReservationID,
					PolicyEpoch:     1,
					ControlRevision: 7,
					ClaimedAt:       metav1.Time{Time: managedTestTime},
				}}
			},
		},
		{
			name: "wrong reservation",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.PrimarySupervisorInstallRequested = true
				up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
					Stage:           opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
					ReservationID:   "other-reservation",
					PolicyEpoch:     1,
					ControlRevision: 7,
					ClaimedAt:       metav1.Time{Time: managedTestTime},
				}}
			},
		},
		{
			name: "future revision",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.PrimarySupervisorInstallRequested = true
				up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
					Stage:           opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
					ReservationID:   managedTestReservationID,
					PolicyEpoch:     1,
					ControlRevision: 8,
					ClaimedAt:       metav1.Time{Time: managedTestTime},
				}}
			},
		},
		{
			name: "stale policy epoch",
			mutate: func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Status.PrimarySupervisorInstallRequested = true
				up.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
					Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, ReservationID: managedTestReservationID,
					PolicyEpoch: 2, ControlRevision: 7, ClaimedAt: metav1.Time{Time: managedTestTime},
				}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up := managedTestLeaf("claim-coverage-" + strings.ReplaceAll(tt.name, " ", "-"))
			tt.mutate(up)
			r := newManagedTestReconciler(t, up, nil)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if decision.allowProgress || decision.allowClaim {
				t.Fatalf("invalid claim coverage accepted: %+v", decision)
			}
		})
	}
}
