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

// Observation is one completed TypeSafe request and the policy applied to it.
type Observation struct {
	ID                      string                `json:"id"`
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

// Recorder receives completed observations without controlling scheduling.
type Recorder interface {
	Record(Observation)
}

// Memory is a concurrency-safe, bounded, newest-first observation store.
type Memory struct {
	mu       sync.RWMutex
	capacity int
	items    []Observation
}

// NewMemory creates a store that retains at most capacity observations.
func NewMemory(capacity int) *Memory {
	if capacity < 1 {
		capacity = 1
	}
	return &Memory{capacity: capacity}
}

// Record appends an observation and evicts the oldest entry when full.
func (m *Memory) Record(observation Observation) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items = append(m.items, observation)
	if overflow := len(m.items) - m.capacity; overflow > 0 {
		copy(m.items, m.items[overflow:])
		m.items = m.items[:m.capacity]
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
}
