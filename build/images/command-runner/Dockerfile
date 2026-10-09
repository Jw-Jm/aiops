FROM docker.io/library/python:3.12.14-slim@sha256:f77ac9e44ae96ef2c90b8053ea08c31f8be030f824196b0ae4db6d462c84e51f
# Installation inputs are authenticated and retained before this offline build.
COPY debian/packages/ /opt/material/debs/
RUN dpkg -i /opt/material/debs/*.deb && rm -rf /opt/material/debs
COPY wheels/ /opt/material/wheels/
COPY requirements.lock /opt/material/requirements.lock
RUN python3 -m pip install --no-index --find-links=/opt/material/wheels --require-hashes -r /opt/material/requirements.lock && rm -rf /opt/material
COPY kubectl /usr/local/bin/kubectl
COPY command-runner /ops-command-runner
COPY ansible_transport.py /opt/ops/ansible_transport.py
RUN chmod 0555 /ops-command-runner /usr/local/bin/kubectl /opt/ops/ansible_transport.py && printf 'opsrunner:x:65532:65532:SP07 one-shot runner:/tmp:/bin/bash\n' >> /etc/passwd && printf 'opsrunner:x:65532:\n' >> /etc/group
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 HOME=/tmp PATH=/usr/local/bin:/usr/bin:/bin
WORKDIR /tmp
USER 65532:65532
ENTRYPOINT ["/ops-command-runner"]
