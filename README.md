# TypeSafe Scheduler Diagnostics

TypeSafe Scheduler Diagnostics is an experimental, advisory Kubernetes
scheduler extension. It turns structured scheduler rejection evidence into a
typed likely cause and a code-owned recommendation. It does not choose nodes,
bind Pods, or otherwise change scheduling decisions.

The included **Jev Scheduling Lab** lets you generate real Pending Pods in a
local kind cluster, watch Jev classify each failure, inspect every typed signal
and probability, and validate the result against a label that Jev never sees.
The monochrome UI includes a live SVG pipeline driven by scheduler lifecycle
updates: captured evidence, queued requests, active Jev calls, and advice
emitted or withheld. Updates stream over Server-Sent Events, including after
reconnection. The animation marks observed transitions; it does not simulate
model activity.

## Why would anyone use this?

A Pending Pod often leaves two groups doing expensive translation work:
application owners see a scheduler message they do not understand, while
platform engineers reconstruct the effective constraint from several plugins,
nodes, volumes, and policies. Static runbooks help with familiar failures but
become a brittle parser once messages vary across plugins or Kubernetes
versions.

This project demonstrates a different boundary:

- **Shorten time to explanation.** Turn many per-node rejection statuses into
  one likely failure family and a specific next place to investigate.
- **Avoid a growing parser.** Consume scheduler framework status objects
  directly. Jev interprets bounded semantic evidence instead of code matching
  human-oriented `FailedScheduling` prose.
- **Keep the scheduler deterministic.** Diagnosis is asynchronous and
  advisory. A slow, unavailable, or wrong model cannot select a node or delay
  the scheduling cycle.
- **Make uncertainty visible.** Operators see the selected remediation, its
  full Choice distribution, four independent Noul probabilities, and the
  confidence threshold that decides whether advice becomes a Kubernetes
  Event.
- **Measure before trusting.** Generated scenarios carry expected labels that
  are withheld from Jev. The UI reports pass/fail results, latency, and token
  use instead of presenting a polished anecdote as evaluation.
- **Own policy in code.** Jev chooses from a closed set. Go code owns the
  recommendation text, the 0.75 publication gate, deduplication, retry policy,
  data bounds, and every cluster-side action.

For a platform team, the practical product could be a diagnosis feed attached
to Pending workloads, an SRE triage queue, or a signal used to route a case to
the right runbook. It is not an autonomous remediation system.

For three fixed, familiar error strings, ordinary rules are simpler and
cheaper. The hypothesis worth testing is that semantic judgment helps with
unfamiliar wording and combinations of plugin evidence. These demo scenarios
prove the integration, not production accuracy or a reduction in incident
resolution time. Evaluate on representative labelled failures before relying
on the recommendations.

## Try the web lab

Prerequisites are Docker, kind, kubectl, and a TypeSafe environment file
containing `TYPESAFE_API_KEY=...`. The build is pinned to Kubernetes 1.36.4 and
uses the matching kind node image.

Create the cluster, build and load the scheduler, install the API key, and run
three labelled baseline scenarios:

```sh
make kind-demo TYPESAFE_ENV=/path/to/typesafe-env
```

In another terminal, forward the dashboard's loopback-only port:

```sh
make ui
```

