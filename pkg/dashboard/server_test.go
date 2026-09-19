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

package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/diagnosis"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/observation"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/plugin"
)

func TestGenerateCreatesMixedLabeledScenarios(t *testing.T) {
	client := fake.NewSimpleClientset()
	var createdPods []*v1.Pod
	client.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
		pod := action.(clienttesting.CreateAction).GetObject().(*v1.Pod).DeepCopy()
		pod.Name = fmt.Sprintf("%s%d", pod.GenerateName, len(createdPods)+1)
		createdPods = append(createdPods, pod)
		return true, pod, nil
	})
	server, err := New(client, observation.NewMemory(10), "demo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewBufferString(`{"scenario":"mixed","count":4}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if got, want := response.Code, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d: %s", got, want, response.Body.String())
	}

	var gotScenarios []string
	for _, pod := range createdPods {
		gotScenarios = append(gotScenarios, pod.Labels[scenarioLabel])
		if got := pod.Annotations[plugin.ExpectedRemediationAnnotation]; got == "" {
			t.Errorf("Pod %q has no expected remediation", pod.GenerateName)
		}
	}
	if got, want := len(gotScenarios), 4; got != want {
		t.Fatalf("created Pod count = %d, want %d", got, want)
	}
	for index, want := range []string{"capacity", "constraints", "storage", "capacity"} {
		if got := gotScenarios[index]; got != want {
			t.Errorf("scenario %d = %q, want %q", index, got, want)
		}
	}
}

func TestEventStreamPublishesInitialSnapshotAndLifecycle(t *testing.T) {
	store := observation.NewMemory(10)
	store.Record(observation.Observation{ID: "one", Stage: "queued"})
	dashboard, err := New(fake.NewSimpleClientset(), store, "demo")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(dashboard.Handler())
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	readStage := func(want string) {
		t.Helper()
		for scanner.Scan() {
			if !strings.HasPrefix(scanner.Text(), "data: ") {
				continue
			}
			var snapshot snapshotResponse
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Items) != 1 || snapshot.Items[0].Stage != want {
				t.Fatalf("stream snapshot = %#v, want one %s observation", snapshot.Items, want)
			}
			return
		}
		t.Fatalf("stream ended before %s: %v", want, scanner.Err())
	}
	readStage("queued")
	store.Record(observation.Observation{ID: "one", Stage: "evaluating"})
	readStage("evaluating")
	store.Record(observation.Observation{ID: "one", Stage: "complete"})
	readStage("complete")
}

func TestGenerateRejectsUnboundedBatch(t *testing.T) {
	server, err := New(fake.NewSimpleClientset(), observation.NewMemory(10), "demo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewBufferString(`{"scenario":"capacity","count":26}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if got, want := response.Code, http.StatusBadRequest; got != want {
		t.Errorf("status = %d, want %d", got, want)
	}
}

func TestSnapshotExposesContractAndObservations(t *testing.T) {
	store := observation.NewMemory(10)
	store.Record(observation.Observation{
		ID:         "one",
		Validation: observation.ValidationPassed,
		Result: &diagnosis.Result{
			Remediation: diagnosis.RemediationScaleCluster,
		},
	})
	server, err := New(fake.NewSimpleClientset(), store, "demo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	var snapshot snapshotResponse
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got, want := snapshot.Contract.Model, "jev-latest"; got != want {
		t.Errorf("model = %q, want %q", got, want)
	}
	if got, want := len(snapshot.Contract.Questions), 5; got != want {
		t.Errorf("question count = %d, want %d", got, want)
	}
	if got, want := snapshot.Items[0].ID, "one"; got != want {
		t.Errorf("observation ID = %q, want %q", got, want)
	}
}
