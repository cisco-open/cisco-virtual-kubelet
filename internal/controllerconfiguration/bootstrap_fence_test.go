// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func bootstrapFenceFixture(t *testing.T) (client.Client, *cvk.NetworkController, *config.NetworkControllerConfig) {
	t.Helper()
	c, nc, intent := bootstrapIdentityFixture(t)
	_ = apps.AddToScheme(c.Scheme())
	_ = coordination.AddToScheme(c.Scheme())
	_ = admission.AddToScheme(c.Scheme())
	policy, binding, namespace := bootstrapProtectionFixture(t)
	policy.Spec.Validations[0].Expression = strings.ReplaceAll(policy.Spec.Validations[0].Expression, "bootstrap-leases", "leases")
	for i := range policy.Spec.Variables {
		if policy.Spec.Variables[i].Name == "sharedNamespace" {
			policy.Spec.Variables[i].Expression = strings.ReplaceAll(policy.Spec.Variables[i].Expression, "bootstrap-leases", "leases")
		}
	}
	namespace.Name = "leases"
	for _, obj := range []client.Object{policy, binding, namespace} {
		obj.SetResourceVersion("")
		if err := c.Create(context.Background(), obj); err != nil {
			t.Fatal(err)
		}
	}

	_, _, deployment, rs, pod, _ := controllerWorkerFixture(t)
	deployment.Annotations["cisco.vk/network-controller-name"] = nc.Name
	deployment.Annotations["cisco.vk/network-controller-uid"] = string(nc.UID)
	args := []string{"controller-worker", "--controller-namespace=" + nc.Namespace, "--controller-name=" + nc.Name, "--controller-uid=" + string(nc.UID), "--controller-generation=1", "--controller-type=catalyst-center"}
	deployment.Spec.Template.Spec.Containers[0].Args = args
	rs.Spec.Template.Spec.Containers[0].Args = args
	pod.Spec.Containers[0].Args = args
	for _, obj := range []client.Object{deployment, rs, pod} {
		if err := c.Create(context.Background(), obj); err != nil {
			t.Fatal(err)
		}
	}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{DeploymentName: deployment.Name}
	if err := c.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	return c, nc, intent
}

func bootstrapFenceFixtureWorker(t *testing.T, c client.Client, nc *cvk.NetworkController) *bootstrapControllerWorker {
	t.Helper()
	worker, err := readBootstrapControllerWorker(context.Background(), c, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func bootstrapFenceTestInstallation() BootstrapFenceInstallation {
	return BootstrapFenceInstallation{PolicyName: "cvk-cisco-virtual-kubelet-bootstrap-fence", ManagerUsername: bootstrapTestManager, LeaseNamespace: "leases"}
}
func bootstrapFenceFixtureProtection(t *testing.T, c client.Client) *BootstrapFenceProtection {
	t.Helper()
	installation := bootstrapFenceTestInstallation()
	proof, err := ReadBootstrapFenceProtection(context.Background(), c, installation.PolicyName, installation.ManagerUsername, installation.LeaseNamespace)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

type bootstrapFenceStore struct {
	client.Client
	mode      string
	creates   atomic.Int32
	leaseGets atomic.Int32
}

func (s *bootstrapFenceStore) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	err := s.Client.Get(ctx, key, obj, opts...)
	if l, ok := obj.(*coordination.Lease); ok {
		count := s.leaseGets.Add(1)
		if err == nil && s.mode == "replaced-uid" && count >= 2 {
			l.UID = "replacement"
		}
	}
	return err
}
func (s *bootstrapFenceStore) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	l, ok := obj.(*coordination.Lease)
	if !ok {
		return errors.New("bootstrap fence wrote an unexpected kind")
	}
	count := s.creates.Add(1)
	l.UID = types.UID(fmt.Sprintf("fence-%d", count))
	if s.mode == "own-race" || s.mode == "foreign-race" {
		copy := l.DeepCopy()
		copy.UID = "raced-fence"
		if s.mode == "foreign-race" {
			copy.Annotations[bootstrapFenceBindingAnnotation] = "foreign"
		}
		if err := s.Client.Create(ctx, copy, opts...); err != nil {
			return err
		}
		return apierrors.NewAlreadyExists(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, l.Name)
	}
	if s.mode == "pruned-binding" {
		delete(l.Annotations, bootstrapFenceBindingAnnotation)
	}
	if s.mode == "missing-uid" {
		l.UID = ""
	}
	if err := s.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if s.mode == "create-timeout" {
		return context.DeadlineExceeded
	}
	if s.mode == "late-device" {
		d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "late", Namespace: "another-tenant", UID: "late"}, Spec: cvk.DeviceSpec{PhysicalIdentity: "BOOTSTRAP123"}}
		return s.Client.Create(ctx, d)
	}
	return nil
}

