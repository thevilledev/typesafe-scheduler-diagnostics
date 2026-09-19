# Architecture

[Project overview](../README.md) · [Tradeoffs](tradeoffs.md) · [Development](development.md)

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

Jev supplies four independent failure-family probabilities and one closed-set
remediation choice. Go owns the recommendation text and the 0.75 confidence
gate. Unknown or low-confidence choices produce no advice Event. The model
does not choose nodes, bind Pods, enforce admission policy, or fix workloads.

The web server is embedded in the proof-of-concept scheduler binary and binds
to `127.0.0.1:8080`. It is not published through a Service or ingress. The
scenario endpoint can create at most 25 Pods per request and the scheduler
ServiceAccount gets additional scenario-creation permissions only in
`typesafe-demo`. The normal scheduler roles retain their existing permissions.

## Data boundary

Only Pod identity, scheduler name, an optional bounded intent annotation, node
count, plugin names, status codes, and bounded rejection messages are sent for
diagnosis. Complete Pod or Node objects, expected validation labels,
environment values, and Secrets are never included.

The API key stays server-side. Browser endpoints expose the fixed decision
contract and bounded traces but never configuration or credentials.

## Limitations

This is deliberately a proof of concept. It keeps the TypeSafe client,
in-memory trace store, and loopback web server in the secondary scheduler
process to make the full data flow easy to run and inspect.

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

The scheduler framework is compiled into kube-scheduler, so this project must
track Kubernetes minor versions. Do not mix the binary with a different
cluster minor without testing that combination. The current build and kind
node image are pinned to Kubernetes 1.36.4.

## Production path

A production implementation should move API access, credentials, persistence,
and the web API into a separate diagnosis controller. The scheduler plugin
would publish only bounded structured evidence to that controller. The web API
would then add authentication, authorization, durable retention, tenancy, rate
limits, and audit logging. Scenario generation should remain a development-only
capability and should not ship in an operator-facing production deployment.
