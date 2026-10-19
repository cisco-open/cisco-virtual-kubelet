// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type receiptConflictClient struct {
	ctrlclient.Client
	attempts int
	replace  bool
	always   bool
}

func (c *receiptConflictClient) Status() ctrlclient.SubResourceWriter {
	return &receiptConflictWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type receiptConflictWriter struct {
	ctrlclient.SubResourceWriter
	c *receiptConflictClient
}

func (w *receiptConflictWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*ciskov1.NetworkController)
	if ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		phase := nc.Status.Configuration.Operation.Phase
		if phase == "Accepted" || phase == "Verifying" {
			w.c.attempts++
			if w.c.attempts == 1 || w.c.always {
				fresh := &ciskov1.NetworkController{}
				if err := w.c.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), fresh); err != nil {
					return err
				}
				fresh.Status.ObservedGeneration = 42
				if w.c.replace {
					fresh.Status.Configuration.Operation.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				}
				if err := w.c.Client.Status().Update(ctx, fresh); err != nil {
					return err
				}
				return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("concurrent status update"))
			}
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
func TestConfigurationReceiptConflictDoesNotReplaySubmission(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(map[bool]string{false: "async", true: "sync"}[synchronous]+map[bool]string{false: "-heartbeat", true: "-replaced"}[replace], func(t *testing.T) {
				a, config := journalFixture(t)
				writer := &receiptConflictClient{Client: a.statusWriter, replace: replace}
				a.statusWriter = writer
				calls := 0
				err := a.submitConfigurationReceipt(context.Background(), config, nil, "sites", "Global/Lab", "Create", func(context.Context) (configurationReceipt, error) {
					calls++
					if synchronous {
						return configurationReceipt{completed: true}, nil
					}
					return configurationReceipt{task: Task{ID: "task-1"}}, nil
				})
				if replace {
					if !errors.Is(err, errConfigurationTransition) {
						t.Fatalf("replaced operation: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				nc, err := a.configurationRecoveryController(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 || nc.Status.ObservedGeneration != 42 {
					t.Fatalf("calls=%d observed=%d", calls, nc.Status.ObservedGeneration)
				}
				op := nc.Status.Configuration.Operation
				if replace {
					if op.ID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || op.Phase != "Submitting" || writer.attempts != 1 {
						t.Fatalf("foreign operation overwritten: %+v", op)
					}
				} else {
					want := "Accepted"
					if synchronous {
						want = "Verifying"
					}
					if op.Phase != want || writer.attempts != 2 || (!synchronous && op.TaskID != "task-1") {
						t.Fatalf("receipt not durable: %+v", op)
					}
				}
			})
		}
	}
}

func TestConfigurationReceiptConflictExhaustionRetainsFence(t *testing.T) {
	a, config := journalFixture(t)
	writer := &receiptConflictClient{Client: a.statusWriter, always: true}
	a.statusWriter = writer
	calls := 0
	submit := func(context.Context) (Task, error) { calls++; return Task{ID: "task-1"}, nil }
	err := a.submitConfigurationOperation(context.Background(), config, nil, "sites", "Global/Lab", "Create", submit)
	if !apierrors.IsConflict(err) || writer.attempts < 2 || writer.attempts > 10 {
		t.Fatalf("unbounded or missing retries: attempts=%d err=%v", writer.attempts, err)
	}
	if op := journalState(t, a).Operation; op.Phase != "Submitting" || op.TaskID != "" {
		t.Fatalf("uncertain request released: %+v", op)
	}
	err = a.submitConfigurationOperation(context.Background(), config, nil, "sites", "Global/Lab", "Create", submit)
	if !errors.Is(err, errConfigurationBusy) || calls != 1 {
		t.Fatalf("request replayed: calls=%d err=%v", calls, err)
	}
}
