// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type strippingCreatedReceiptClient struct{ ctrlclient.Client }
type strippingCreatedReceiptWriter struct{ ctrlclient.SubResourceWriter }

func (c strippingCreatedReceiptClient) Status() ctrlclient.SubResourceWriter {
	return strippingCreatedReceiptWriter{c.Client.Status()}
}
func (w strippingCreatedReceiptWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.CreatedResourceID != "" {
		copy := nc.DeepCopy()
		copy.Status.Configuration.Operation.CreatedResourceID = ""
		return w.SubResourceWriter.Update(ctx, copy, opts...)
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestCreatedResourceReceiptSurvivesRestartWithoutReplay(t *testing.T) {
	for _, mode := range []string{"valid", "conflict", "replaced", "stripped", "lost", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			if mode == "conflict" || mode == "replaced" {
				a.statusWriter = &receiptConflictClient{Client: a.statusWriter, replace: mode == "replaced"}
			}
			if mode == "stripped" {
				a.statusWriter = strippingCreatedReceiptClient{a.statusWriter}
			}
			calls := 0
			write := &configurationWrite{resource: "created-resource", action: "Create", submitCreated: func(context.Context) (string, error) {
				calls++
				if mode == "lost" {
					return "", errors.New("lost response")
				}
				if mode == "invalid" {
					return "", nil
				}
				return "native-id", nil
			}}
			err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", write)
			valid := mode == "valid" || mode == "conflict"
			if (err == nil) != valid {
				t.Fatal("wrong persistence outcome", err)
			}
			op := journalState(t, a).Operation
			if valid && (op.Phase != "Verifying" || op.CreatedResourceID != "native-id" || op.TaskID != "" || op.SubmittedAt == nil) {
				t.Fatal("created identity was not durable")
			}
			if (mode == "lost" || mode == "invalid") && (op.Phase != "Unknown" || op.CreatedResourceID != "") {
				t.Fatal("uncertainty lost")
			}
			restarted := *a
			if err := restarted.submitConfigurationWrite(context.Background(), config, nil, "sites", write); err == nil || calls != 1 {
				t.Fatal("native create replayed", calls, err)
			}
		})
	}
}

func TestCreatedResourceReceiptCannotUsePhysicalOrUpdatePath(t *testing.T) {
	for _, mode := range []string{"physical", "update", "existing-target", "two-callbacks"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			write := &configurationWrite{resource: "resource", action: "Create", submitCreated: func(context.Context) (string, error) { t.Fatal("unexpected create"); return "id", nil }}
			switch mode {
			case "physical":
				write.identity = configurationResourceIdentity{target: "device", serial: "serial"}
			case "update":
				write.action = "Update"
			case "existing-target":
				write.identity.target = "existing"
			case "two-callbacks":
				write.submitCompleted = func(context.Context) error { t.Fatal("unexpected sync write"); return nil }
			}
			if err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", write); err == nil {
				t.Fatal("invalid creation receipt path accepted")
			}
		})
	}
}
