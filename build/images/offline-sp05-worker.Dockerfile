FROM scratch
COPY --chown=65532:65532 --chmod=0555 ops-process /ops-process
COPY --chown=65532:65532 --chmod=0555 k8sgpt /opt/ops/bin/k8sgpt
COPY runtime-notices /usr/share/ops/legal
USER 65532:65532
ENTRYPOINT ["/ops-process"]
