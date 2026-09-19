# TypeSafe Scheduler Diagnostics

TypeSafe Scheduler Diagnostics is an experimental, advisory Kubernetes
scheduler extension. It turns structured scheduler rejection evidence into a
typed likely cause and a code-owned recommendation. It does not choose nodes,
bind Pods, or otherwise change scheduling decisions.

The project deliberately consumes scheduler framework status objects rather
than parsing the human-readable `FailedScheduling` event message.

## Status

The repository currently contains the portable TypeSafe diagnosis core. The
out-of-tree scheduler plugin and kind demonstration are built in subsequent
commits.

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
