// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const projectedCredential = "test-credential\nwith-whitespace "
const projectedSecretPointer = "/authentication_and_policy_servers/aaa/0/shared_secret"
const projectedSecretModel = `{"catalyst_center":{"system_settings":{"authentication_and_policy_servers":{"aaa":[{"ip_address":"192.0.2.43","protocols":{"radius":{}}}]}}}}`

func configurationSecretFixture(t *testing.T) (*ciskov1.NetworkController, *configapi.NetworkControllerConfig, string, string, []configurationSecretDestination) {
	t.Helper()
	nc := &ciskov1.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: "lab", UID: "controller-uid", Generation: 1}}
	nc.Spec.IntentSecretSources = []ciskov1.NetworkControllerIntentSecretSource{{Alias: "aaa-secret", Name: "aaa-credentials", Key: "radius"}}
	config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "aaa", Namespace: "lab", UID: "config-uid", Generation: 1}}
	config.Spec.ControllerRef = configapi.NetworkControllerRef{Name: "catc"}
	config.Spec.ManagedSections = []string{"system_settings"}
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "system_settings", Path: projectedSecretPointer, Source: "aaa-secret"}}
	root := t.TempDir()
	relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{
		ConfigName: config.Name, Section: "system_settings", JSONPointer: projectedSecretPointer,
		SourceAlias: "aaa-secret", SecretName: "aaa-credentials", SecretKey: "radius",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(projectedCredential), 0600); err != nil {
		t.Fatal(err)
	}
	return nc, config, root, path, []configurationSecretDestination{{"system_settings", projectedSecretPointer}}
}

func TestConfigurationSecretProjectionAndInjection(t *testing.T) {
	nc, config, root, _, destinations := configurationSecretFixture(t)
	snapshot, err := resolveConfigurationSecrets(nc, config, root, destinations)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.unchanged(nc, config); err != nil {
		t.Fatal(err)
	}
	raw := []byte(projectedSecretModel)
	out, err := snapshot.inject(raw)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Root struct {
			Settings struct {
				Servers struct {
					AAA []struct {
						Secret string `json:"shared_secret"`
					} `json:"aaa"`
				} `json:"authentication_and_policy_servers"`
			} `json:"system_settings"`
		} `json:"catalyst_center"`
	}
	if err := json.Unmarshal(out, &result); err != nil || len(result.Root.Settings.Servers.AAA) != 1 || result.Root.Settings.Servers.AAA[0].Secret != projectedCredential {
		t.Fatal("credential injection did not preserve bytes")
	}
	if string(raw) != projectedSecretModel || strings.Contains(fmt.Sprintf("%v %+v %#v", snapshot, snapshot, snapshot), "test-credential") {
		t.Fatal("input changed or formatting exposed credentials")
	}
}

func TestConfigurationSecretAuthorizationAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ciskov1.NetworkController, *configapi.NetworkControllerConfig)
	}{
		{"revoked", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) {
			n.Spec.IntentSecretSources = nil
		}},
		{"remapped key", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) {
			n.Spec.IntentSecretSources[0].Key = "other"
		}},
		{"remapped name", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) {
			n.Spec.IntentSecretSources[0].Name = "other"
		}},
		{"duplicate alias", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) {
			n.Spec.IntentSecretSources = append(n.Spec.IntentSecretSources, n.Spec.IntentSecretSources[0])
		}},
		{"paused", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) { n.Spec.Paused = true }},
		{"controller replaced", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) { n.UID = "replacement" }},
		{"controller generation", func(n *ciskov1.NetworkController, _ *configapi.NetworkControllerConfig) { n.Generation++ }},
		{"config replaced", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) { c.UID = "replacement" }},
		{"config generation", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) { c.Generation++ }},
		{"namespace", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) { c.Namespace = "other" }},
		{"config name", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) { c.Name = "other" }},
		{"controller ref", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.ControllerRef.Name = "other"
		}},
		{"changed destination", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs[0].Path = "/external_authentication/enabled"
		}},
		{"unmanaged section", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.ManagedSections = []string{"sites"}
		}},
		{"duplicate ref", func(_ *ciskov1.NetworkController, c *configapi.NetworkControllerConfig) {
			c.Spec.SecretRefs = append(c.Spec.SecretRefs, c.Spec.SecretRefs[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			nc, config, root, _, destinations := configurationSecretFixture(t)
			snapshot, err := resolveConfigurationSecrets(nc, config, root, destinations)
			if err != nil {
				t.Fatal(err)
			}
			test.change(nc, config)
			if snapshot.unchanged(nc, config) == nil {
				t.Fatal("changed credential authority/identity was accepted")
			}
		})
	}
	nc, config, root, _, _ := configurationSecretFixture(t)
	if _, err := resolveConfigurationSecrets(nc, config, root, nil); err == nil {
		t.Fatal("source authorization bypassed product destination allowlist")
	}
}

