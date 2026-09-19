# Tradeoffs and evaluation

[Project overview](../README.md) · [Architecture](architecture.md) · [Demo guide](demo.md)

## What benefit should users get?

The intended benefit is less investigation and fewer wrong-team handoffs,
not faster scheduling or extra cluster capacity. Application owners get a
specific next place to investigate, while platform engineers can inspect the
scheduler evidence behind it.

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

## Why not a controller watching Pods and Events?

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

The [demo guide](demo.md#validation-and-history) explains what the dashboard's
label agreement measures. The [architecture guide](architecture.md#limitations)
covers incomplete evidence and operational limits.
