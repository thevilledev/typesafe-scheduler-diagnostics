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

// Package diagnosis turns bounded scheduler evidence into typed advisory
// judgments. Callers retain control of all scheduling and remediation actions.
package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	defaultEndpoint    = "https://api.typesafe.ai/v1/systemone"
	defaultModel       = "jev-latest"
	defaultMaxAttempts = 3
	defaultBackoff     = 100 * time.Millisecond

	// DefaultRecommendationConfidence is conservative because an incorrect
	// diagnosis is more expensive than withholding optional advice.
	DefaultRecommendationConfidence = 0.75
)

// Remediation is an advisory next step selected from a code-owned set.
type Remediation string

const (
	RemediationWait                      Remediation = "wait"
	RemediationScaleCluster              Remediation = "scale_cluster"
	RemediationFixWorkloadConstraints    Remediation = "fix_workload_constraints"
	RemediationFixStorageOrDevices       Remediation = "fix_storage_or_devices"
	RemediationInspectSchedulerExtension Remediation = "inspect_scheduler_extension"
	RemediationUnknown                   Remediation = "unknown"
)

var remediationCriteria = map[string]string{
	string(RemediationWait):                      "The evidence describes a temporary scheduler, controller, preemption, quorum, gate, or resource lifecycle state that should be observed rather than fixed immediately.",
	string(RemediationScaleCluster):              "The workload has a resource requirement that available nodes cannot currently satisfy; adding suitable cluster capacity is the most relevant next step.",
	string(RemediationFixWorkloadConstraints):    "Hard placement constraints declared by the pod or workload, such as pod affinity, node selectors, tolerations, ports, or pod topology rules, exclude otherwise usable nodes. This excludes constraints originating from storage or device objects.",
	string(RemediationFixStorageOrDevices):       "Persistent volumes, storage topology or node affinity, ResourceClaims, DRA devices, or device availability are the primary blocker, even when the symptom is a topology or node-affinity mismatch.",
	string(RemediationInspectSchedulerExtension): "An extender, out-of-tree plugin, or scheduler internal error needs investigation; the evidence does not point to an ordinary workload or capacity correction.",
	string(RemediationUnknown):                   "The evidence is insufficient, contradictory, or does not clearly fit another option.",
}

var recommendationText = map[Remediation]string{
	RemediationWait:                      "The evidence points to a transient scheduler or controller state; wait for that state to change, then recheck the Pod.",
	RemediationScaleCluster:              "Available cluster capacity is the likely blocker; inspect allocatable resources and autoscaling before changing workload constraints.",
	RemediationFixWorkloadConstraints:    "Hard workload constraints are the likely blocker; inspect affinity, selectors, tolerations, ports, and topology rules.",
	RemediationFixStorageOrDevices:       "Storage or device allocation is the likely blocker; inspect PVC/PV topology, ResourceClaims, and DRA device availability.",
	RemediationInspectSchedulerExtension: "A scheduler extension or internal error needs investigation; inspect the named plugin or extender and its logs.",
}

// Failure is the bounded scheduler state sent for advisory diagnosis. Callers
// must not include Secrets, environment values, or complete Kubernetes objects.
type Failure struct {
	Pod                  PodSummary `json:"pod"`
	NodeCount            int        `json:"node_count"`
	Reasons              []Reason   `json:"reasons"`
	UnschedulablePlugins []string   `json:"unschedulable_plugins,omitempty"`
	PendingPlugins       []string   `json:"pending_plugins,omitempty"`
	PreFilterMessage     string     `json:"pre_filter_message,omitempty"`
	PostFilterMessage    string     `json:"post_filter_message,omitempty"`
}

// PodSummary contains only the Pod fields useful for interpreting a failure.
type PodSummary struct {
	Namespace        string `json:"namespace,omitempty"`
	Name             string `json:"name,omitempty"`
	SchedulerName    string `json:"scheduler_name,omitempty"`
	SchedulingIntent string `json:"scheduling_intent,omitempty"`
}

