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

// Package plugin implements an informational scheduler PostFilter plugin. It
// copies structured failure evidence to an asynchronous worker and never
// changes a scheduling decision.
package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	fwk "k8s.io/kube-scheduler/framework"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/diagnosis"
)

const (
	// Name is the scheduler framework plugin name.
	Name = "TypeSafeDiagnosis"

	// IntentAnnotation optionally gives the diagnosis bounded, user-supplied
	// context about what the workload is meant to accomplish.
	IntentAnnotation = "diagnostics.typesafe.ai/intent"

	eventReason          = "TypeSafeDiagnosis"
	queueCapacity        = 128
	maxReasons           = 64
	maxTextRunes         = 512
	diagnosisTimeout     = 20 * time.Second
	duplicateWindow      = 5 * time.Minute
	maxRecentFingerprint = 4096
)

// Diagnoser is implemented by the TypeSafe client and by deterministic tests.
type Diagnoser interface {
	Diagnose(context.Context, diagnosis.Failure) (diagnosis.Result, error)
}

type queuedFailure struct {
	pod     *v1.Pod
	failure diagnosis.Failure
}

// Plugin is an informational PostFilter plugin.
type Plugin struct {
	handle    fwk.Handle
	diagnoser Diagnoser
	queue     chan queuedFailure

	mu     sync.Mutex
	recent map[string]time.Time
	now    func() time.Time
}

var _ fwk.PostFilterPlugin = &Plugin{}

// New constructs the plugin. A missing API key is a startup error rather than
// a stream of failed requests hidden in scheduler logs.
func New(ctx context.Context, _ runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
	client, err := diagnosis.NewClient(os.Getenv("TYPESAFE_API_KEY"))
	if err != nil {
		return nil, fmt.Errorf("initialize %s: %w", Name, err)
	}
	plugin := &Plugin{
		handle:    handle,
		diagnoser: client,
		queue:     make(chan queuedFailure, queueCapacity),
		recent:    make(map[string]time.Time),
		now:       time.Now,
	}
	go plugin.run(ctx)
	return plugin, nil
}

// Name returns the scheduler framework plugin name.
func (p *Plugin) Name() string {
	return Name
}

// PostFilter snapshots structured rejection evidence and returns immediately.
// Returning Unschedulable allows later PostFilter plugins, including default
// preemption, to continue normally.
func (p *Plugin) PostFilter(ctx context.Context, _ fwk.CycleState, pod *v1.Pod, statuses fwk.NodeToStatusReader) (*fwk.PostFilterResult, *fwk.Status) {
	nodeInfos, err := p.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		klog.FromContext(ctx).Error(err, "Cannot snapshot nodes for TypeSafe diagnosis", "pod", klog.KObj(pod))
		return nil, fwk.NewStatus(fwk.Unschedulable)
	}
	nodeNames := make([]string, 0, len(nodeInfos))
	for _, nodeInfo := range nodeInfos {
		nodeNames = append(nodeNames, nodeInfo.Node().Name)
	}
	failure := FailureFromPostFilter(pod, nodeNames, statuses)
	fingerprint := failureFingerprint(pod, failure)
	if !p.reserveFingerprint(fingerprint) {
		return nil, fwk.NewStatus(fwk.Unschedulable)
	}

	item := queuedFailure{pod: pod.DeepCopy(), failure: failure}
	select {
	case p.queue <- item:
	default:
		p.releaseFingerprint(fingerprint)
		klog.FromContext(ctx).Info("Dropping TypeSafe diagnosis because the advisory queue is full", "pod", klog.KObj(pod), "capacity", cap(p.queue))
	}
	return nil, fwk.NewStatus(fwk.Unschedulable)
}

func (p *Plugin) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-p.queue:
			p.diagnose(ctx, item)
		}
	}
}

