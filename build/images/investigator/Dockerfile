# Inputs are authenticated by scripts/prepare-investigator-material.py.
# Build with artifacts/sp06-investigator as context, --network=none --pull=false.
FROM docker.io/library/python:3.12.14-slim@sha256:f77ac9e44ae96ef2c90b8053ea08c31f8be030f824196b0ae4db6d462c84e51f
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PIP_NO_INDEX=1 LITELLM_LOCAL_MODEL_COST_MAP=True PYTHONPATH=/app/src
WORKDIR /app
COPY wheels /material/wheels
COPY offline-requirements.txt /material/offline-requirements.txt
RUN python -m pip install --no-index --no-deps --require-hashes --find-links=/material/wheels -r /material/offline-requirements.txt && python -m pip check
COPY wrapper/src /app/src
COPY notices /usr/share/ops/legal/python
COPY base/notices /usr/share/ops/legal/base
COPY base/source.lock.json base/python-source.lock.json /usr/share/ops/legal/base/
COPY wrapper/LICENSE wrapper/NOTICE /usr/share/ops/legal/wrapper/
COPY runtime.lock.json sbom.cdx.json /usr/share/ops/legal/
COPY python-runtime-source.tar /usr/share/ops/legal/python-runtime-source.tar
RUN python -c 'from holmes.core.tool_calling_llm import ToolCallingLLM; from holmes.core.llm import DefaultLLM; from mcp.client.streamable_http import streamable_http_client; import investigator.worker' && rm -rf /material
USER 65532:65532
ENTRYPOINT ["python", "-m", "investigator.worker"]
