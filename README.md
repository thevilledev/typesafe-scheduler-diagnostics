# TypeSafe Scheduler Diagnostics

Explain why an accepted Pod cannot be scheduled, and where to investigate
next. TypeSafe Scheduler Diagnostics is an experimental, advisory Kubernetes
scheduler extension. It turns scheduler rejection evidence into a typed likely
cause and a code-owned recommendation. The diagnosis plugin does not choose
nodes, bind Pods, enforce admission policy, or fix workloads.

The included **Jev Scheduling Lab** lets you generate real Pending Pods in a
local kind cluster, watch Jev classify each failure, inspect every typed signal
and probability, and validate the result against a label that Jev never sees.

## Why would anyone use this?

A platform team could use this to help application owners answer "why is my
Pod still Pending?" without first handing the problem to a scheduler expert.
The intended benefit is less investigation and fewer wrong-team handoffs,
not faster scheduling or extra cluster capacity.

- **Find the next useful check.** Classify the observed failure as capacity,
  workload constraints, storage/devices, a transient state, or an extension
  problem. Publish a specific investigation hint on the Pod as an Event.
- **Keep the evidence attached.** Inspect the responsible plugin, status,
  rejection messages, and affected-node counts alongside the recommendation.
  This is evidence from an actual scheduling attempt, not just a manifest lint.
- **Make advice inspectable.** See probabilities, withheld advice, errors,
  latency, and token use. Generated scenarios have expected labels hidden from
  Jev so you can check agreement instead of trusting a plausible explanation.

For example, the storage demo is accepted by the API but cannot be placed
because its persistent volume requires a nonexistent node. The rejection
mentions *node affinity*, but comes from `VolumeBinding`. The useful next step
is to inspect PVC/PV topology, not edit the Pod's selector. That distinction
can send an operator to the right runbook or team. The lab emits the advice;
ticket routing and runbook integrations are possible consumers, not features
implemented here.

## Why not an admission controller?

For explaining a failed placement, this plugin has the better observation
point. For preventing forbidden configurations, admission control is the
right tool. They solve different problems and can be used together.

| Comparison | Admission control | This diagnosis plugin |
| --- | --- | --- |
| Question | May this API change be accepted? | Why did this placement attempt fail? |
| Timing | Before the API change is stored | After admission, when scheduling finds no feasible node |
| Evidence | Submitted object and any additional state a webhook queries | Scheduler plugin statuses from the failed attempt |
| Effect | Reject a request or, with mutation, change it | Emit optional advice; no placement or policy changes |
| Installation | Admission policy or webhook; no custom scheduler required | Custom secondary scheduler; Pods opt in with `schedulerName` |

See the Kubernetes documentation for [admission control](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/)
and the [PostFilter extension point](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/#postfilter).

A webhook can query Nodes, volumes, and other cluster state. It does not,
however, receive the results of the Pod's later scheduling attempt. Available
capacity and placement constraints can change after admission; accepting a
Pod is not a promise that it can run immediately. Keep admission checks for
requirements such as approved images, required labels, and security policy.
Do not replace deterministic policy enforcement with model confidence.

Putting inference inside an admission webhook would also put a network wait
on the API request path. Here, inference runs asynchronously after the failed
attempt: a runtime API error withholds advice, and a full queue drops optional
work. Kubernetes documents why [admission webhook latency matters](https://kubernetes.io/docs/concepts/cluster-administration/admission-webhooks-good-practices/#performance-and-latency).
This is not zero overhead or failure isolation: evidence capture still runs
in the scheduling cycle, and the worker shares the scheduler process.

If easy installation and broad workload coverage matter more than native
scheduler evidence, a separate controller watching Pods and Events is a
simpler starting point. It can also call Jev, but would work from API-visible
conditions and Events instead of this plugin's structured per-node statuses.
The custom scheduler is a tradeoff, not a requirement for AI-assisted triage.

## What does Jev add over ordinary rules?

Capturing structured scheduler evidence is useful without AI. The separate
hypothesis is that Jev can interpret unfamiliar wording and combinations of
plugin evidence without an expanding collection of message-matching rules.
The statuses are structured, but their reason text still needs interpretation.

Jev selects from a closed set; Go owns the advice text, the 0.75 publication
threshold, deduplication, retries, and data bounds. An `unknown` or
low-confidence result produces no advice Event. Confidence is not a guarantee
of correctness, and `scale_cluster` means inspect capacity and autoscaling,
not automatically buy nodes.

For the three fixed demo cases, rules based on plugin names and known messages
are simpler and cheaper. These scenarios prove the integration, not that Jev
beats rules or reduces incident resolution time. Before adopting it, compare
both approaches on representative labelled failures: wrong recommendations,
withheld advice, operator time, latency, and API cost.

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

The live SVG pipeline shows captured evidence, queued requests, active Jev
calls, and advice emitted or withheld. Updates stream over Server-Sent Events,
including after reconnection. Animation marks observed transitions; it does
not simulate model activity.

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
TypeSafeDiagnosis PostFilter plugin -- returns Unschedulable; no network wait
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
drops optional work without waiting for queue capacity.

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

Current limits matter when deciding whether to use it:

- Only Pods assigned to `typesafe-scheduler`, on failure paths that reach this
  PostFilter plugin, produce evidence. It is not cluster-wide Pending-Pod
  coverage and does not diagnose admission rejections or container startup
  failures such as image pulls.
- Evidence is partial and time-bound. A rejecting Filter plugin stops later
  filters for that node, so the captured statuses need not list every blocker.
  Advice can arrive after cluster state has changed; recheck before acting.
- Runtime inference errors leave scheduling decisions alone, but the lab is
  not operationally isolated: the worker and UI share scheduler resources,
  and a missing API key prevents this scheduler from starting.
- Diagnosis sends bounded evidence to an external API, incurs API costs, and
  can be wrong. Review the [data boundary](#data-boundary) and evaluate your
  own failures.

A production implementation should move API access, credentials, persistence,
and the web API into a separate diagnosis controller. The scheduler plugin
would publish only bounded structured evidence to that controller. The web API
would then add authentication, authorization, durable retention, tenancy, rate
limits, and audit logging. Scenario generation should remain a development-only
capability and should not ship in an operator-facing production deployment.

The scheduler framework is compiled into kube-scheduler, so this project must
track Kubernetes minor versions. Do not mix the binary with a different
cluster minor without testing that combination.
