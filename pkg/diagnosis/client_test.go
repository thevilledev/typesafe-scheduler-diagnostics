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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDiagnose(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}
		var payload systemOneRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got, want := len(payload.Questions), 5; got != want {
			t.Errorf("question count = %d, want %d", got, want)
		}
		if got, want := payload.State.Reasons[0].Plugin, "NodeAffinity"; got != want {
			t.Errorf("first normalized plugin = %q, want %q", got, want)
		}
		return jsonResponse(http.StatusOK, successfulResponse), nil
	})

	client := testClient(t, transport)
	result, err := client.Diagnose(context.Background(), Failure{
		Reasons: []Reason{
			{Count: 1, Plugin: "TaintToleration", Message: "untolerated taint"},
			{Count: 2, Plugin: "NodeAffinity", Message: "selector mismatch"},
		},
	})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got, want := result.Remediation, RemediationFixWorkloadConstraints; got != want {
		t.Errorf("remediation = %q, want %q", got, want)
	}
	if recommendation, ok := result.Recommendation(DefaultRecommendationConfidence); !ok || !strings.Contains(recommendation, "affinity") {
		t.Errorf("Recommendation() = %q, %v, want constraint advice", recommendation, ok)
	}
}

func TestDiagnoseRetriesOverload(t *testing.T) {
	calls := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return jsonResponse(529, `{"detail":"overloaded"}`), nil
		}
		return jsonResponse(http.StatusOK, successfulResponse), nil
	})

	client := testClient(t, transport)
	var waits []time.Duration
	client.wait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	if _, err := client.Diagnose(context.Background(), Failure{}); err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if got, want := calls, 3; got != want {
		t.Errorf("request count = %d, want %d", got, want)
	}
	if got, want := fmt.Sprint(waits), "[100ms 200ms]"; got != want {
		t.Errorf("retry waits = %s, want %s", got, want)
	}
}

func TestDiagnoseRejectsUnexpectedChoice(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Replace(successfulResponse, "fix_workload_constraints", "reboot_everything", 1)), nil
	})
	client := testClient(t, transport)
	_, err := client.Diagnose(context.Background(), Failure{})
	if err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("Diagnose error = %v, want unknown option error", err)
	}
}

func TestRecommendationConfidenceGate(t *testing.T) {
	tests := []struct {
		name string
		in   Result
		want bool
	}{
		{name: "known and confident", in: Result{Remediation: RemediationScaleCluster, Confidence: 0.9}, want: true},
		{name: "low confidence", in: Result{Remediation: RemediationScaleCluster, Confidence: 0.6}},
		{name: "unknown", in: Result{Remediation: RemediationUnknown, Confidence: 0.99}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, got := test.in.Recommendation(DefaultRecommendationConfidence)
			if got != test.want {
				t.Errorf("Recommendation() advice = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewClientRejectsEmptyKey(t *testing.T) {
	if _, err := NewClient("  "); err == nil {
		t.Fatal("NewClient() error = nil, want an error")
	}
}

func testClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	client, err := NewClient("test-key")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.httpClient = &http.Client{Transport: transport}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const successfulResponse = `{
  "model": "jev-test",
  "answers": {
    "capacity_shortfall": {"type": "noul", "noul": 0.04},
    "constraint_mismatch": {"type": "noul", "noul": 0.96},
    "storage_or_device_block": {"type": "noul", "noul": 0.02},
    "transient_state": {"type": "noul", "noul": 0.03},
    "remediation": {
      "type": "choice",
      "choice": "fix_workload_constraints",
      "probabilities": {
        "wait": 0.01,
        "scale_cluster": 0.01,
        "fix_workload_constraints": 0.94,
        "fix_storage_or_devices": 0.01,
        "inspect_scheduler_extension": 0.01,
        "unknown": 0.02
      },
      "confidence": 0.91
    }
  },
  "usage": {"input_tokens": 111, "output_tokens": 22}
}`
