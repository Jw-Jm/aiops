FROM docker.io/library/golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Prepared from the exact licensed source closure by the SP05 material entry.
# No mutable downloaded CLI or caller-selected digest is admitted.
RUN echo '6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53  build/qualified/k8sgpt' | sha256sum -c -
ARG TARGETOS=linux
ARG TARGETARCH=arm64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/ops-worker ./cmd/platform-worker

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build --chown=65532:65532 /out/ops-worker /ops-worker
COPY --from=build --chown=65532:65532 /src/build/qualified/k8sgpt /opt/ops/bin/k8sgpt
COPY --from=build /src/build/qualified/runtime-notices /usr/share/ops/legal
USER 65532:65532
ENTRYPOINT ["/ops-worker"]
