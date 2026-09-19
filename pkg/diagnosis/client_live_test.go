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

package diagnosis

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLive(t *testing.T) {
	if os.Getenv("TYPESAFE_LIVE_TEST") != "1" {
		t.Skip("set TYPESAFE_LIVE_TEST=1 to run the live TypeSafe evaluation")
	}
	client, err := NewClient(os.Getenv("TYPESAFE_API_KEY"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	tests := []struct {
		name            string
		failure         Failure
		wantRemediation Remediation
		primarySignal   func(Signals) float64
	}{
		{
			name: "capacity shortfall",
			failure: Failure{
				Pod:                  PodSummary{Namespace: "demo", Name: "cpu-worker", SchedulerName: "typesafe-scheduler", SchedulingIntent: "Run a batch worker that requires 64 CPUs."},
				NodeCount:            3,
				Reasons:              []Reason{{Count: 3, Plugin: "NodeResourcesFit", StatusCode: "Unschedulable", Message: "Insufficient cpu"}},
				UnschedulablePlugins: []string{"NodeResourcesFit"},
			},
			wantRemediation: RemediationScaleCluster,
			primarySignal:   func(signals Signals) float64 { return signals.CapacityShortfall },
		},
		{
			name: "hard placement constraint",
			failure: Failure{
				Pod:                  PodSummary{Namespace: "demo", Name: "api", SchedulerName: "typesafe-scheduler"},
				NodeCount:            5,
				Reasons:              []Reason{{Count: 5, Plugin: "NodeAffinity", StatusCode: "UnschedulableAndUnresolvable", Message: "node(s) didn't match Pod's node affinity/selector"}},
				UnschedulablePlugins: []string{"NodeAffinity"},
			},
			wantRemediation: RemediationFixWorkloadConstraints,
			primarySignal:   func(signals Signals) float64 { return signals.ConstraintMismatch },
		},
		{
			name: "storage topology",
			failure: Failure{
				Pod:                  PodSummary{Namespace: "demo", Name: "database", SchedulerName: "typesafe-scheduler"},
				NodeCount:            4,
				Reasons:              []Reason{{Count: 4, Plugin: "VolumeBinding", StatusCode: "UnschedulableAndUnresolvable", Message: "node(s) didn't match PersistentVolume's node affinity"}},
				UnschedulablePlugins: []string{"VolumeBinding"},
			},
			wantRemediation: RemediationFixStorageOrDevices,
			primarySignal:   func(signals Signals) float64 { return signals.StorageOrDeviceBlock },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result, err := client.Diagnose(ctx, test.failure)
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			t.Logf("remediation=%s confidence=%.3f signals=%+v tokens=%d/%d", result.Remediation, result.Confidence, result.Signals, result.Usage.InputTokens, result.Usage.OutputTokens)
			if result.Remediation != test.wantRemediation {
				t.Errorf("remediation = %q, want %q", result.Remediation, test.wantRemediation)
			}
			if got := test.primarySignal(result.Signals); got < 0.7 {
				t.Errorf("primary signal = %.3f, want at least 0.7", got)
			}
		})
	}
}
