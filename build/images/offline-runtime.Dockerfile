# A locally compiled CGO_ENABLED=0 Go binary; no base packages or image pulls.
# Preparation compiles with the repository-locked Go version and cached modules.
FROM scratch
COPY --chown=65532:65532 --chmod=0555 ops-process /ops-process
USER 65532:65532
ENTRYPOINT ["/ops-process"]