func TestConfigurationSecretAtomicProjectionRotation(t *testing.T) {
	nc, config, root, path, destinations := configurationSecretFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Reproduce a Kubernetes projected-volume leaf through ..data. Rotation
	// swaps the symlink, not the pathname the worker already knows.
	for _, name := range []string{"..one", "..two"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "key"), []byte(projectedCredential), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("..one", filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../..data/key", path); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolveConfigurationSecrets(nc, config, root, destinations)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..two", filepath.Join(root, "..new")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "..new"), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.unchanged(nc, config); err != nil {
		t.Fatal("identical value projection rotation rejected", err)
	}
	if err := os.WriteFile(filepath.Join(root, "..two", "key"), []byte("rotated-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot.unchanged(nc, config) == nil {
		t.Fatal("material rotation accepted")
	}
	if err := os.Remove(filepath.Join(root, "..two", "key")); err != nil {
		t.Fatal(err)
	}
	if snapshot.unchanged(nc, config) == nil {
		t.Fatal("removed projection accepted")
	}
}

func TestConfigurationSecretProjectionRejectsInvalidMaterial(t *testing.T) {
	for _, value := range [][]byte{nil, {0xff}, {'a', 0, 'b'}, bytes.Repeat([]byte("x"), maxConfigurationSecretBytes+1)} {
		nc, config, root, path, destinations := configurationSecretFixture(t)
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveConfigurationSecrets(nc, config, root, destinations); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	t.Run("escaping symlink", func(t *testing.T) {
		nc, config, root, path, destinations := configurationSecretFixture(t)
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte(projectedCredential), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveConfigurationSecrets(nc, config, root, destinations); err != errConfigurationSecret {
			t.Fatal("escaping projection was accepted or exposed filesystem details")
		}
	})
}

func TestConfigurationSecretPointerAndLeafIsolation(t *testing.T) {
	nc, config, root, _, destinations := configurationSecretFixture(t)
	snapshot, err := resolveConfigurationSecrets(nc, config, root, destinations)
	if err != nil {
		t.Fatal(err)
	}
	for _, pointer := range []string{"", "relative", "/trailing~", "/bad~2", "/authentication_and_policy_servers/aaa/00/shared_secret", "/authentication_and_policy_servers/aaa/-/shared_secret", "/authentication_and_policy_servers/aaa/+0/shared_secret", "/authentication_and_policy_servers/aaa/1/shared_secret", "/authentication_and_policy_servers/aaa/0", "/missing/shared_secret"} {
		snapshot.values[0].ref.Path = pointer
		if _, err := snapshot.inject([]byte(projectedSecretModel)); err != errConfigurationSecret {
			t.Fatal("invalid or non-leaf destination accepted")
		}
	}
	snapshot.values[0].ref.Path = projectedSecretPointer
	for _, literal := range []string{`"inline-secret"`, `""`, `{}`, `[]`, `false`} {
		raw := strings.Replace(projectedSecretModel, `"ip_address":`, `"shared_secret":`+literal+`,"ip_address":`, 1)
		if _, err := snapshot.inject([]byte(raw)); err != errConfigurationSecret {
			t.Fatal("inline value overwritten")
		}
	}
	snapshot.values[0].ref.Path = "/a~1b/~0secret"
	if _, err := snapshot.inject([]byte(`{"catalyst_center":{"system_settings":{"a/b":{"~secret":null}}}}`)); err != nil {
		t.Fatal("valid RFC6901 escape rejected", err)
	}
}

type rotatingSecretJournalClient struct {
	ctrlclient.Client
	phase  string
	rotate func() error
}
type rotatingSecretJournalWriter struct {
	ctrlclient.SubResourceWriter
	phase  string
	rotate func() error
}

func (c rotatingSecretJournalClient) Status() ctrlclient.SubResourceWriter {
	return rotatingSecretJournalWriter{c.Client.Status(), c.phase, c.rotate}
}
func (w rotatingSecretJournalWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if nc, ok := obj.(*ciskov1.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == w.phase {
		return w.rotate()
	}
	return nil
}

func TestConfigurationSecretJournalChecksBeforeAndAfterPermit(t *testing.T) {
	for _, phase := range []string{"missing", "before-prepare", "Prepared", "Submitting", "unchanged"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			nc, config, root, path, destinations := configurationSecretFixture(t)
			nc.Spec.Type = TypeName
			config.Spec.Mode = configapi.NetworkControllerApplyModeApply
			scheme := runtime.NewScheme()
			if err := ciskov1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := configapi.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, config).WithObjects(nc, config).Build()
			if err := k8s.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			a := &adapter{key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, statusReader: k8s, statusWriter: k8s}
			secret, err := resolveConfigurationSecrets(nc, config, root, destinations)
			if err != nil {
				t.Fatal(err)
			}
			source := &configSourceSnapshot{secrets: secret}
			rotate := func() error { return os.WriteFile(path, []byte("new-credential"), 0600) }
			switch phase {
			case "missing":
				source.secrets = nil
			case "before-prepare":
				if err := rotate(); err != nil {
					t.Fatal(err)
				}
			case "Prepared", "Submitting":
				a.statusWriter = rotatingSecretJournalClient{k8s, phase, rotate}
			}
			calls := 0
			submit := func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }
			err = a.submitConfigurationOperation(ctx, config, source, "system_settings", "192.0.2.43", "Create", submit)
			if phase == "unchanged" {
				if err != nil || calls != 1 {
					t.Fatal("valid snapshot prevented submission", err)
				}
			} else if err != errConfigurationSecret || calls != 0 {
				t.Fatal("credential change did not stop API submission", err, calls)
			}
			state := journalState(t, a)
			if phase == "missing" || phase == "before-prepare" {
				if state != nil && state.Operation != nil {
					t.Fatal("invalid credentials reserved operation")
				}
				return
			}
			want := phase
			if phase == "unchanged" {
				want = "Accepted"
			}
			if state == nil || state.Operation == nil || state.Operation.Phase != want {
				t.Fatal("lost durable operation fence")
			}
			encoded, err := json.Marshal(state)
			if err != nil || bytes.Contains(encoded, []byte("credential")) {
				t.Fatal("credential leaked to journal")
			}
			if phase == "Submitting" {
				// A restarted worker can read the new value, but that does not
				// authorize replay of the earlier uncertain submission permit.
				a.statusWriter = k8s
				fresh, err := resolveConfigurationSecrets(nc, config, root, destinations)
				if err != nil {
					t.Fatal(err)
				}
				source.secrets = fresh
				if err := a.submitConfigurationOperation(ctx, config, source, "system_settings", "192.0.2.43", "Create", submit); err != errConfigurationBusy || calls != 0 {
					t.Fatal("new secret snapshot replayed uncertain operation")
				}
			}
		})
	}
}

func TestPrivateComparisonProjectionCannotInjectAndDetectsRotation(t *testing.T) {
	nc, config, root, path, destinations := configurationSecretFixture(t)
	config.Spec.SecretRefs[0].Use = "PrivateComparisonKey"
	if _, err := resolveConfigurationSecrets(nc, config, root, destinations); err == nil {
		t.Fatal("comparison key accepted as value")
	}
	snapshot, err := resolveConfigurationSecretsForUse(nc, config, root, destinations, "PrivateComparisonKey")
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.unchanged(nc, config); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.inject([]byte(projectedSecretModel)); err == nil {
		t.Fatal("comparison key injected into model")
	}
	if err := os.WriteFile(path, []byte("rotated-private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.unchanged(nc, config); err == nil {
		t.Fatal("rotated projection accepted")
	}
}
