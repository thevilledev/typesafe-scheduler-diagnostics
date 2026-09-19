# Development

[Project overview](../README.md) · [Architecture](architecture.md) · [Demo guide](demo.md)

Run commands from the repository root with the Go toolchain declared in
[go.mod](../go.mod).

## Tests

Run deterministic tests without a TypeSafe API key or cluster:

```sh
go test ./...
```

Live TypeSafe tests are opt-in and make external API calls:

```sh
source ./typesafe-env
export TYPESAFE_API_KEY
TYPESAFE_LIVE_TEST=1 go test ./pkg/diagnosis -run TestLive -v
```

Keep the environment file out of version control. For the real-cluster
workflow and baseline verification, see the [demo guide](demo.md).

## Build

```sh
make build
make image
```

`make build` writes the local binary to `bin/typesafe-scheduler`.
`make image` builds `typesafe-scheduler-diagnostics:dev` by default.

## Source layout

- [cmd/typesafe-scheduler](../cmd/typesafe-scheduler) — scheduler entry point.
- [pkg/plugin](../pkg/plugin) — PostFilter evidence capture and advisory worker.
- [pkg/diagnosis](../pkg/diagnosis) — typed TypeSafe client and code-owned advice.
- [pkg/observation](../pkg/observation) — bounded in-memory lifecycle history.
- [pkg/dashboard](../pkg/dashboard) — web UI, streaming API, and scenario generator.
- [config](../config) — kind, scheduler deployment, and baseline scenarios.

The scheduler binary must track the cluster's Kubernetes minor version. See
[limitations and the production path](architecture.md#limitations) before
extending this proof of concept for production use.
