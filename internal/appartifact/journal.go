// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package appartifact

import (
	"context"
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	core "k8s.io/client-go/kubernetes/typed/core/v1"
)

// Installation records are stored in an operator-owned ConfigMap, never in
// workload annotations. ResourceVersion is the compare-and-swap boundary.
type Installation struct {
	SourceImage      string `json:"sourceImage,omitempty"`
	Version          int    `json:"version"`
	Device           string `json:"device,omitempty"`
	PodUID           string `json:"podUID,omitempty"`
	AppID            string `json:"appID,omitempty"`
	DescriptorDigest string `json:"descriptorDigest,omitempty"`
	PackageDigest    string `json:"packageDigest,omitempty"`
	StagedPath       string `json:"stagedPath,omitempty"`
	ConfigDigest     string `json:"configDigest,omitempty"`

	Identity string `json:"identity"`
	Phase    string `json:"phase"`
}
type Journal struct {
	Maps core.ConfigMapInterface
	Name string
	UID  types.UID
}

func (j Journal) Load(ctx context.Context, key string) (Installation, string, error) {
	cm, err := j.Maps.Get(ctx, j.Name, metav1.GetOptions{})
	if err != nil {
		return Installation{}, "", err
	}
	if j.UID != "" && cm.UID != j.UID {
		return Installation{}, "", fmt.Errorf("installation journal was replaced")
	}
	var record Installation
	if raw, ok := cm.Data[key]; ok {
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return record, "", fmt.Errorf("invalid installation journal: %w", err)
		}
		if record.Version != 1 || record.Identity == "" || record.Phase == "" {
			return record, "", fmt.Errorf("unsupported or incomplete installation record")
		}
	}
	return record, cm.ResourceVersion, nil
}
func (j Journal) Save(ctx context.Context, key, version string, record Installation) error {
	cm, err := j.Maps.Get(ctx, j.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if j.UID != "" && cm.UID != j.UID {
		return fmt.Errorf("installation journal was replaced")
	}
	if cm.ResourceVersion != version {
		return fmt.Errorf("installation journal changed concurrently; retry observation")
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	record.Version = 1
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	cm.Data[key] = string(raw)
	_, err = j.Maps.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}
