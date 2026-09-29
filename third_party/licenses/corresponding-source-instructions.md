# Corresponding source, notices and replacement instructions

This archive accompanies an unchanged, exact upstream core image. It is source
distribution, not an installation-time build. Installing the signed Bundle does
not run these build scripts, extract nested publisher archives or fetch sources.
`SOURCE-INVENTORY.json` identifies every original filename, publisher URL,
version, full Git commit where applicable, checksum and package/image scope.
All licenses and copyright notices remain under their original terms. The
platform does not impose additional restrictions on copying, modification,
reverse engineering for debugging modifications, or replacement of these
components. Bundle signature verification establishes integrity of this release;
it does not prohibit building and signing a separate modified release.

## Native packages

Debian/PGDG packages include their original `.dsc`, upstream tarball and Debian
packaging archive (or complete native source tarball). Place those files together
using the inventory's original basenames, unpack with `dpkg-source -x <file.dsc>`,
and build with `dpkg-buildpackage -b -us -uc` in the matching distribution and
architecture. The `.dsc` and `debian/control` declare build dependencies;
`debian/rules` and included patches provide the distributor's build instructions.
The source archive's `debian/copyright` and the copied image copyright notices
retain file-specific conditions. The archive includes complete source for all
other distributed native packages, including libraries and programs using
Berkeley DB. Source is supplied directly, without a written-offer dependency.

Red Hat UBI packages include the exact source RPM named by the running image's
RPM database, including its spec, original sources and applied patches. Unpack
the source RPM into an RPM build tree; build with `rpmbuild --rebuild <source.rpm>`
for the recorded RHEL/UBI release and architecture. The spec declares BuildRequires
and the `%prep`, `%build`, `%install` and patch steps. OpenJDK's full source RPM
contains its OpenJDK tree, legal notices, build configuration and distributor
patches. These include the applicable GPL/ClassPath and compiler-runtime
exceptions; the original texts, not a scanner's broad label, govern each file.

Alpine packages include the exact aports `APKBUILD`, every listed checksum-bound
source and patch, and the original source-download origin. Put them together under
the recorded recipe directory and use `abuild` for the matching Alpine release
and architecture. Do not run recipes as root. The full recipe is preferred source
for generated configuration/data packages. Its checksums and source inventory
must be preserved when making an unchanged build; document local changes and
keep the original notices when distributing a modified package.

## Go applications and statically compiled dependencies

The full application Git archive and exact Go module ZIPs correspond to the
recorded binary buildinfo, including local OpenBao replacements. Go compiler
source archives are pinned by the official release-index checksum. Unpack the
application source and build according to its included Makefile/build scripts
using the recorded toolchain and native dependencies. Module ZIPs and their
`.info` metadata identify the exact module versions; they may be placed in a
local Go proxy/cache for an offline rebuild. Keep `go.mod`/`go.sum` and upstream
vendor metadata. Do not replace the VictoriaMetrics dependency of VictoriaLogs
with the separate deployed VictoriaMetrics release. The OpenBao boltdb path is
its local MPL stub. The Flatbuffers and reedsolomon supplementary notices retain
their exact source correspondence and all original attribution.

## Keycloak Java and native JAR libraries

The full exact Keycloak Git tree, Maven source JARs, image JAR hashes and complete
official release third-party notice accompany the unchanged image. The source
tree's Maven build files describe the application and dependencies. Each source
JAR is the preferred Java source for its matched Maven binary; aggregate records
identify embedded components and annotation-processor inputs for generated
loggers. H2's two recorded packaging metadata changes are upstream changes;
the inventory preserves their hashes and the unchanged class/resource comparison.

Native libraries embedded in JARs also include the full exact JNA, Netty, JLine,
Byte Buddy and Brotli4j source projects and any pinned Git submodules. Their
included native build files supply C/C++ source and build instructions. A Java
source JAR alone is not used as proof of native source completeness.

Replace or relink LGPL shared libraries using the same ABI, or replace an LGPL
JAR on the application classpath with a rebuilt compatible version. The image
can be rebuilt or a derived image can replace those libraries/JARs; no platform
EULA forbids those operations. Retain source, original license and notice files.
GPL/ClassPath and Universal FOSS exceptions are preserved with their source and
apply only within their actual publisher scope. EPL-covered source is supplied
under its original EPL terms. Platform API/Worker code does not link against
these separate native/Java component processes.

## Attribution and unchanged distribution

Full publisher copyright files and source notices accompany the images and this
archive. Where applicable, this distribution includes software developed by the
University of California, Berkeley and its contributors, Carnegie Mellon
University, the Massachusetts Institute of Technology, Kungliga Tekniska
Högskolan, the OpenSSL Project and Eric Young. RSA MD5 components are identified
as the RSA Data Security, Inc. MD5 Message-Digest Algorithm. These acknowledgements
do not imply endorsement. No upstream component code has been modified by the
platform; Helm configuration, ownership labels and read-only runtime mounts are
platform deployment configuration. Future modifications must preserve the
applicable conditions and be identified separately.
