# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.0-bookworm AS build
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY pkg ./pkg
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" \
    -o /out/typesafe-scheduler ./cmd/typesafe-scheduler

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/typesafe-scheduler /usr/local/bin/typesafe-scheduler
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/typesafe-scheduler"]