Open [http://localhost:8080](http://localhost:8080). Choose a capacity,
constraint, storage, or mixed scenario; set the number of failure events from
1 to 25; then launch the experiment.

Each requested event is a real Pod using
`spec.schedulerName: typesafe-scheduler`. The UI shows:

- the exact bounded scheduler state sent to Jev;
- all four independent Noul probabilities;
- the selected remediation and full Choice probability distribution;
- model, latency, and token counts;
- whether the selection matched the hidden expected remediation; and
- whether confidence was high enough to publish advice as a Kubernetes Event.

The expected annotation is copied into the local evaluation record only. It is
not part of `diagnosis.Failure`, so it cannot leak the answer into model state.
Dashboard history is bounded to 500 traces and remains in memory. Queue,
evaluation, and completion updates share one trace ID. Pipeline counts and
metrics cover that retained history, not the lifetime of the cluster. Label
agreement counts completed labelled results; service errors are shown
separately and excluded from the agreement denominator.

Use **Clear generated run** in the UI to delete generated Pods and traces. The
three baseline objects from `config/demo.yaml` are left intact. Remove the
entire cluster with `make kind-down`.

## How it works

```text
Pod using typesafe-scheduler
          |
          v
kube-scheduler filter failure
          |
          v
TypeSafeDiagnosis PostFilter plugin -- immediately returns Unschedulable
          |
          v
bounded asynchronous queue
          |
          v
TypeSafe System One / Jev
  four Noul signals + one closed-set Choice
          |
          +----------------------+
          |                      |
          v                      v
confidence gate            in-memory trace
Kubernetes Event           web UI + validation
```

The informational `PostFilter` plugin runs before `DefaultPreemption`. It
copies the scheduler's structured node statuses, queues diagnosis without
waiting for the network, and returns `Unschedulable` so preemption continues
normally. Duplicate failures are suppressed for five minutes and a full queue
drops optional work rather than delaying scheduling.

The web server is embedded in the proof-of-concept scheduler binary and binds
to `127.0.0.1:8080`. It is not published through a Service or ingress. The
scenario endpoint can create at most 25 Pods per request and the scheduler
ServiceAccount gets additional scenario-creation permissions only in
`typesafe-demo`. The normal scheduler roles retain their existing permissions.

## Demo scenarios

The baseline manifest creates three intentionally Pending Pods:

| Pod | Structured failure | Expected remediation |
| --- | --- | --- |
| `insufficient-cpu` | `NodeResourcesFit: Insufficient cpu` | `scale_cluster` |
| `impossible-affinity` | `NodeAffinity` selector mismatch | `fix_workload_constraints` |
| `impossible-storage` | `VolumeBinding` PV affinity mismatch | `fix_storage_or_devices` |

Inspect the user-facing Kubernetes Events directly:

```sh
kubectl --namespace typesafe-demo get events \
  --field-selector reason=TypeSafeDiagnosis \
  --sort-by=.metadata.creationTimestamp
```

Run the cluster steps individually when debugging:

```sh
make kind-up
make image
make kind-load
make secret TYPESAFE_ENV=/path/to/typesafe-env
make deploy
make demo
make verify-demo
make ui
```

## Data boundary

Only Pod identity, scheduler name, an optional bounded intent annotation, node
count, plugin names, status codes, and bounded rejection messages are sent for
diagnosis. Complete Pod or Node objects, expected validation labels,
environment values, and Secrets are never included.

The API key stays server-side. Browser endpoints expose the fixed decision
contract and bounded traces but never configuration or credentials.

## Development

Run deterministic tests:

```sh
go test ./...
```

Live TypeSafe tests are opt-in:

```sh
source ./typesafe-env
export TYPESAFE_API_KEY
TYPESAFE_LIVE_TEST=1 go test ./pkg/diagnosis -run TestLive -v
```

## Current boundary and production path

This is deliberately a proof of concept. It keeps the TypeSafe client,
in-memory trace store, and loopback web server in the secondary scheduler
process to make the full data flow easy to run and inspect.

A production implementation should move API access, credentials, persistence,
and the web API into a separate diagnosis controller. The scheduler plugin
would publish only bounded structured evidence to that controller. The web API
would then add authentication, authorization, durable retention, tenancy, rate
limits, and audit logging. Scenario generation should remain a development-only
capability and should not ship in an operator-facing production deployment.

The scheduler framework is compiled into kube-scheduler, so this project must
track Kubernetes minor versions. Do not mix the binary with a different
cluster minor without testing that combination.
