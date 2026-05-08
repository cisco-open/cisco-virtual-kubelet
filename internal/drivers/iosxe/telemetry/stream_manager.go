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
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type stateUpdater func(name string, mutate func(*SubscriptionState))

type StreamManager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *SubscribeClient
	logger    logr.Logger
	events    chan NotificationEvent
	reconnect *configv1alpha1.ReconnectConfig
	update    stateUpdater

	mu         sync.Mutex
	generation int64
	subs       map[string]configv1alpha1.TelemetrySubscription
	streams    map[bucketKey]*streamHandle
	wg         sync.WaitGroup
}

type StreamManagerOptions struct {
	Logger          logr.Logger
	ChannelCapacity int
	Reconnect       *configv1alpha1.ReconnectConfig
	Update          stateUpdater
}

func NewStreamManager(ctx context.Context, client *SubscribeClient, opts StreamManagerOptions) *StreamManager {
	capacity := opts.ChannelCapacity
	if capacity <= 0 {
		capacity = DefaultEventChannelCapacity
	}
	mctx, cancel := context.WithCancel(ctx)
	return &StreamManager{
		ctx:       mctx,
		cancel:    cancel,
		client:    client,
		logger:    opts.Logger,
		events:    make(chan NotificationEvent, capacity),
		reconnect: opts.Reconnect,
		update:    opts.Update,
		subs:      map[string]configv1alpha1.TelemetrySubscription{},
		streams:   map[bucketKey]*streamHandle{},
	}
}

func (m *StreamManager) Events() <-chan NotificationEvent {
	if m == nil {
		return nil
	}
	return m.events
}

func (m *StreamManager) SetReconnectConfig(cfg *configv1alpha1.ReconnectConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnect = cfg
}

func (m *StreamManager) Stop() {
	if m == nil {
		return
	}
	m.cancel()
	m.mu.Lock()
	for _, h := range m.streams {
		h.cancel()
	}
	m.streams = map[bucketKey]*streamHandle{}
	m.mu.Unlock()
	m.wg.Wait()
	close(m.events)
}

