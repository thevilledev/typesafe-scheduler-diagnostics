# TypeSafe Scheduler Diagnostics

TypeSafe Scheduler Diagnostics is an experimental, advisory Kubernetes
scheduler extension. It turns structured scheduler rejection evidence into a
typed likely cause and a code-owned recommendation. It does not choose nodes,
bind Pods, or otherwise change scheduling decisions.

The project deliberately consumes scheduler framework status objects rather
than parsing the human-readable `FailedScheduling` event message.

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
TypeSafe System One -- typed signals and closed-set remediation
          |
          v
confidence gate -- Kubernetes Event with code-owned advice
```

The informational `PostFilter` plugin runs before `DefaultPreemption`. It
copies the scheduler's structured node statuses, queues diagnosis without
waiting for the network, and returns `Unschedulable` so preemption continues
normally. Duplicate failures are suppressed for five minutes and a full queue
drops optional work rather than delaying scheduling.

Only Pod identity, scheduler name, an optional bounded intent annotation, node
count, plugin names, status codes, and bounded rejection messages are sent for
diagnosis. Complete Pod or Node objects, environment values, and Secrets are
never included.

## Run the kind demo

Prerequisites are Docker, kind, kubectl, and a TypeSafe environment file
containing `TYPESAFE_API_KEY=...`. The build is pinned to Kubernetes 1.36.4 and
uses the matching kind node image.

Run everything:

```sh
make kind-demo TYPESAFE_ENV=/path/to/typesafe-env
```

Or inspect each step:

```sh
make kind-up
make image
make kind-load
make secret TYPESAFE_ENV=/path/to/typesafe-env
make deploy
make demo
make verify-demo
```

The demo creates three intentionally Pending Pods using
`spec.schedulerName: typesafe-scheduler`:

| Pod | Structured failure | Expected remediation |
| --- | --- | --- |
| `insufficient-cpu` | `NodeResourcesFit: Insufficient cpu` | `scale_cluster` |
| `impossible-affinity` | `NodeAffinity` selector mismatch | `fix_workload_constraints` |
| `impossible-storage` | `VolumeBinding` PV affinity mismatch | `fix_storage_or_devices` |

Inspect the user-facing diagnoses directly:

```sh
kubectl --namespace typesafe-demo get events \
  --field-selector reason=TypeSafeDiagnosis \
  --sort-by=.metadata.creationTimestamp
```

Remove the local cluster with `make kind-down`.

## Development

```sh
go test ./...
```

Live tests are opt-in and use the local ignored `typesafe-env` file:

```sh
source ./typesafe-env
export TYPESAFE_API_KEY
TYPESAFE_LIVE_TEST=1 go test ./pkg/diagnosis -run TestLive -v
```

## Current boundary

This proof of concept keeps the TypeSafe client in the secondary scheduler
process, but all calls are asynchronous and advisory. A production-oriented
follow-up should move API access and the credential into a separate diagnosis
controller, with the scheduler plugin publishing bounded structured evidence.

The scheduler framework is compiled into kube-scheduler, so this project must
track Kubernetes minor versions. Do not mix the binary with a different
cluster minor without testing that combination.