// Reason is one scheduler rejection reason and the number of nodes it affected.
type Reason struct {
	Count      int    `json:"count"`
	Plugin     string `json:"plugin,omitempty"`
	StatusCode string `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

// Signals contains independent probabilities for broad failure families.
type Signals struct {
	CapacityShortfall    float64 `json:"capacity_shortfall"`
	ConstraintMismatch   float64 `json:"constraint_mismatch"`
	StorageOrDeviceBlock float64 `json:"storage_or_device_block"`
	TransientState       float64 `json:"transient_state"`
}

// Usage reports API token counts for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is the typed advisory diagnosis returned by TypeSafe.
type Result struct {
	Model         string                  `json:"model"`
	Remediation   Remediation             `json:"remediation"`
	Confidence    float64                 `json:"confidence"`
	Probabilities map[Remediation]float64 `json:"probabilities"`
	Signals       Signals                 `json:"signals"`
	Usage         Usage                   `json:"usage"`
}

// Question describes one typed judgment sent to TypeSafe. Question IDs are
// local API keys and are not used by the model during inference.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Contract describes the stable decision boundary around Jev.
type Contract struct {
	Model                           string              `json:"model"`
	MinimumRecommendationConfidence float64             `json:"minimum_recommendation_confidence"`
	Questions                       map[string]Question `json:"questions"`
}

// DecisionContract returns the model, questions, and confidence policy used by
// this client. It is safe to expose for inspection because it contains no
// credentials or runtime state.
func DecisionContract() Contract {
	return Contract{
		Model:                           defaultModel,
		MinimumRecommendationConfidence: DefaultRecommendationConfidence,
		Questions:                       diagnosisQuestions(),
	}
}

// Recommendation returns code-owned advice only for known, sufficiently
// confident results.
func (r Result) Recommendation(minConfidence float64) (string, bool) {
	if minConfidence < 0 || minConfidence > 1 {
		return "", false
	}
	if r.Remediation == RemediationUnknown || r.Confidence < minConfidence {
		return "", false
	}
	recommendation, ok := recommendationText[r.Remediation]
	return recommendation, ok
}

// Client evaluates scheduler failures with the TypeSafe System One API.
type Client struct {
	apiKey      string
	endpoint    string
	model       string
	httpClient  *http.Client
	maxAttempts int
	baseBackoff time.Duration
	wait        func(context.Context, time.Duration) error
}

// NewClient creates a client for the public TypeSafe endpoint.
func NewClient(apiKey string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("TypeSafe API key is empty")
	}
	return &Client{
		apiKey:      apiKey,
		endpoint:    defaultEndpoint,
		model:       defaultModel,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		maxAttempts: defaultMaxAttempts,
		baseBackoff: defaultBackoff,
		wait:        waitFor,
	}, nil
}

// Diagnose submits one state with independent failure-family questions and a
// closed-set remediation choice. It has no scheduler side effects.
func (c *Client) Diagnose(ctx context.Context, failure Failure) (Result, error) {
	body, err := json.Marshal(systemOneRequest{
		State:     normalizeFailure(failure),
		Model:     c.model,
		Questions: diagnosisQuestions(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("encode TypeSafe diagnosis request: %w", err)
	}

	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		result, retry, err := c.attempt(ctx, body)
		if err == nil || !retry || attempt == c.maxAttempts-1 {
			return result, err
		}
		backoff := c.baseBackoff * time.Duration(1<<attempt)
		if err := c.wait(ctx, backoff); err != nil {
			return Result{}, fmt.Errorf("wait to retry TypeSafe diagnosis: %w", err)
		}
	}
	return Result{}, fmt.Errorf("TypeSafe diagnosis exhausted retries")
}

func (c *Client) attempt(ctx context.Context, body []byte) (Result, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, false, fmt.Errorf("create TypeSafe diagnosis request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Result{}, false, fmt.Errorf("send TypeSafe diagnosis request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusOK {
		var decoded systemOneResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded); err != nil {
			return Result{}, false, fmt.Errorf("decode TypeSafe diagnosis response: %w", err)
		}
		result, err := decodeResult(decoded)
		return result, false, err
	}

	errorBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if readErr != nil {
		return Result{}, false, fmt.Errorf("TypeSafe diagnosis failed with status %s and unreadable response: %w", response.Status, readErr)
	}
	retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode == 529
	return Result{}, retry, fmt.Errorf("TypeSafe diagnosis failed with status %s: %s", response.Status, strings.TrimSpace(string(errorBody)))
}

func waitFor(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

type systemOneRequest struct {
	State     Failure             `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   Usage                      `json:"usage"`
}

type noulAnswer struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
}

type choiceAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func diagnosisQuestions() map[string]Question {
	return map[string]Question{
		"capacity_shortfall": {
			Type:         "noul",
			Instructions: "Do `reasons`, `pre_filter_message`, and `post_filter_message` show that the Pod's requested compute resources exceed currently available suitable node capacity?",
			Criteria: map[string]string{
				"true":  "Insufficient CPU, memory, ephemeral storage, scalar resources, or similarly exhausted node capacity is a primary blocker.",
				"false": "The evidence does not show an available-capacity shortage as a primary blocker.",
			},
		},
		"constraint_mismatch": {
			Type:         "noul",
			Instructions: "Do `reasons`, `pre_filter_message`, and `post_filter_message` show that hard placement constraints declared by the Pod or workload reject otherwise usable nodes? Do not count constraints originating from storage, PersistentVolumes, ResourceClaims, DRA, or devices.",
			Criteria: map[string]string{
				"true":  "Pod affinity, node selectors, taints or tolerations, host ports, requested node names, or Pod topology constraints are a primary blocker.",
				"false": "The evidence does not show Pod- or workload-owned hard placement constraints as a primary blocker, including when a similar mismatch originates from storage or device objects.",
			},
		},
		"storage_or_device_block": {
			Type:         "noul",
			Instructions: "Do `reasons`, `pre_filter_message`, and `post_filter_message` show that storage or device allocation is a primary blocker?",
			Criteria: map[string]string{
				"true":  "PVCs, PV topology, volume limits, ResourceClaims, DRA, or unavailable devices are a primary blocker.",
				"false": "The evidence does not show storage or device allocation as a primary blocker.",
			},
		},
		"transient_state": {
			Type:         "noul",
			Instructions: "Do `reasons`, `pre_filter_message`, and `post_filter_message` primarily describe a temporary state that may resolve without changing the workload?",
			Criteria: map[string]string{
				"true":  "The Pod is waiting for scheduling gates, gang quorum, preemption, controller reconciliation, or another explicitly temporary lifecycle state.",
				"false": "The evidence describes a persistent configuration, capacity, storage, device, extension, or unknown blocker.",
			},
		},
		"remediation": {
			Type:         "choice",
			Instructions: "Which single advisory next step best matches the complete scheduler failure evidence in `reasons`, the plugin lists, and the pre-filter and post-filter messages? Choose unknown when evidence is insufficient or genuinely split across unrelated causes.",
			Criteria:     remediationCriteria,
		},
	}
}

