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

package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/logs/json/register"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"
	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/dashboard"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/observation"
	"github.com/typesafe-ai/typesafe-scheduler-diagnostics/pkg/plugin"
)

func main() {
	observations := observation.NewMemory(500)
	dashboardAddress := environmentOrDefault("TYPESAFE_DASHBOARD_ADDR", dashboard.DefaultAddress)
	dashboardNamespace := environmentOrDefault("TYPESAFE_DEMO_NAMESPACE", dashboard.DefaultNamespace)
	var startDashboard sync.Once
	var dashboardErr error

	command := app.NewSchedulerCommand(
		app.WithPlugin(plugin.Name, func(ctx context.Context, configuration runtime.Object, handle fwk.Handle) (fwk.Plugin, error) {
			schedulerPlugin, err := plugin.NewWithRecorder(ctx, configuration, handle, observations)
			if err != nil {
				return nil, err
			}
			startDashboard.Do(func() {
				dashboardErr = dashboard.Start(ctx, dashboardAddress, handle.ClientSet(), observations, dashboardNamespace)
			})
			if dashboardErr != nil {
				return nil, fmt.Errorf("start Jev scheduling lab: %w", dashboardErr)
			}
			return schedulerPlugin, nil
		}),
	)
	os.Exit(cli.Run(command))
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