func (m *StreamManager) UpsertSubscription(spec configv1alpha1.TelemetrySubscription) error {
	if m == nil {
		return nil
	}
	if !subscriptionEnabled(spec) {
		m.RemoveSubscription(spec.Name)
		return nil
	}
	for _, p := range spec.Paths {
		if _, err := parsePath(p); err != nil {
			return fmt.Errorf("subscription %q path %q: %w", spec.Name, p, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[spec.Name] = spec
	m.rebuildLocked()
	return nil
}

func (m *StreamManager) RemoveSubscription(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, name)
	m.rebuildLocked()
}

func (m *StreamManager) rebuildLocked() {
	for _, h := range m.streams {
		h.cancel()
	}
	m.streams = map[bucketKey]*streamHandle{}

	buckets := map[bucketKey][]configv1alpha1.TelemetrySubscription{}
	for _, sub := range m.subs {
		key := bucketFor(sub)
		buckets[key] = append(buckets[key], sub)
	}
	keys := make([]bucketKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, key := range keys {
		subs := buckets[key]
		sort.Slice(subs, func(i, j int) bool { return subs[i].Name < subs[j].Name })
		m.generation++
		ctx, cancel := context.WithCancel(m.ctx)
		h := newStreamHandle(key, subs, StreamID(fmt.Sprintf("%s-%d", key.String(), m.generation)), cancel)
		m.streams[key] = h
		m.wg.Add(1)
		go func(handle *streamHandle) {
			defer m.wg.Done()
			m.runStream(ctx, handle)
		}(h)
	}
}

func (m *StreamManager) runStream(ctx context.Context, h *streamHandle) {
	backoff := NewReconnectState(m.reconnect)
	for {
		err := m.openAndDrain(ctx, h)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		delay, ok := backoff.Next()
		for _, name := range h.subNames {
			m.updateState(name, func(st *SubscriptionState) {
				st.StreamID = h.id
				st.Running = false
				st.LastError = err.Error()
				st.CurrentBackoff = delay
				if ok {
					st.Reconnects++
				} else {
					st.Failed = true
				}
			})
		}
		if !ok {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *StreamManager) openAndDrain(ctx context.Context, h *streamHandle) error {
	if m.client == nil || m.client.Client == nil {
		return fmt.Errorf("subscribe client is nil")
	}
	stream, err := m.client.Client.Subscribe(m.client.AuthContext(ctx))
	if err != nil {
		return fmt.Errorf("gnmi Subscribe open: %w", err)
	}
	if err := stream.Send(&gpb.SubscribeRequest{
		Request: &gpb.SubscribeRequest_Subscribe{
			Subscribe: &gpb.SubscriptionList{
				Mode:         gpb.SubscriptionList_STREAM,
				Encoding:     encodingEnum(h.bucket.encoding),
				Subscription: h.subscriptions,
			},
		},
	}); err != nil {
		return fmt.Errorf("gnmi Subscribe send: %w", err)
	}
	for _, name := range h.subNames {
		m.updateState(name, func(st *SubscriptionState) {
			st.StreamID = h.id
			st.Running = true
			st.Failed = false
			st.LastError = ""
			st.CurrentBackoff = 0
		})
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == io.EOF {
				return fmt.Errorf("gnmi Recv: %w", err)
			}
			return fmt.Errorf("gnmi Recv: %w", err)
		}
		notif := resp.GetUpdate()
		if notif == nil {
			continue
		}
		path := notificationPath(notif)
		names := h.match(path)
		if len(names) == 0 {
			names = h.subNames
		}
		event := NotificationEvent{
			StreamID:          h.id,
			SubscriptionNames: names,
			Notification:      notif,
			Path:              path,
			Updates:           len(notif.GetUpdate()) + len(notif.GetDelete()),
		}
		select {
		case m.events <- event:
			now := metav1.Now()
			for _, name := range names {
				m.updateState(name, func(st *SubscriptionState) {
					st.StreamID = h.id
					st.LastUpdate = &now
					st.MessagesReceived++
					st.Running = true
					st.Failed = false
					st.LastError = ""
					st.CurrentBackoff = 0
				})
			}
			m.logger.Info("gnmi notification received",
				"streamID", h.id,
				"path", path,
				"updates", event.Updates,
			)
		case <-ctx.Done():
			return nil
		default:
			for _, name := range names {
				m.updateState(name, func(st *SubscriptionState) {
					if st.DroppedEvents == nil {
						st.DroppedEvents = map[string]int64{}
					}
					st.DroppedEvents[DropReasonBufferOverflow]++
				})
			}
		}
	}
}

func (m *StreamManager) updateState(name string, mutate func(*SubscriptionState)) {
	if m.update != nil {
		m.update(name, mutate)
	}
}

type bucketKey struct {
	encoding       string
	sampleInterval time.Duration
}

func (b bucketKey) String() string {
	return fmt.Sprintf("%s-%s", strings.ToLower(b.encoding), b.sampleInterval)
}

func bucketFor(sub configv1alpha1.TelemetrySubscription) bucketKey {
	return bucketKey{
		encoding:       subscriptionEncoding(sub),
		sampleInterval: sub.SampleInterval.Duration,
	}
}

type streamHandle struct {
	bucket        bucketKey
	id            StreamID
	cancel        context.CancelFunc
	subNames      []string
	pathBySub     map[string][]string
	subscriptions []*gpb.Subscription
}

func newStreamHandle(
	key bucketKey,
	subs []configv1alpha1.TelemetrySubscription,
	id StreamID,
	cancel context.CancelFunc,
) *streamHandle {
	h := &streamHandle{
		bucket:    key,
		id:        id,
		cancel:    cancel,
		pathBySub: map[string][]string{},
	}
	for _, sub := range subs {
		h.subNames = append(h.subNames, sub.Name)
		for _, path := range sub.Paths {
			gpath, _ := parsePath(path)
			h.subscriptions = append(h.subscriptions, &gpb.Subscription{
				Path:              gpath,
				Mode:              subscriptionModeEnum(sub),
				SampleInterval:    uint64(sub.SampleInterval.Duration.Nanoseconds()),
				SuppressRedundant: boolPtrValue(sub.SuppressRedundant),
				HeartbeatInterval: durationPtrNanos(sub.HeartbeatInterval),
			})
			h.pathBySub[sub.Name] = append(h.pathBySub[sub.Name], normalizePath(path))
		}
	}
	return h
}

func (h *streamHandle) match(path string) []string {
	if path == "" {
		return nil
	}
	path = normalizePath(path)
	var names []string
	for _, name := range h.subNames {
		for _, want := range h.pathBySub[name] {
			if strings.HasPrefix(path, want) || strings.HasPrefix(want, path) {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

func boolPtrValue(v *bool) bool {
	return v != nil && *v
}

func durationPtrNanos(v *metav1.Duration) uint64 {
	if v == nil {
		return 0
	}
	return uint64(v.Duration.Nanoseconds())
}

func subscriptionModeEnum(sub configv1alpha1.TelemetrySubscription) gpb.SubscriptionMode {
	switch streamMode(sub) {
	case configv1alpha1.TelemetryStreamModeSample:
		return gpb.SubscriptionMode_SAMPLE
	case configv1alpha1.TelemetryStreamModeOnChange:
		return gpb.SubscriptionMode_ON_CHANGE
	default:
		return gpb.SubscriptionMode_TARGET_DEFINED
	}
}

func encodingEnum(encoding string) gpb.Encoding {
	switch encoding {
	case configv1alpha1.TelemetryEncodingJSONIETF:
		return gpb.Encoding_JSON_IETF
	default:
		return gpb.Encoding_PROTO
	}
}

func parsePath(p string) (*gpb.Path, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return &gpb.Path{}, nil
	}
	p = strings.TrimPrefix(p, "/")
	out := &gpb.Path{}
	for _, raw := range strings.Split(p, "/") {
		if raw == "" {
			continue
		}
		elem, err := parsePathElem(raw)
		if err != nil {
			return nil, err
		}
		out.Elem = append(out.Elem, elem)
	}
	return out, nil
}

func parsePathElem(raw string) (*gpb.PathElem, error) {
	name := raw
	keys := map[string]string{}
	if idx := strings.Index(raw, "["); idx >= 0 {
		name = raw[:idx]
		rest := raw[idx:]
		for rest != "" {
			if !strings.HasPrefix(rest, "[") {
				return nil, fmt.Errorf("malformed key selector %q", raw)
			}
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, fmt.Errorf("malformed key selector %q", raw)
			}
			kv := rest[1:end]
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				return nil, fmt.Errorf("malformed key selector %q", raw)
			}
			keys[parts[0]] = parts[1]
			rest = rest[end+1:]
		}
	} else if idx := strings.Index(raw, "="); idx > 0 {
		name = raw[:idx]
		keys["name"] = raw[idx+1:]
	}
	if idx := strings.Index(name, ":"); idx > 0 {
		name = name[idx+1:]
	}
	if name == "" {
		return nil, fmt.Errorf("empty path element in %q", raw)
	}
	elem := &gpb.PathElem{Name: name}
	if len(keys) > 0 {
		elem.Key = keys
	}
	return elem, nil
}

func notificationPath(n *gpb.Notification) string {
	if n == nil {
		return ""
	}
	if len(n.GetUpdate()) > 0 {
		return joinPath(n.GetPrefix(), n.GetUpdate()[0].GetPath())
	}
	if len(n.GetDelete()) > 0 {
		return joinPath(n.GetPrefix(), n.GetDelete()[0])
	}
	return pathToString(n.GetPrefix())
}

func joinPath(prefix, path *gpb.Path) string {
	if prefix == nil {
		return pathToString(path)
	}
	if path == nil {
		return pathToString(prefix)
	}
	merged := &gpb.Path{Elem: append([]*gpb.PathElem{}, prefix.GetElem()...)}
	merged.Elem = append(merged.Elem, path.GetElem()...)
	return pathToString(merged)
}

func pathToString(p *gpb.Path) string {
	if p == nil || len(p.GetElem()) == 0 {
		return ""
	}
	var b strings.Builder
	for _, elem := range p.GetElem() {
		b.WriteByte('/')
		b.WriteString(elem.GetName())
		if len(elem.GetKey()) > 0 {
			keys := make([]string, 0, len(elem.GetKey()))
			for k := range elem.GetKey() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.WriteByte('[')
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(elem.GetKey()[k])
				b.WriteByte(']')
			}
		}
	}
	return b.String()
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}