func normalizeFailure(failure Failure) Failure {
	failure.Reasons = append([]Reason(nil), failure.Reasons...)
	sort.Slice(failure.Reasons, func(i, j int) bool {
		if failure.Reasons[i].Plugin != failure.Reasons[j].Plugin {
			return failure.Reasons[i].Plugin < failure.Reasons[j].Plugin
		}
		if failure.Reasons[i].Message != failure.Reasons[j].Message {
			return failure.Reasons[i].Message < failure.Reasons[j].Message
		}
		return failure.Reasons[i].Count < failure.Reasons[j].Count
	})
	failure.UnschedulablePlugins = append([]string(nil), failure.UnschedulablePlugins...)
	failure.PendingPlugins = append([]string(nil), failure.PendingPlugins...)
	sort.Strings(failure.UnschedulablePlugins)
	sort.Strings(failure.PendingPlugins)
	return failure
}

func decodeResult(response systemOneResponse) (Result, error) {
	capacity, err := decodeNoul(response.Answers, "capacity_shortfall")
	if err != nil {
		return Result{}, err
	}
	constraint, err := decodeNoul(response.Answers, "constraint_mismatch")
	if err != nil {
		return Result{}, err
	}
	storage, err := decodeNoul(response.Answers, "storage_or_device_block")
	if err != nil {
		return Result{}, err
	}
	transient, err := decodeNoul(response.Answers, "transient_state")
	if err != nil {
		return Result{}, err
	}

	raw, ok := response.Answers["remediation"]
	if !ok {
		return Result{}, fmt.Errorf("TypeSafe response is missing answer %q", "remediation")
	}
	var answer choiceAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Result{}, fmt.Errorf("decode TypeSafe answer %q: %w", "remediation", err)
	}
	if answer.Type != "choice" {
		return Result{}, fmt.Errorf("TypeSafe answer %q has type %q, want %q", "remediation", answer.Type, "choice")
	}
	if _, ok := remediationCriteria[answer.Choice]; !ok {
		return Result{}, fmt.Errorf("TypeSafe answer %q selected unknown option %q", "remediation", answer.Choice)
	}
	if err := validateProbability("remediation confidence", answer.Confidence); err != nil {
		return Result{}, err
	}

	probabilities := make(map[Remediation]float64, len(answer.Probabilities))
	for option, probability := range answer.Probabilities {
		if _, ok := remediationCriteria[option]; !ok {
			return Result{}, fmt.Errorf("TypeSafe answer %q returned unknown probability option %q", "remediation", option)
		}
		if err := validateProbability("remediation probability for "+option, probability); err != nil {
			return Result{}, err
		}
		probabilities[Remediation(option)] = probability
	}

	return Result{
		Model:         response.Model,
		Remediation:   Remediation(answer.Choice),
		Confidence:    answer.Confidence,
		Probabilities: probabilities,
		Signals: Signals{
			CapacityShortfall:    capacity,
			ConstraintMismatch:   constraint,
			StorageOrDeviceBlock: storage,
			TransientState:       transient,
		},
		Usage: response.Usage,
	}, nil
}

func decodeNoul(answers map[string]json.RawMessage, id string) (float64, error) {
	raw, ok := answers[id]
	if !ok {
		return 0, fmt.Errorf("TypeSafe response is missing answer %q", id)
	}
	var answer noulAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return 0, fmt.Errorf("decode TypeSafe answer %q: %w", id, err)
	}
	if answer.Type != "noul" {
		return 0, fmt.Errorf("TypeSafe answer %q has type %q, want %q", id, answer.Type, "noul")
	}
	if err := validateProbability(id, answer.Noul); err != nil {
		return 0, err
	}
	return answer.Noul, nil
}

func validateProbability(name string, value float64) error {
	if value < 0 || value > 1 {
		return fmt.Errorf("TypeSafe %s is %v, want a value from 0 to 1", name, value)
	}
	return nil
}
