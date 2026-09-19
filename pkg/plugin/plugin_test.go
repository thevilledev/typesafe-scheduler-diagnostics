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

package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fwk "k8s.io/kube-scheduler/framework"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/diagnosis"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/observation"
)

func TestFailureFromPostFilter(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "demo",
			Name:      "api",
			UID:       types.UID("pod-uid"),
			Annotations: map[string]string{
				IntentAnnotation:              " Keep the API available across zones. ",
				ExpectedRemediationAnnotation: "withheld-test-answer",
			},
		},
		Spec: v1.PodSpec{SchedulerName: "typesafe-scheduler"},
	}
	statuses := schedulerframework.NewNodeToStatus(
		map[string]*fwk.Status{
			"node-a": fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "node(s) didn't match Pod's node affinity/selector").WithPlugin("NodeAffinity"),
			"node-b": fwk.NewStatus(fwk.Unschedulable, "Insufficient cpu").WithPlugin("NodeResourcesFit"),
		},
		fwk.NewStatus(fwk.UnschedulableAndUnresolvable, "node(s) had untolerated taint").WithPlugin("TaintToleration"),
	)

	failure := FailureFromPostFilter(pod, []string{"node-a", "node-b", "node-c", "node-d"}, statuses)
	encoded, err := json.Marshal(failure)
	if err != nil || strings.Contains(string(encoded), "withheld-test-answer") {
		t.Fatalf("expected answer leaked into model evidence: %s (%v)", encoded, err)
	}
	if got, want := failure.Pod.SchedulingIntent, "Keep the API available across zones."; got != want {
		t.Errorf("intent = %q, want %q", got, want)
	}
	if got, want := len(failure.Reasons), 3; got != want {
		t.Fatalf("reason count = %d, want %d: %#v", got, want, failure.Reasons)
	}
	if got, want := failure.Reasons[0].Plugin, "NodeAffinity"; got != want {
		t.Errorf("first reason plugin = %q, want %q", got, want)
	}
	if got, want := failure.Reasons[2].Count, 2; got != want {
		t.Errorf("absent-node reason count = %d, want %d", got, want)
	}
	if got, want := strings.Join(failure.UnschedulablePlugins, ","), "NodeAffinity,NodeResourcesFit,TaintToleration"; got != want {
		t.Errorf("plugins = %q, want %q", got, want)
	}
}

func TestDiagnoseRecordsValidationWithoutPublishingLowConfidenceAdvice(t *testing.T) {
	now := time.Unix(1_000, 0)
	recorder := &captureRecorder{}
	plugin := &Plugin{
		diagnoser: staticDiagnoser{result: diagnosis.Result{
			Remediation: diagnosis.RemediationScaleCluster,
			Confidence:  0.5,
		}},
		recorder: recorder,
		now:      func() time.Time { return now },
	}
	plugin.diagnose(context.Background(), queuedFailure{
		pod: &v1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("pod-uid")}},
		failure: diagnosis.Failure{
			Pod: diagnosis.PodSummary{Namespace: "demo", Name: "large-worker"},
		},
		expectedRemediation: diagnosis.RemediationScaleCluster,
	})

	if got, want := recorder.observation.Validation, observation.ValidationPassed; got != want {
		t.Errorf("validation = %q, want %q", got, want)
	}
	if recorder.observation.RecommendationPublished {
		t.Error("low-confidence recommendation was marked as published")
	}
	if got := strings.Join(recorder.stages, ","); got != "evaluating,complete" {
		t.Errorf("recorded lifecycle = %q, want evaluating,complete", got)
	}
}

type staticDiagnoser struct {
	result diagnosis.Result
	err    error
}

func (d staticDiagnoser) Diagnose(context.Context, diagnosis.Failure) (diagnosis.Result, error) {
	return d.result, d.err
}

type captureRecorder struct {
	observation observation.Observation
	stages      []string
}

func (r *captureRecorder) Record(value observation.Observation) {
	r.observation = value
	r.stages = append(r.stages, value.Stage)
}

func TestFailureFromPostFilterBoundsText(t *testing.T) {
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{IntentAnnotation: strings.Repeat("x", maxTextRunes+10)}}}
	failure := FailureFromPostFilter(pod, nil, nil)
	if got, want := len([]rune(failure.Pod.SchedulingIntent)), maxTextRunes; got != want {
		t.Errorf("intent length = %d, want %d", got, want)
	}
}

func TestReserveFingerprintDeduplicatesTemporarily(t *testing.T) {
	now := time.Unix(1_000, 0)
	plugin := &Plugin{recent: make(map[string]time.Time), now: func() time.Time { return now }}
	if !plugin.reserveFingerprint("same") {
		t.Fatal("first fingerprint was rejected")
	}
	if plugin.reserveFingerprint("same") {
		t.Fatal("duplicate fingerprint was accepted")
	}
	now = now.Add(duplicateWindow)
	if !plugin.reserveFingerprint("same") {
		t.Fatal("expired fingerprint was rejected")
	}
}

func TestReserveFingerprintBoundsHistory(t *testing.T) {
	now := time.Unix(1_000, 0)
	plugin := &Plugin{recent: make(map[string]time.Time), now: func() time.Time { return now }}
	for i := 0; i <= maxRecentFingerprint; i++ {
		if !plugin.reserveFingerprint(string(rune(i))) {
			t.Fatalf("fingerprint %d was unexpectedly rejected", i)
		}
	}
	if got := len(plugin.recent); got != maxRecentFingerprint {
		t.Errorf("recent fingerprint count = %d, want %d", got, maxRecentFingerprint)
	}
}
