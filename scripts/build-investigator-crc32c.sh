#!/bin/sh
# Exact unmodified publisher Python source plus locked native CRC submodule.
# Run in the recorded compiler image with --network=none and an empty /build.
set -eu
export PIP_NO_INDEX=1 SOURCE_DATE_EPOCH=0
python -m pip install --no-index --no-deps --find-links /material/base/build-tools setuptools==84.0.0 wheel==0.45.1
tar xf /material/native/crc32c-02e65f4fd3065d27b2e29324800ca6d04df16126.tar.gz -C /build
cmake -S /build/crc32c-02e65f4fd3065d27b2e29324800ca6d04df16126 -B /build/native-build -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/build/native -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DCRC32C_BUILD_TESTS=OFF -DCRC32C_BUILD_BENCHMARKS=OFF -DCRC32C_USE_GLOG=OFF
cmake --build /build/native-build --parallel 2
cmake --install /build/native-build
source=$(find /material/sources -maxdepth 1 -name 'google-crc32c-1.9.0-*.tar.gz')
[ -n "$source" ]
tar xf "$source" -C /build
export CRC32C_INSTALL_PREFIX=/build/native CRC32C_PURE_PYTHON=0
# The locked native library is C++; link its static archive with the C++
# driver so its runtime symbols resolve without changing publisher sources.
export LDSHARED='g++ -shared'
cd /build/google_crc32c-1.9.0
python -m pip wheel --no-build-isolation --no-deps --wheel-dir /build/wheels .
python -m pip install --no-index --no-deps /build/wheels/google_crc32c-1.9.0-*.whl
python -c 'import google_crc32c; assert google_crc32c.implementation=="c"; assert google_crc32c.value(b"123456789")==0xe3069283'
