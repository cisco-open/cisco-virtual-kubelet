// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package appartifact

import (
	"context"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func TestJournalRejectsStaleVersionAndSurvivesNewClient(t *testing.T) {
	ctx := context.Background()
	kube := fake.NewClientset(&core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "journal", Namespace: "operators", ResourceVersion: "1"}})
	journal := Journal{Maps: kube.CoreV1().ConfigMaps("operators"), Name: "journal"}
	if err := journal.Save(ctx, "app", "old", Installation{Identity: "id", Phase: "installPending"}); err == nil {
		t.Fatal("accepted stale write")
	}
	if err := journal.Save(ctx, "app", "1", Installation{Identity: "id", Phase: "installPending"}); err != nil {
		t.Fatal(err)
	}
	restarted := Journal{Maps: kube.CoreV1().ConfigMaps("operators"), Name: "journal"}
	got, _, err := restarted.Load(ctx, "app")
	if err != nil || got.Identity != "id" || got.Phase != "installPending" {
		t.Fatalf("lost intent: %+v %v", got, err)
	}
	if _, _, err = (Journal{Maps: journal.Maps, Name: "missing"}).Load(ctx, "app"); err == nil {
		t.Fatal("missing journal accepted")
	}
}