func TestBootstrapFenceReservation(t *testing.T) {
	for _, mode := range []string{"valid", "mutate-arguments", "wrong-namespace", "denied", "missing-runtime", "source-changed", "post-denied", "late-device", "late-worker", "late-protection", "foreign-fence", "expired-fence", "own-race", "foreign-race", "create-timeout", "pruned-binding", "missing-uid", "replaced-uid"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, intent := bootstrapFenceFixture(t)
			_ = coordination.AddToScheme(c.Scheme())
			store := &bootstrapFenceStore{Client: c, mode: mode}
			observation, err := ReadBootstrapIdentity(ctx, c, nc)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := bootstrapFenceCandidate("leases", nc.Namespace, observation, bootstrapFenceFixtureWorker(t, c, nc), bootstrapFenceTestInstallation(), bootstrapFenceFixtureProtection(t, c))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "foreign-fence" || mode == "expired-fence" {
				existing := expected.DeepCopy()
				existing.UID = "old-fence"
				if mode == "foreign-fence" {
					existing.Annotations[bootstrapFenceBindingAnnotation] = "other operation"
				}
				if mode == "expired-fence" {
					seconds := int32(1)
					at := metav1.NewMicroTime(time.Now().Add(-time.Hour))
					existing.Spec.LeaseDurationSeconds = &seconds
					existing.Spec.RenewTime = &at
				}
				if err := c.Create(ctx, existing); err != nil {
					t.Fatal(err)
				}
			}
			originalExisting := &coordination.Lease{}
			if mode == "foreign-fence" || mode == "expired-fence" {
				if err := c.Get(ctx, client.ObjectKeyFromObject(expected), originalExisting); err != nil {
					t.Fatal(err)
				}
			}
			checks := 0
			authorize := BootstrapFenceRuntimeCheck(func(ctx context.Context, namespace string, controller *cvk.NetworkController, identity BootstrapIdentityObservation) error {
				checks++
				if mode == "denied" || namespace != "leases" || mode == "post-denied" && checks == 2 {
					return errors.New("runtime unavailable")
				}
				if mode == "source-changed" {
					copy := intent.DeepCopy()
					copy.Generation++
					if err := c.Update(ctx, copy); err != nil {
						return err
					}
				}
				if mode == "late-protection" && checks == 2 {
					var policy admission.ValidatingAdmissionPolicy
					if err := c.Get(ctx, client.ObjectKey{Name: bootstrapFenceTestInstallation().PolicyName}, &policy); err != nil {
						return err
					}
					if err := c.Delete(ctx, &policy); err != nil {
						return err
					}
					policy.UID = "replacement"
					policy.ResourceVersion = ""
					if err := c.Create(ctx, &policy); err != nil {
						return err
					}
				}
				if mode == "late-worker" && checks == 2 {
					var pod core.Pod
					if err := c.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: "worker"}, &pod); err != nil {
						return err
					}
					pod.Spec.Containers[0].Args = append(pod.Spec.Containers[0].Args, "--controller-uid=other")
					if err := c.Update(ctx, &pod); err != nil {
						return err
					}
				}
				if mode == "mutate-arguments" {
					controller.Status.Configuration.Operation.TargetSerial = "foreign"
					identity.PhysicalIdentity = "foreign"
				}
				return nil
			})
			if mode == "missing-runtime" {
				authorize = nil
			}
			namespace := "leases"
			if mode == "wrong-namespace" {
				namespace = "tenant-selected"
			}
			result, err := ReserveBootstrapFence(ctx, store, store, nc, BootstrapFenceInstallation{PolicyName: bootstrapFenceTestInstallation().PolicyName, ManagerUsername: bootstrapTestManager, LeaseNamespace: namespace}, authorize)
			valid := mode == "valid" || mode == "mutate-arguments" || mode == "own-race"
			if (err == nil) != valid {
				t.Fatal("reservation outcome", err)
			}
			var leases coordination.LeaseList
			if err := c.List(ctx, &leases); err != nil {
				t.Fatal(err)
			}
			preRefused := mode == "denied" || mode == "missing-runtime" || mode == "wrong-namespace" || mode == "source-changed"
			if preRefused {
				if store.creates.Load() != 0 || len(leases.Items) != 0 {
					t.Fatal("unqualified source/runtime wrote a fence")
				}
				return
			}
			if len(leases.Items) != 1 {
				t.Fatal("reservation evidence was removed")
			}
			if !valid {
				if result != nil {
					t.Fatal("invalid reservation returned")
				}
				if (mode == "foreign-fence" || mode == "expired-fence") && store.creates.Load() != 0 {
					t.Fatal("existing foreign/expiring evidence was overwritten")
				}
				if (mode == "foreign-fence" || mode == "expired-fence") && !reflect.DeepEqual(originalExisting, &leases.Items[0]) {
					t.Fatal("existing evidence changed")
				}

				if mode == "create-timeout" {
					store.mode = ""
					result, err = ReserveBootstrapFence(ctx, store, store, nc, bootstrapFenceTestInstallation(), authorize)
					if err != nil || result.UID != leases.Items[0].UID || store.creates.Load() != 1 {
						t.Fatal("uncertain metadata creation could not be corroborated without recreating", err)
					}
				}
				return
			}
			if checks != 2 || !sameBootstrapFence(result, expected) || result.Spec.LeaseDurationSeconds != nil || result.Spec.RenewTime != nil {
				t.Fatal("invalid retained serial fence")
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "DO-NOT-PRINT") {
				t.Fatal("source values entered fence metadata")
			}
			store.mode = ""
			again, err := ReserveBootstrapFence(ctx, store, store, nc, bootstrapFenceTestInstallation(), authorize)
			if err != nil || again.UID != result.UID || store.creates.Load() != 1 {
				t.Fatal("owned idempotence rewrote or recreated evidence", err)
			}
		})
	}
}

