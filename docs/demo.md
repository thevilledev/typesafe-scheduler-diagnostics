# Jev Scheduling Lab

[Project overview and quick start](../README.md#run-the-lab) · [Architecture](architecture.md) · [Evaluation](tradeoffs.md)

The lab generates real Pending Pods in a local kind cluster, streams their
diagnosis lifecycle, and lets you inspect Jev's typed output against labels
that the model never sees.

## Setup

Prerequisites are Docker, kind, kubectl, make, and a TypeSafe environment file
containing `TYPESAFE_API_KEY=...`. The build is pinned to Kubernetes 1.36.4 and
uses the matching kind node image.

Run these commands from the repository root:

```sh
make kind-up
kubectl config use-context kind-typesafe-diagnostics
make kind-demo TYPESAFE_ENV=/path/to/typesafe-env
make ui
```

The Make targets use the current kubectl context. Select the demo context
explicitly even if the cluster already exists; adjust its name if you override
`KIND_CLUSTER`. `make ui` keeps a port-forward running in the foreground.

`make kind-demo` builds and loads the scheduler, installs the server-side API
key, deploys the scheduler, and creates and verifies three labelled baseline
scenarios. Keep the environment file out of version control.

Open [localhost:8080](http://localhost:8080). Choose a capacity, constraint,
storage, or mixed scenario; set the number of failure events from 1 to 25;
then launch the experiment. Each requested event is a real Pod using
`spec.schedulerName: typesafe-scheduler`.

## What the dashboard shows

The live SVG pipeline shows captured evidence, queued requests, active Jev
calls, and advice emitted or withheld. Updates stream over Server-Sent Events,
including after reconnection. Animation marks observed transitions; it does
not simulate model activity.

Select an observation to inspect:

- the exact bounded scheduler state sent to Jev;
- all four independent Noul probabilities;
- the selected remediation and full Choice probability distribution;
- model, latency, and token counts;
- whether the selection matched the hidden expected remediation; and
- whether confidence was high enough to publish advice as a Kubernetes Event.

## Validation and history

The expected annotation is copied into the local evaluation record only. It is
not part of `diagnosis.Failure`, so it cannot leak the answer into model state.
Dashboard history is bounded to 500 traces and remains in memory. Queue,
evaluation, and completion updates share one trace ID. Pipeline counts and
metrics cover that retained history, not the lifetime of the cluster. Label
agreement counts completed labelled results; service errors are shown
separately and excluded from the agreement denominator.

These scenarios verify the integration, not production accuracy. See
[tradeoffs and evaluation](tradeoffs.md#what-does-jev-add-over-ordinary-rules)
before drawing conclusions about Jev versus rules.

## Baseline scenarios

The [baseline manifest](../config/demo.yaml) creates three intentionally
Pending Pods and the storage fixtures used by the lab:

| Pod | Structured failure | Expected remediation |
| --- | --- | --- |
| `insufficient-cpu` | `NodeResourcesFit: Insufficient cpu` | `scale_cluster` |
| `impossible-affinity` | `NodeAffinity` selector mismatch | `fix_workload_constraints` |
| `impossible-storage` | `VolumeBinding` PV affinity mismatch | `fix_storage_or_devices` |

Run `make demo` before generating storage or mixed scenarios if you deployed
the scheduler manually. Those scenarios need the baseline PVC/PV fixtures.

Inspect the user-facing Kubernetes Events directly:

```sh
kubectl --context kind-typesafe-diagnostics --namespace typesafe-demo get events \
  --field-selector reason=TypeSafeDiagnosis \
  --sort-by=.metadata.creationTimestamp
```

## Run individual steps

For debugging, run the workflow one step at a time:

```sh
make kind-up
kubectl config use-context kind-typesafe-diagnostics
make image
make kind-load
make secret TYPESAFE_ENV=/path/to/typesafe-env
make deploy
make demo
make verify-demo
make ui
```

## Cleanup

Use **Clear generated run** in the UI to delete generated Pods and their
matching traces. The baseline Pods and shared storage fixtures from
`config/demo.yaml` are left intact. Remove the entire demo cluster with:

```sh
make kind-down
```

## Recording

The [README animation](assets/jev-scheduling-lab.gif) is a 22.8-second,
1280 × 720 capture sampled at up to 10 fps. It shows nine generated Pods,
three of each scenario, on a real three-node kind cluster with live TypeSafe
responses. Pauses between capture segments were omitted; model activity and
outcomes were not simulated. Its label agreement is not an accuracy claim.
