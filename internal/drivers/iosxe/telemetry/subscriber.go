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
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state"
)

// MappingProfile carries spec-level mapping/output/cardinality/timestamp
// configuration shared by every subscription on a Subscriber.
type MappingProfile struct {
	Mapping           *configv1alpha1.MappingConfig
	Classifier        classifier.Classifier
	Output            configv1alpha1.OutputConfig
	Budgets           configv1alpha1.BudgetConfig
	CardinalityLimits *configv1alpha1.CardinalityLimits
	Timestamps        *configv1alpha1.TimestampConfig
}

// Subscriber owns one telemetry gNMI client connection and all active
// Subscribe RPCs for a CiscoDevice.
type Subscriber struct {
	deviceRef string
	factory   SubscribeClientFactory
	logger    logr.Logger

	channelCapacity int
	reconnect       *configv1alpha1.ReconnectConfig
	mapper          *mapper.Mapper
	logsEmitter     *emit.LogsEmitter
	metricsEmitter  *emit.MetricsEmitter
	tracesEmitter   *emit.TracesEmitter
	selfMetrics     *emit.SelfMetrics
	resourceAttrs   map[string]string
	stateCache      *state.Cache
	appConsumer     state.AppEventConsumer
	correlation     *correlation.Cache

	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	conn           *grpc.ClientConn
	connRelease    func()
	manager        *StreamManager
	specs          map[string]configv1alpha1.TelemetrySubscription
	states         map[string]*SubscriptionState
	profiles       map[string]MappingProfile
	defaultProfile MappingProfile
	started        bool

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

// WithMapper attaches a telemetry Mapper. When nil, drainEvents is a no-op.
func WithMapper(m *mapper.Mapper) SubscriberOption {
	return func(s *Subscriber) { s.mapper = m }
}

// WithLogsEmitter attaches the OTel logs emitter that consumes mapped events
// where Signal=logs.
func WithLogsEmitter(e *emit.LogsEmitter) SubscriberOption {
	return func(s *Subscriber) { s.logsEmitter = e }
}

// WithMetricsEmitter attaches the OTel metrics emitter that consumes mapped
// events where Signal=metrics.
func WithMetricsEmitter(e *emit.MetricsEmitter) SubscriberOption {
	return func(s *Subscriber) { s.metricsEmitter = e }
}

// WithTracesEmitter attaches the OTel traces emitter that consumes mapped
// events where Signal=traces.
func WithTracesEmitter(e *emit.TracesEmitter) SubscriberOption {
	return func(s *Subscriber) { s.tracesEmitter = e }
}

// WithSelfMetrics attaches the shared SelfMetrics. The Subscriber threads it
// down to the StreamManager so stream-level counters report to the same OTel
// pipeline as emitter-level counters.
func WithSelfMetrics(self *emit.SelfMetrics) SubscriberOption {
	return func(s *Subscriber) { s.selfMetrics = self }
}

// WithResourceAttributes seeds the per-event resource attributes (device,
// service.name, etc.) added to every mapped record alongside the mapping
// configured ResourceAttributes leaves.
func WithResourceAttributes(attrs map[string]string) SubscriberOption {
	return func(s *Subscriber) {
		if attrs == nil {
			return
		}
		s.resourceAttrs = make(map[string]string, len(attrs))
		for k, v := range attrs {
			s.resourceAttrs[k] = v
		}
	}
}

// WithStateCache attaches the shared MDT state cache that receives every
// mapped event before signal-specific emitters consume it.
func WithStateCache(cache *state.Cache) SubscriberOption {
	return func(s *Subscriber) { s.stateCache = cache }
}

// WithAppEventConsumer attaches a non-blocking consumer for app-hosting state
// events. The provider's PodNotifier bridge implements this interface.
func WithAppEventConsumer(consumer state.AppEventConsumer) SubscriberOption {
	return func(s *Subscriber) { s.appConsumer = consumer }
}

// WithCorrelationCache attaches the app-ID to SpanContext cache used to parent
// MDT recovery spans under the VK admission trace that created the app.
func WithCorrelationCache(cache *correlation.Cache) SubscriberOption {
	return func(s *Subscriber) { s.correlation = cache }
}

// WithMappingProfile installs a default profile applied to subscriptions that
// have no per-subscription profile set. Useful for tests; production wiring
// should call SetSubscriptionProfile per subscription.
func WithMappingProfile(p MappingProfile) SubscriberOption {
	return func(s *Subscriber) { s.defaultProfile = p }
}

// SetSubscriptionProfile installs the mapping profile for one subscription.
// Distinct CRs targeting the same device get distinct profiles; nothing is
// shared across CRs. The metrics emitter's instrument cap is recomputed as
// the maximum of all installed profiles. SetTransitions is the union of
// transition rules across all profiles (the traces emitter does not care
// which CR contributed which rule — its transition tracker is keyed by
// path+entity).
func (s *Subscriber) SetSubscriptionProfile(name string, p MappingProfile) {
	if s == nil || name == "" {
		return
	}
	s.mu.Lock()
	if s.profiles == nil {
		s.profiles = map[string]MappingProfile{}
	}
	s.profiles[name] = p
	transitions := unionTransitionsLocked(s.profiles)
	maxInstruments := maxInstrumentsLocked(s.profiles)
	tracesEmitter := s.tracesEmitter
	metricsEmitter := s.metricsEmitter
	s.mu.Unlock()
	if tracesEmitter != nil {
		tracesEmitter.SetTransitions(transitions)
	}
	if metricsEmitter != nil && maxInstruments > 0 {
		metricsEmitter.SetMaxInstruments(maxInstruments)
	}
}

// RemoveSubscriptionProfile drops the per-subscription profile when its
// owning CR is deleted or when the subscription is removed from a spec.
func (s *Subscriber) RemoveSubscriptionProfile(name string) {
	if s == nil || name == "" {
		return
	}
	s.mu.Lock()
	delete(s.profiles, name)
	transitions := unionTransitionsLocked(s.profiles)
	maxInstruments := maxInstrumentsLocked(s.profiles)
	tracesEmitter := s.tracesEmitter
	metricsEmitter := s.metricsEmitter
	s.mu.Unlock()
	if tracesEmitter != nil {
		tracesEmitter.SetTransitions(transitions)
	}
	if metricsEmitter != nil && maxInstruments > 0 {
		metricsEmitter.SetMaxInstruments(maxInstruments)
	}
}

// unionTransitionsLocked must be called with s.mu held.
func unionTransitionsLocked(profiles map[string]MappingProfile) []configv1alpha1.Transition {
	if len(profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []configv1alpha1.Transition
	seen := map[string]struct{}{}
	for _, name := range names {
		for _, t := range mappingTransitions(profiles[name].Mapping) {
			key := t.Path + "\x00" + strings.Join(t.HealthyValues, "\x00") + "\x00" + strings.Join(t.UnhealthyValues, "\x00")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// maxInstrumentsLocked must be called with s.mu held.
func maxInstrumentsLocked(profiles map[string]MappingProfile) int {
	max := 0
	for _, p := range profiles {
		if p.CardinalityLimits == nil {
			continue
		}
		if int(p.CardinalityLimits.MaxInstruments) > max {
			max = int(p.CardinalityLimits.MaxInstruments)
		}
	}
	return max
}

func NewSubscriber(deviceRef string, factory SubscribeClientFactory, opts ...SubscriberOption) *Subscriber {
	s := &Subscriber{
		deviceRef:       deviceRef,
		factory:         factory,
		logger:          logr.Discard(),
		channelCapacity: DefaultEventChannelCapacity,
		specs:           map[string]configv1alpha1.TelemetrySubscription{},
		states:          map[string]*SubscriptionState{},
		profiles:        map[string]MappingProfile{},
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
		Device:          s.deviceRef,
		SelfMetrics:     s.selfMetrics,
	})

	s.mu.Lock()
	s.ctx = child
	s.cancel = cancel
	s.conn = client.Conn
	s.connRelease = client.Release
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
	release := s.connRelease
	s.cancel = nil
	s.ctx = nil
	s.manager = nil
	s.conn = nil
	s.connRelease = nil
	s.started = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if manager != nil {
		manager.Stop()
	}
	// Conn ownership: when the factory supplied a Release hook (pool-
	// backed dial), use it and leave the underlying conn alone for any
	// other ClassTelemetry leases. Otherwise fall back to Close() so the
	// original "telemetry owns its dial" contract still holds.
	if release != nil {
		release()
	} else if conn != nil {
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

func (s *Subscriber) bumpLogRecords(name string, n int64) {
	s.updateState(name, func(st *SubscriptionState) { st.LogRecordsEmitted += n })
}

func (s *Subscriber) bumpMetricPoints(name string, n int64) {
	s.updateState(name, func(st *SubscriptionState) { st.MetricPointsEmitted += n })
}

func (s *Subscriber) recordMappedDrops(name string, events []mapper.MappedEvent) {
	var drops map[string]int64
	for _, ev := range events {
		if ev.Signal != mapper.SignalKindDrop {
			continue
		}
		if drops == nil {
			drops = map[string]int64{}
		}
		reason := ev.DropReason
		if reason == "" {
			reason = "unknown"
		}
		drops[reason]++
	}
	if len(drops) == 0 {
		return
	}
	s.updateState(name, func(st *SubscriptionState) {
		if st.DroppedEvents == nil {
			st.DroppedEvents = map[string]int64{}
		}
		for k, v := range drops {
			st.DroppedEvents[k] += v
		}
	})
}

func (s *Subscriber) drainEvents(events <-chan NotificationEvent) {
	useMapper := s.mapper != nil
	for ev := range events {
		if !useMapper {
			continue
		}
		s.mu.Lock()
		resAttrs := s.resourceAttrs
		stateCache := s.stateCache
		appConsumer := s.appConsumer
		corr := s.correlation
		emitCtx := s.ctx
		defaultProfile := s.defaultProfile
		profilesByName := make(map[string]MappingProfile, len(ev.SubscriptionNames))
		var subs []string
		for _, name := range ev.SubscriptionNames {
			if _, ok := s.specs[name]; !ok {
				continue
			}
			subs = append(subs, name)
			if p, ok := s.profiles[name]; ok {
				profilesByName[name] = p
			} else {
				profilesByName[name] = defaultProfile
			}
		}
		s.mu.Unlock()
		if emitCtx == nil {
			emitCtx = context.Background()
		}
		for _, name := range subs {
			profile := profilesByName[name]
			ctx := mapper.EventContext{
				Device:             s.deviceRef,
				Subscription:       name,
				StreamID:           string(ev.StreamID),
				StreamEpoch:        ev.StreamEpoch,
				Mapping:            profile.Mapping,
				Classifier:         profile.Classifier,
				Output:             profile.Output,
				CardinalityLimits:  profile.CardinalityLimits,
				Timestamps:         profile.Timestamps,
				ResourceAttributes: resAttrs,
				ReceiveTime:        time.Now(),
			}
			startProcessing := time.Now()
			mapped := s.mapper.Process(ev.Notification, ctx)
			if len(mapped) == 0 {
				s.selfMetrics.RecordProcessingDuration(emitCtx, time.Since(startProcessing).Seconds(), s.deviceRef, name)
				continue
			}
			if stateCache != nil {
				stateCache.ApplyMappedEvents(mapped)
			}
			logsEmitted, metricsEmitted := s.emitMappedEventsWithCorrelation(
				emitCtx, corr, appConsumer, name, mapped, profile,
			)
			if logsEmitted > 0 {
				s.bumpLogRecords(name, int64(logsEmitted))
			}
			if metricsEmitted > 0 {
				s.bumpMetricPoints(name, int64(metricsEmitted))
			}
			s.recordMappedDrops(name, mapped)
			s.selfMetrics.RecordProcessingDuration(emitCtx, time.Since(startProcessing).Seconds(), s.deviceRef, name)
		}
	}
}

func (s *Subscriber) emitMappedEventsWithCorrelation(
	emitCtx context.Context,
	cache *correlation.Cache,
	appConsumer state.AppEventConsumer,
	subscriptionName string,
	mapped []mapper.MappedEvent,
	profile MappingProfile,
) (logsEmitted, metricsEmitted int) {
	policyKey := s.deviceRef + "\x00" + subscriptionName
	for i := range mapped {
		events := mapped[i : i+1]
		appEvents := state.ExtractAppEvents(events)
		eventCtx := cachedEventCorrelationContext(emitCtx, cache, appEvents)
		if appConsumer != nil {
			for _, appEvent := range appEvents {
				if ok := appConsumer.ObserveAppEvent(eventCtx, appEvent); !ok && s.selfMetrics != nil {
					s.selfMetrics.IncNotifierDropped(eventCtx, "consumer_backpressure")
				}
			}
		}
		if s.logsEmitter != nil {
			logsEmitted += s.logsEmitter.EmitWithPolicy(eventCtx, events, profile.Output.Logs, profile.Budgets, policyKey)
		}
		if s.metricsEmitter != nil {
			metricsEmitted += s.metricsEmitter.Emit(eventCtx, events)
		}
		if s.tracesEmitter != nil {
			s.tracesEmitter.Emit(eventCtx, events)
		}
	}
	return logsEmitted, metricsEmitted
}

func cachedEventCorrelationContext(ctx context.Context, cache *correlation.Cache, appEvents []state.AppEvent) context.Context {
	if cache == nil {
		return ctx
	}
	for _, appEvent := range appEvents {
		sc, lifecycleID, age, ok := cache.GetWithLifecycle(appEvent.Device, appEvent.AppID)
		if !ok {
			continue
		}
		ctx = correlation.WithLifecycleID(ctx, lifecycleID)
		switch cache.RelationshipForAge(age) {
		case correlation.RelationshipParent:
			ctx = correlation.WithSpanContext(ctx, sc)
		default:
			ctx = correlation.WithSpanLink(ctx, sc)
		}
		break
	}
	return ctx
}

func mappingTransitions(mapping *configv1alpha1.MappingConfig) []configv1alpha1.Transition {
	if mapping == nil {
		return nil
	}
	return mapping.Transitions
}