func TestBootstrapFenceSerialScope(t *testing.T) {
	c, nc, _ := bootstrapFenceFixture(t)
	identity, err := ReadBootstrapIdentity(context.Background(), c, nc)
	if err != nil {
		t.Fatal(err)
	}
	worker := bootstrapFenceFixtureWorker(t, c, nc)
	worker.Username = "system:serviceaccount:tenant-one:connector"
	one, err := bootstrapFenceCandidate("leases", "tenant-one", identity, worker, bootstrapFenceTestInstallation(), bootstrapFenceFixtureProtection(t, c))
	if err != nil {
		t.Fatal(err)
	}
	other := *identity
	other.Controller.UID = "different-controller"
	other.Config.UID = "different-intent"
	other.NativeTargetID = "different-native-record"
	worker.Username = "system:serviceaccount:tenant-two:connector"
	two, err := bootstrapFenceCandidate("leases", "tenant-two", &other, worker, bootstrapFenceTestInstallation(), bootstrapFenceFixtureProtection(t, c))
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := BootstrapFenceName("BOOTSTRAP123")
	if err != nil {
		t.Fatal(err)
	}
	if one.Name != two.Name || one.Name != mixed || *one.Spec.HolderIdentity == *two.Spec.HolderIdentity {
		t.Fatal("physical scope split by controller/namespace or ownership collapsed")
	}
}

