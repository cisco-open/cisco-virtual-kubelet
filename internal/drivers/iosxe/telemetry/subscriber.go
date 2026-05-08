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

package telemetry

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Subscriber owns one telemetry gNMI client connection and all active
// Subscribe RPCs for a CiscoDevice.
type Subscriber struct {
	deviceRef string
	factory   SubscribeClientFactory
	logger    logr.Logger

	channelCapacity int
	reconnect       *configv1alpha1.ReconnectConfig

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	conn    *grpc.ClientConn
	manager *StreamManager
	specs   map[string]configv1alpha1.TelemetrySubscription
	states  map[string]*SubscriptionState
	started bool

	stateChanged chan struct{}
}

type SubscriberOption func(*Subscriber)

func WithLogger(logger logr.Logger) SubscriberOption {
	return func(s *Subscriber) { s.logger = logger }
}

func WithChannelCapacity(capacity int) SubscriberOption {
	return func(s *Subscriber) { s.channelCapacity = capacity }
}

func WithReconnectConfig(cfg *configv1alpha1.ReconnectConfig) SubscriberOption {
	return func(s *Subscriber) { s.reconnect = cfg }
}

func NewSubscriber(deviceRef string, factory SubscribeClientFactory, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{
		deviceRef:       deviceRef,
		factory:         factory,
		logger:          logr.Discard(),
		channelCapacity: DefaultEventChannelCapacity,
		specs:           map[string]configv1alpha1.TelemetrySubscription{},
		states:          map[string]*SubscriptionState{},
		stateChanged:    make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Subscriber) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	factory := s.factory
	specs := make([]configv1alpha1.TelemetrySubscription, 0, len(s.specs))
	for _, spec := range s.specs {
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	s.mu.Unlock()

	if factory == nil {
		return fmt.Errorf("telemetry subscriber: nil SubscribeClientFactory")
	}
	client, err := factory.NewClient(ctx)
	if err != nil {
		return err
	}
	if client.AuthContext == nil {
		client.AuthContext = func(ctx context.Context) context.Context { return ctx }
	}
	child, cancel := context.WithCancel(ctx)
	manager := NewStreamManager(child, client, StreamManagerOptions{
		Logger:          s.logger.WithValues("device", s.deviceRef),
		ChannelCapacity: s.channelCapacity,
		Reconnect:       s.reconnect,
		Update:          s.updateState,
	})

	s.mu.Lock()
	s.ctx = child
	s.cancel = cancel
	s.conn = client.Conn
	s.manager = manager
	s.started = true
	s.mu.Unlock()
	s.signalStateChanged()

	go s.drainEvents(manager.Events())

	for _, spec := range specs {
		if err := manager.UpsertSubscription(spec); err != nil {
			return err
		}
	}
	return nil
}

func (s *Subscriber) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	manager := s.manager
	conn := s.conn
	s.cancel = nil
	s.ctx = nil
	s.manager = nil
	s.conn = nil
	s.started = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if manager != nil {
		manager.Stop()
	}
	if conn != nil {
		_ = conn.Close()
	}
	s.signalStateChanged()
}

func (s *Subscriber) AddSubscription(spec configv1alpha1.TelemetrySubscription) error {
	if s == nil {
		return nil
	}
	if spec.Name == "" {
		return fmt.Errorf("telemetry subscription name is required")
	}
	if spec.Mode != configv1alpha1.TelemetryModeStream {
		return fmt.Errorf("telemetry subscription %q mode %q is not supported", spec.Name, spec.Mode)
	}
	switch subscriptionEncoding(spec) {
	case configv1alpha1.TelemetryEncodingProto, configv1alpha1.TelemetryEncodingJSONIETF:
	default:
		return fmt.Errorf("telemetry subscription %q encoding %q is not supported", spec.Name, spec.Encoding)
	}
	for _, path := range spec.Paths {
		if _, err := parsePath(path); err != nil {
			return fmt.Errorf("telemetry subscription %q path %q: %w", spec.Name, path, err)
		}
	}

	s.mu.Lock()
	s.specs[spec.Name] = spec
	if subscriptionEnabled(spec) {
		s.ensureStateLocked(spec.Name)
	}
	manager := s.manager
	s.mu.Unlock()

	if manager == nil {
		s.signalStateChanged()
		return nil
	}
	if subscriptionEnabled(spec) {
		if err := manager.UpsertSubscription(spec); err != nil {
			return err
		}
	} else {
		manager.RemoveSubscription(spec.Name)
	}
	s.signalStateChanged()
	return nil
}

