#!/bin/sh
# Run inside the recorded Linux/arm64 compiler environment, --network=none.
# /material: authenticated corresponding-source material; /build: empty writable dir.
set -eu
export PIP_NO_INDEX=1 CARGO_NET_OFFLINE=true CARGO_BUILD_JOBS=2
python -m pip install --no-index --no-deps --find-links /material/base/build-tools setuptools==84.0.0 wheel==0.45.1 setuptools-rust==1.11.1 semantic-version==2.10.0
for file in /material/rust/toolchain/rustc-*.tar.xz /material/rust/toolchain/cargo-*.tar.xz /material/rust/toolchain/rust-std-*.tar.xz; do
 tar xf "$file" -C /build
 directory=$(basename "$file" .tar.xz)
 /build/"$directory"/install.sh --prefix=/build/rust --disable-ldconfig
done
export PATH=/build/rust/bin:$PATH CARGO_HOME=/build/cargo
source=$(find /material/sources -maxdepth 1 -name 'tiktoken-0.14.0-*.tar.gz')
[ -n "$source" ]
tar xf "$source" -C /build
cp /material/rust/publisher-locks/tiktoken/platform-locked-Cargo.lock /build/tiktoken-0.14.0/Cargo.lock
cp -R /material/rust/tiktoken-vendor /build/tiktoken-0.14.0/vendor
mkdir -p /build/tiktoken-0.14.0/.cargo
printf '%s\n' '[source.crates-io]' 'replace-with = "vendored-sources"' '[source.vendored-sources]' 'directory = "vendor"' > /build/tiktoken-0.14.0/.cargo/config.toml
cd /build/tiktoken-0.14.0
python -m pip wheel --no-build-isolation --no-deps --wheel-dir /build/wheels .