func TestBootstrapFenceConcurrentIdempotence(t *testing.T) {
	ctx := context.Background()
	c, nc, _ := bootstrapFenceFixture(t)
	_ = coordination.AddToScheme(c.Scheme())
	store := &bootstrapFenceStore{Client: c}
	authorize := BootstrapFenceRuntimeCheck(func(context.Context, string, *cvk.NetworkController, BootstrapIdentityObservation) error { return nil })
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	uids := make(chan types.UID, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := ReserveBootstrapFence(ctx, store, store, nc, bootstrapFenceTestInstallation(), authorize)
			if err != nil {
				failures <- err
				return
			}
			uids <- lease.UID
		}()
	}
	wg.Wait()
	close(failures)
	close(uids)
	for err := range failures {
		t.Error(err)
	}
	uid := types.UID("")
	for current := range uids {
		if uid == "" {
			uid = current
		}
		if uid != current {
			t.Error("concurrent callers observed different fence incarnations")
		}
	}
	var leases coordination.LeaseList
	if err := c.List(ctx, &leases); err != nil || len(leases.Items) != 1 {
		t.Fatal("physical scope was not serialized", err)
	}
}

func TestBootstrapFenceReadback(t *testing.T) {
	for _, mode := range []string{"held", "missing", "deleted-source", "changed-source", "malformed", "future-protocol", "wrong-serial", "duplicate-json", "expired", "deleting", "empty-holder"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, intent := bootstrapFenceFixture(t)
			_ = coordination.AddToScheme(c.Scheme())
			observation, err := ReadBootstrapIdentity(ctx, c, nc)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := bootstrapFenceCandidate("leases", nc.Namespace, observation, bootstrapFenceFixtureWorker(t, c, nc), bootstrapFenceTestInstallation(), bootstrapFenceFixtureProtection(t, c))
			if err != nil {
				t.Fatal(err)
			}
			lease.UID = "retained"
			switch mode {
			case "deleted-source":
				if err := c.Delete(ctx, intent); err != nil {
					t.Fatal(err)
				}
			case "changed-source":
				intent.Generation++
				if err := c.Update(ctx, intent); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				lease.Annotations[bootstrapFenceBindingAnnotation] = "{}"
			case "future-protocol":
				lease.Annotations[bootstrapFenceBindingAnnotation] = strings.Replace(lease.Annotations[bootstrapFenceBindingAnnotation], BootstrapFenceProtocol, "cvk-bootstrap-fence.v9", 1)
			case "wrong-serial":
				lease.Annotations[bootstrapFenceBindingAnnotation] = strings.ReplaceAll(lease.Annotations[bootstrapFenceBindingAnnotation], "bootstrap123", "another-serial")
			case "duplicate-json":
				lease.Annotations[bootstrapFenceBindingAnnotation] = strings.Replace(lease.Annotations[bootstrapFenceBindingAnnotation], `{"protocol":`, `{"protocol":"unknown","protocol":`, 1)
			case "expired":
				duration := int32(1)
				lease.Spec.LeaseDurationSeconds = &duration
			case "empty-holder":
				lease.Spec.HolderIdentity = nil
			}
			if mode != "missing" {
				if err := c.Create(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "deleting" {
				if err := c.Delete(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			result, err := ReadBootstrapFence(ctx, c, "leases", "BOOTSTRAP123")
			valid := mode == "held" || mode == "deleted-source" || mode == "changed-source" || mode == "missing"
			if (err == nil) != valid {
				t.Fatal("retained readback result", err)
			}
			if mode == "missing" {
				if result != nil {
					t.Fatal("invented missing fence")
				}
				return
			}
			if valid && (result == nil || result.UID != lease.UID) {
				t.Fatal("retained fence lost when source changed")
			}
			if !valid && result != nil {
				t.Fatal("malformed evidence returned as valid")
			}
		})
	}
}
