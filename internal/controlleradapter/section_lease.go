// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controlleradapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SectionLease binds a section to a config incarnation and one worker session.
// The API reader MUST bypass caches. Unsettled dispatches prohibit takeover.
type SectionLease struct {
	Client                                                       client.Client
	Namespace, ControllerUID, Scope, Section, ConfigUID, Session string
	Now                                                          func() time.Time
}
type MutationReceipt struct {
	State string `json:"state"`
	ID    string `json:"id,omitempty"`
}
type SectionJournal map[string]MutationReceipt

const sectionJournalAnnotation = "cisco.vk/controller-section-journal"
const sectionOwnerAnnotation = "cisco.vk/controller-config-uid"

var ErrSectionOwned = errors.New("controller section is owned or has an unresolved mutation")

func (s *SectionLease) Name() string {
	sum := sha256.Sum256([]byte(s.ControllerUID + "\x00" + s.Scope + "\x00" + s.Section))
	return fmt.Sprintf("cvk-section-%x", sum[:20])
}
func (s *SectionLease) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *SectionLease) Acquire(ctx context.Context) (*coordinationv1.Lease, SectionJournal, error) {
	if s.Client == nil || s.Namespace == "" || s.ControllerUID == "" || s.ConfigUID == "" || s.Session == "" || s.Scope == "" || s.Section == "" {
		return nil, nil, errors.New("incomplete section ownership identity")
	}
	key := types.NamespacedName{Namespace: s.Namespace, Name: s.Name()}
	l := &coordinationv1.Lease{}
	err := s.Client.Get(ctx, key, l)
	now := metav1.NewMicroTime(s.now())
	duration := int32(90)
	if apierrors.IsNotFound(err) {
		holder := s.Session
		l = &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: s.Name(), Annotations: map[string]string{sectionOwnerAnnotation: s.ConfigUID, sectionJournalAnnotation: "{}"}}, Spec: coordinationv1.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, AcquireTime: &now, RenewTime: &now}}
		if err = s.Client.Create(ctx, l); err != nil {
			return nil, nil, err
		}
		return l, SectionJournal{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	j := SectionJournal{}
	if json.Unmarshal([]byte(l.Annotations[sectionJournalAnnotation]), &j) != nil || j == nil {
		return nil, nil, ErrSectionOwned
	}
	if l.Annotations[sectionOwnerAnnotation] != s.ConfigUID {
		return nil, nil, ErrSectionOwned
	}
	if l.Spec.HolderIdentity == nil || l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return nil, nil, ErrSectionOwned
	}
	if *l.Spec.HolderIdentity != s.Session {
		for _, r := range j {
			if r.State == "Dispatched" {
				return nil, nil, ErrSectionOwned
			}
		}
		if s.now().Before(l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second)) {
			return nil, nil, ErrSectionOwned
		}
	}
	l.Spec.HolderIdentity = &s.Session
	l.Spec.RenewTime = &now
	l.Spec.LeaseDurationSeconds = &duration
	if err = s.Client.Update(ctx, l); err != nil {
		return nil, nil, err
	}
	return l, j, nil
}

// Check fences remote dispatch against fresh API-server lease state.
func (s *SectionLease) Check(ctx context.Context, l *coordinationv1.Lease) error {
	live := &coordinationv1.Lease{}
	if err := s.Client.Get(ctx, client.ObjectKeyFromObject(l), live); err != nil {
		return err
	}
	if live.UID != l.UID || live.ResourceVersion != l.ResourceVersion || live.Spec.HolderIdentity == nil || *live.Spec.HolderIdentity != s.Session || live.Annotations[sectionOwnerAnnotation] != s.ConfigUID || live.Spec.RenewTime == nil || live.Spec.LeaseDurationSeconds == nil || !s.now().Before(live.Spec.RenewTime.Add(time.Duration(*live.Spec.LeaseDurationSeconds)*time.Second)) {
		return ErrSectionOwned
	}
	return nil
}

// Save uses resourceVersion CAS. A successful write-ahead save is required
// immediately before dispatch. Only IDs and state enter the durable journal.
func (s *SectionLease) Save(ctx context.Context, l *coordinationv1.Lease, j SectionJournal) error {
	live := &coordinationv1.Lease{}
	if err := s.Client.Get(ctx, client.ObjectKeyFromObject(l), live); err != nil {
		return err
	}
	if live.UID != l.UID || live.ResourceVersion != l.ResourceVersion || live.Spec.HolderIdentity == nil || *live.Spec.HolderIdentity != s.Session || live.Annotations[sectionOwnerAnnotation] != s.ConfigUID || live.Spec.RenewTime == nil || live.Spec.LeaseDurationSeconds == nil || !s.now().Before(live.Spec.RenewTime.Add(time.Duration(*live.Spec.LeaseDurationSeconds)*time.Second)) {
		return ErrSectionOwned
	}
	if len(j) > 128 {
		return errors.New("section journal resource limit exceeded")
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(data) > 60000 {
		return errors.New("section journal size exceeded")
	}
	l.Annotations[sectionJournalAnnotation] = string(data)
	now := metav1.NewMicroTime(s.now())
	l.Spec.RenewTime = &now
	return s.Client.Update(ctx, l)
}
