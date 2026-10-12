FROM ops.local/amd64-prep/python:3.12.14
COPY compiler-debs /compiler-debs
RUN dpkg -i /compiler-debs/*.deb && rm -rf /compiler-debs
COPY setuptools-84.0.0-py3-none-any.whl wheel-0.45.1-py3-none-any.whl packaging-26.3-py3-none-any.whl /build-tools/
RUN python -m pip install --no-index --no-deps --find-links=/build-tools setuptools==84.0.0 wheel==0.45.1 packaging==26.3
COPY cffi-2.1.1.tar.gz /source/cffi-2.1.1.tar.gz
ENV SOURCE_DATE_EPOCH=0
RUN test "$(gcc -dumpfullversion)" = "14.2.0" && test "$(dpkg-query -W -f='${source:Package} ${source:Version}' libffi-dev)" = "libffi 3.4.8-2" && python -m pip wheel --no-index --no-deps --no-build-isolation --wheel-dir=/output /source/cffi-2.1.1.tar.gz
