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

// Package dashboard serves the local Jev scheduling lab and creates bounded
// demo workloads through the Kubernetes API.
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/diagnosis"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/observation"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/plugin"
)

const (
	// DefaultAddress allows access through kubectl port-forward only.
	DefaultAddress = "127.0.0.1:8080"

	// DefaultNamespace is isolated from ordinary application workloads.
	DefaultNamespace = "typesafe-demo"

	generatedLabel = "diagnostics.typesafe.ai/generated"
	scenarioLabel  = "diagnostics.typesafe.ai/scenario"
	maxBatchSize   = 25
)

//go:embed static/*
var staticFiles embed.FS

// Server exposes an inspectable diagnosis feed and a bounded scenario API.
type Server struct {
	client       kubernetes.Interface
	observations *observation.Memory
	namespace    string
	handler      http.Handler
}

type snapshotResponse struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Contract    diagnosis.Contract        `json:"contract"`
	Items       []observation.Observation `json:"items"`
}

type generateRequest struct {
	Scenario string `json:"scenario"`
	Count    int    `json:"count"`
}

type generateResponse struct {
	Created []string `json:"created"`
}

type errorResponse struct {
	Error   string   `json:"error"`
	Created []string `json:"created,omitempty"`
}

type scenario struct {
	name      string
	intent    string
	expected  diagnosis.Remediation
	configure func(*v1.Pod)
}

var scenarios = map[string]scenario{
	"capacity": {
		name:     "capacity",
		intent:   "Run a deliberately oversized batch worker.",
		expected: diagnosis.RemediationScaleCluster,
		configure: func(pod *v1.Pod) {
			pod.Spec.Containers[0].Resources.Requests = v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("100"),
			}
		},
	},
	"constraints": {
		name:     "constraints",
		intent:   "Run only in the fictional unavailable node pool.",
		expected: diagnosis.RemediationFixWorkloadConstraints,
		configure: func(pod *v1.Pod) {
			pod.Spec.NodeSelector = map[string]string{
				"diagnostics.typesafe.ai/pool": "unavailable",
			}
		},
	},
	"storage": {
		name:     "storage",
		intent:   "Mount a local volume whose node affinity cannot be satisfied.",
		expected: diagnosis.RemediationFixStorageOrDevices,
		configure: func(pod *v1.Pod) {
			pod.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{{
				Name:      "data",
				MountPath: "/data",
			}}
			pod.Spec.Volumes = []v1.Volume{{
				Name: "data",
				VolumeSource: v1.VolumeSource{PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: "impossible-storage",
				}},
			}}
		},
	},
}

// New creates a dashboard handler. The Kubernetes client is used only for
// explicitly requested demo workload creation and cleanup.
func New(client kubernetes.Interface, observations *observation.Memory, namespace string) (*Server, error) {
	if client == nil {
		return nil, fmt.Errorf("dashboard Kubernetes client is nil")
	}
	if observations == nil {
		return nil, fmt.Errorf("dashboard observation store is nil")
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, fmt.Errorf("dashboard namespace is empty")
	}

	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded dashboard assets: %w", err)
	}
	server := &Server{
		client:       client,
		observations: observations,
		namespace:    namespace,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/snapshot", server.snapshot)
	mux.HandleFunc("GET /api/v1/events", server.events)
	mux.HandleFunc("POST /api/v1/scenarios", server.generate)
	mux.HandleFunc("DELETE /api/v1/scenarios", server.clear)
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	server.handler = securityHeaders(http.NewCrossOriginProtection().Handler(mux))
	return server, nil
}

// Handler returns the dashboard's HTTP handler for tests and embedding.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Start binds the dashboard before returning and serves until ctx is done.
func Start(ctx context.Context, address string, client kubernetes.Interface, observations *observation.Memory, namespace string) error {
	server, err := New(client, observations, namespace)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen for dashboard on %s: %w", address, err)
	}
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			klog.FromContext(ctx).Error(err, "Dashboard shutdown failed")
		}
	}()
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			klog.FromContext(ctx).Error(err, "Dashboard server failed")
		}
	}()
	klog.FromContext(ctx).Info("Jev scheduling lab is listening", "address", listener.Addr().String(), "namespace", namespace)
	return nil
}

