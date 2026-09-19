#!/usr/bin/env bash

# Copyright 2026 The TypeSafe Scheduler Diagnostics Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

namespace="${DEMO_NAMESPACE:-typesafe-demo}"
timeout_seconds="${DEMO_TIMEOUT_SECONDS:-120}"
scenarios="insufficient-cpu:scale_cluster impossible-affinity:fix_workload_constraints impossible-storage:fix_storage_or_devices"
deadline=$((SECONDS + timeout_seconds))

for scenario in ${scenarios}; do
  pod="${scenario%%:*}"
  expected_remediation="${scenario#*:}"
  while true; do
    message="$(kubectl --namespace "${namespace}" get events \
      --field-selector "involvedObject.name=${pod},reason=TypeSafeDiagnosis" \
      --output jsonpath='{.items[0].message}' 2>/dev/null)"
    if [[ "${message}" == *"Remediation: ${expected_remediation};"* ]]; then
      break
    fi
    if [[ -n "${message}" ]]; then
      echo "unexpected diagnosis for Pod ${namespace}/${pod}: ${message}" >&2
      exit 1
    fi
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for a TypeSafeDiagnosis event for Pod ${namespace}/${pod}" >&2
      kubectl --namespace "${namespace}" get events --sort-by=.lastTimestamp >&2
      exit 1
    fi
    sleep 2
  done
done

kubectl --namespace "${namespace}" get pods
kubectl --namespace "${namespace}" get events \
  --field-selector reason=TypeSafeDiagnosis \
  --sort-by=.metadata.creationTimestamp