func (s *Subscriber) RemoveSubscription(name string) {
	if s == nil || name == "" {
		return
	}
	s.mu.Lock()
	delete(s.specs, name)
	delete(s.states, name)
	manager := s.manager
	s.mu.Unlock()
	if manager != nil {
		manager.RemoveSubscription(name)
	}
	s.signalStateChanged()
}

func (s *Subscriber) SetReconnectConfig(cfg *configv1alpha1.ReconnectConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.reconnect = cfg
	manager := s.manager
	s.mu.Unlock()
	if manager != nil {
		manager.SetReconnectConfig(cfg)
	}
}

func (s *Subscriber) StatusFor(names []string) (string, []configv1alpha1.ObservedSubscriptionState) {
	if s == nil {
		return configv1alpha1.IOSXETelemetryPhasePending, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]configv1alpha1.ObservedSubscriptionState, 0, len(names))
	if len(names) == 0 {
		return configv1alpha1.IOSXETelemetryPhasePending, out
	}
	anyRunning := false
	anyPending := false
	anyError := false
	anyFailed := false
	for _, name := range names {
		st, ok := s.states[name]
		if !ok {
			blank := SubscriptionState{Name: name}
			out = append(out, blank.ToStatus())
			anyPending = true
			continue
		}
		copyState := *st
		if st.DroppedEvents != nil {
			copyState.DroppedEvents = make(map[string]int64, len(st.DroppedEvents))
			for k, v := range st.DroppedEvents {
				copyState.DroppedEvents[k] = v
			}
		}
		out = append(out, copyState.ToStatus())
		anyRunning = anyRunning || st.Running
		anyPending = anyPending || !st.Running
		anyError = anyError || st.LastError != ""
		anyFailed = anyFailed || st.Failed
	}
	switch {
	case anyFailed:
		return configv1alpha1.IOSXETelemetryPhaseFailed, out
	case anyError:
		return configv1alpha1.IOSXETelemetryPhaseDegraded, out
	case anyRunning && !anyPending:
		return configv1alpha1.IOSXETelemetryPhaseStreaming, out
	case anyRunning:
		return configv1alpha1.IOSXETelemetryPhaseDegraded, out
	default:
		return configv1alpha1.IOSXETelemetryPhasePending, out
	}
}

func (s *Subscriber) StateChanged() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.stateChanged
}

func (s *Subscriber) Conn() *grpc.ClientConn {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

func (s *Subscriber) updateState(name string, mutate func(*SubscriptionState)) {
	s.mu.Lock()
	st, ok := s.states[name]
	if !ok {
		s.mu.Unlock()
		return
	}
	mutate(st)
	s.mu.Unlock()
	s.signalStateChanged()
}

func (s *Subscriber) ensureStateLocked(name string) *SubscriptionState {
	if st, ok := s.states[name]; ok {
		return st
	}
	st := &SubscriptionState{Name: name, DroppedEvents: map[string]int64{}}
	s.states[name] = st
	return st
}

func (s *Subscriber) signalStateChanged() {
	select {
	case s.stateChanged <- struct{}{}:
	default:
	}
}

func (s *Subscriber) drainEvents(events <-chan NotificationEvent) {
	for range events {
	}
}