func (p *Plugin) diagnose(parent context.Context, item queuedFailure) {
	ctx, cancel := context.WithTimeout(parent, diagnosisTimeout)
	defer cancel()

	result, err := p.diagnoser.Diagnose(ctx, item.failure)
	if err != nil {
		klog.FromContext(parent).Error(err, "TypeSafe diagnosis failed", "pod", klog.KObj(item.pod))
		return
	}
	recommendation, ok := result.Recommendation(diagnosis.DefaultRecommendationConfidence)
	if !ok {
		klog.FromContext(parent).Info("TypeSafe diagnosis was inconclusive", "pod", klog.KObj(item.pod), "remediation", result.Remediation, "confidence", result.Confidence)
		return
	}

	p.handle.EventRecorder().WithLogger(klog.FromContext(parent)).Eventf(
		item.pod,
		nil,
		v1.EventTypeNormal,
		eventReason,
		"Diagnosing",
		"%s Remediation: %s; confidence: %.2f.",
		recommendation,
		result.Remediation,
		result.Confidence,
	)
}

func (p *Plugin) reserveFingerprint(fingerprint string) bool {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()

	if seen, ok := p.recent[fingerprint]; ok && now.Sub(seen) < duplicateWindow {
		return false
	}
	if len(p.recent) >= maxRecentFingerprint {
		var oldestKey string
		var oldest time.Time
		for key, seen := range p.recent {
			if now.Sub(seen) >= duplicateWindow {
				delete(p.recent, key)
				continue
			}
			if oldestKey == "" || seen.Before(oldest) {
				oldestKey = key
				oldest = seen
			}
		}
		if len(p.recent) >= maxRecentFingerprint && oldestKey != "" {
			delete(p.recent, oldestKey)
		}
	}
	p.recent[fingerprint] = now
	return true
}

func (p *Plugin) releaseFingerprint(fingerprint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.recent, fingerprint)
}

func failureFingerprint(pod *v1.Pod, failure diagnosis.Failure) string {
	payload, _ := json.Marshal(failure)
	hash := sha256.Sum256(append([]byte(pod.UID), payload...))
	return hex.EncodeToString(hash[:])
}

// FailureFromPostFilter converts scheduler-native status data into bounded
// TypeSafe state without formatting and reparsing a FailedScheduling message.
func FailureFromPostFilter(pod *v1.Pod, nodeNames []string, statuses fwk.NodeToStatusReader) diagnosis.Failure {
	failure := diagnosis.Failure{NodeCount: len(nodeNames)}
	if pod != nil {
		failure.Pod = diagnosis.PodSummary{
			Namespace:        pod.Namespace,
			Name:             pod.Name,
			SchedulerName:    pod.Spec.SchedulerName,
			SchedulingIntent: boundedText(pod.Annotations[IntentAnnotation]),
		}
	}
	if statuses == nil {
		return failure
	}

	type reasonKey struct {
		plugin     string
		statusCode string
		message    string
	}
	reasonCounts := make(map[reasonKey]int)
	plugins := make(map[string]struct{})
	addStatus := func(status *fwk.Status, count int) {
		if status == nil || count <= 0 {
			return
		}
		if status.Plugin() != "" {
			plugins[status.Plugin()] = struct{}{}
		}
		for _, message := range status.Reasons() {
			reasonCounts[reasonKey{
				plugin:     status.Plugin(),
				statusCode: status.Code().String(),
				message:    boundedText(message),
			}] += count
		}
	}

	for _, nodeName := range nodeNames {
		addStatus(statuses.Get(nodeName), 1)
	}

	for key, count := range reasonCounts {
		failure.Reasons = append(failure.Reasons, diagnosis.Reason{
			Count:      count,
			Plugin:     key.plugin,
			StatusCode: key.statusCode,
			Message:    key.message,
		})
	}
	sort.Slice(failure.Reasons, func(i, j int) bool {
		if failure.Reasons[i].Plugin != failure.Reasons[j].Plugin {
			return failure.Reasons[i].Plugin < failure.Reasons[j].Plugin
		}
		if failure.Reasons[i].Message != failure.Reasons[j].Message {
			return failure.Reasons[i].Message < failure.Reasons[j].Message
		}
		return failure.Reasons[i].Count < failure.Reasons[j].Count
	})
	if len(failure.Reasons) > maxReasons {
		failure.Reasons = failure.Reasons[:maxReasons]
	}

	for name := range plugins {
		failure.UnschedulablePlugins = append(failure.UnschedulablePlugins, name)
	}
	sort.Strings(failure.UnschedulablePlugins)
	return failure
}

func boundedText(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxTextRunes {
		return value
	}
	return string(runes[:maxTextRunes])
}