func (s *Server) snapshot(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, s.currentSnapshot())
}

func (s *Server) currentSnapshot() snapshotResponse {
	return snapshotResponse{
		GeneratedAt: time.Now().UTC(),
		Contract:    diagnosis.DecisionContract(),
		Items:       s.observations.List(500),
	}
}

// events streams real lifecycle updates. Each client receives a fresh snapshot
// after reconnecting, so missing a notification cannot leave counts stale.
func (s *Server) events(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "Streaming is unavailable", http.StatusInternalServerError)
		return
	}
	changed, unsubscribe := s.observations.Subscribe()
	defer unsubscribe()
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		payload, err := json.Marshal(s.currentSnapshot())
		if err != nil {
			return
		}
		// Bound writes so an abandoned tab does not retain a server goroutine.
		_ = http.NewResponseController(response).SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(response, "data: %s\n\n", payload); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-request.Context().Done():
			return
		case <-changed:
		case <-ticker.C:
		}
	}
}

func (s *Server) generate(response http.ResponseWriter, request *http.Request) {
	var input generateRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, errorResponse{Error: "decode scenario request: " + err.Error()})
		return
	}
	if input.Count < 1 || input.Count > maxBatchSize {
		writeJSON(response, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("count must be from 1 to %d", maxBatchSize)})
		return
	}
	if input.Scenario != "mixed" {
		if _, ok := scenarios[input.Scenario]; !ok {
			writeJSON(response, http.StatusBadRequest, errorResponse{Error: "scenario must be capacity, constraints, storage, or mixed"})
			return
		}
	}

	created := make([]string, 0, input.Count)
	for index := 0; index < input.Count; index++ {
		scenarioName := input.Scenario
		if scenarioName == "mixed" {
			scenarioName = []string{"capacity", "constraints", "storage"}[index%3]
		}
		pod := scenarioPod(s.namespace, scenarios[scenarioName])
		result, err := s.client.CoreV1().Pods(s.namespace).Create(request.Context(), pod, metav1.CreateOptions{})
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, errorResponse{
				Error:   fmt.Sprintf("create %s scenario Pod in namespace %s: %v", scenarioName, s.namespace, err),
				Created: created,
			})
			return
		}
		created = append(created, result.Name)
	}
	writeJSON(response, http.StatusCreated, generateResponse{Created: created})
}

func (s *Server) clear(response http.ResponseWriter, request *http.Request) {
	selector := generatedLabel + "=true"
	if err := s.client.CoreV1().Pods(s.namespace).DeleteCollection(
		request.Context(),
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: selector},
	); err != nil {
		writeJSON(response, http.StatusInternalServerError, errorResponse{
			Error: fmt.Sprintf("delete generated Pods in namespace %s: %v", s.namespace, err),
		})
		return
	}
	s.observations.Clear()
	response.WriteHeader(http.StatusNoContent)
}

func scenarioPod(namespace string, value scenario) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "jev-" + value.name + "-",
			Labels: map[string]string{
				generatedLabel: "true",
				scenarioLabel:  value.name,
			},
			Annotations: map[string]string{
				plugin.IntentAnnotation:              value.intent,
				plugin.ExpectedRemediationAnnotation: string(value.expected),
			},
		},
		Spec: v1.PodSpec{
			SchedulerName: "typesafe-scheduler",
			RestartPolicy: v1.RestartPolicyNever,
			Tolerations: []v1.Toleration{{
				Key:      "node-role.kubernetes.io/control-plane",
				Operator: v1.TolerationOpExists,
				Effect:   v1.TaintEffectNoSchedule,
			}},
			Containers: []v1.Container{{
				Name:  "pause",
				Image: "registry.k8s.io/pause:3.10",
			}},
		},
	}
	value.configure(pod)
	return pod
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(response, request)
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		klog.ErrorS(err, "Encode dashboard response")
	}
}
