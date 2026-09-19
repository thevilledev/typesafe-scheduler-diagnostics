/*
Copyright 2026 The TypeSafe Scheduler Diagnostics Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package observation stores bounded, inspectable traces of advisory
// diagnoses. It deliberately keeps expected labels separate from model input.
package observation

import (
	"sync"
	"time"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/diagnosis"
)

// Validation describes whether Jev's selected remediation matched a label
// supplied by the scenario generator.
type Validation string

const (
	ValidationPassed     Validation = "passed"
	ValidationFailed     Validation = "failed"
	ValidationUnlabelled Validation = "unlabelled"
	ValidationError      Validation = "error"
)

// Observation is the latest lifecycle state of one advisory diagnosis.
type Observation struct {
	ID                      string                `json:"id"`
	Stage                   string                `json:"stage"`
	ObservedAt              time.Time             `json:"observed_at"`
	DurationMilliseconds    int64                 `json:"duration_milliseconds"`
	Failure                 diagnosis.Failure     `json:"failure"`
	Result                  *diagnosis.Result     `json:"result,omitempty"`
	ExpectedRemediation     diagnosis.Remediation `json:"expected_remediation,omitempty"`
	Validation              Validation            `json:"validation"`
	Recommendation          string                `json:"recommendation,omitempty"`
	RecommendationPublished bool                  `json:"recommendation_published"`
	Error                   string                `json:"error,omitempty"`
}

// Recorder receives lifecycle observations without controlling scheduling.
type Recorder interface {
	Record(Observation)
}

// Memory is a concurrency-safe, bounded, newest-first observation store.
type Memory struct {
	mu        sync.RWMutex
	capacity  int
	items     []Observation
	listeners map[chan struct{}]struct{}
}

// NewMemory creates a store that retains at most capacity observations.
func NewMemory(capacity int) *Memory {
	if capacity < 1 {
		capacity = 1
	}
	return &Memory{capacity: capacity, listeners: make(map[chan struct{}]struct{})}
}

// Record updates a trace or appends a new one, evicting the oldest when full.
func (m *Memory) Record(observation Observation) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A trace retains its position as it moves from queued to evaluating to
	// complete, so updates do not count as additional requests.
	for index := range m.items {
		if m.items[index].ID == observation.ID {
			m.items[index] = observation
			m.notify()
			return
		}
	}
	m.items = append(m.items, observation)
	if overflow := len(m.items) - m.capacity; overflow > 0 {
		copy(m.items, m.items[overflow:])
		m.items = m.items[:m.capacity]
	}
	m.notify()
}

// Subscribe signals changes without letting a slow browser block scheduling.
// Notifications may coalesce; consumers read the latest snapshot each time.
func (m *Memory) Subscribe() (<-chan struct{}, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := make(chan struct{}, 1)
	m.listeners[changed] = struct{}{}
	return changed, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.listeners, changed)
	}
}

func (m *Memory) notify() {
	for changed := range m.listeners {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
}

// List returns up to limit observations in newest-first order.
func (m *Memory) List(limit int) []Observation {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit < 1 || limit > len(m.items) {
		limit = len(m.items)
	}
	result := make([]Observation, 0, limit)
	for i := len(m.items) - 1; i >= len(m.items)-limit; i-- {
		result = append(result, m.items[i])
	}
	return result
}

// Clear removes all retained observations.
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = nil
	m.notify()
}
