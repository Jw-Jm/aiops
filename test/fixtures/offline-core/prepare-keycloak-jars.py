"""Verify packaged Keycloak JARs against exact Maven artifacts and sources.

The official release notice is retained verbatim. SHA-256 binds each packaged
JAR to its Maven POM and available source JAR. Missing corresponding source for
copyleft artifacts is a hard error. Does not change admission status.
"""
import concurrent.futures
import hashlib
import json
from pathlib import Path
import urllib.error
import urllib.request
import zipfile
import io
import struct
import xml.etree.ElementTree as ET


ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "keycloak-maven"


def class_source(data):
    """Read the JVM SourceFile attribute, including package-private classes.

    JVM Specification 4.4 and 4.7.10; never executes a distributed class.
    """
    stream = io.BytesIO(data)
    def read(size):
        value = stream.read(size)
        if len(value) != size:
            raise RuntimeError("truncated class file")
        return value
    def u2():
        return struct.unpack(">H", read(2))[0]
    if read(4) != b"\xca\xfe\xba\xbe":
        raise RuntimeError("invalid class magic")
    read(4)
    pool = {}
    index, count = 1, u2()
    widths = {3: 4, 4: 4, 5: 8, 6: 8, 8: 2, 9: 4, 10: 4, 11: 4, 12: 4, 15: 3, 16: 2, 17: 4, 18: 4, 19: 2, 20: 2}
    while index < count:
        tag = read(1)[0]
        if tag == 1:
            pool[index] = read(u2()).decode("utf-8", errors="replace")
        elif tag == 7:
            pool[index] = u2()
        elif tag in widths:
            read(widths[tag])
        else:
            raise RuntimeError("unknown constant pool tag")
        index += 2 if tag in (5, 6) else 1
    read(2)
    name = pool[pool[u2()]]
    read(2)
    read(2 * u2())
    def attributes():
        result = {}
        for _ in range(u2()):
            key = pool[u2()]
            result[key] = read(struct.unpack(">I", read(4))[0])
        return result
    for _ in range(2):  # fields, methods
        for _ in range(u2()):
            read(6)
            attributes()
    source = attributes().get("SourceFile")
    if source is None or len(source) != 2:
        raise RuntimeError("class has no SourceFile provenance")
    filename = pool[struct.unpack(">H", source)[0]]
    if Path(filename).name != filename:
        raise RuntimeError("unsafe class source filename")
    return name.rsplit("/", 1)[0] + "/" + filename if "/" in name else filename


def fetch(url, destination, optional=False):
    if destination.exists():
        return destination.read_bytes()
    try:
        with urllib.request.urlopen(url, timeout=60) as response:
            data = response.read()
    except urllib.error.HTTPError as error:
        if optional and error.code == 404:
            return None
        raise
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)
    return data


def main():
    mappings = json.loads((ROOT / "keycloak-jar-license-mapping.json").read_text())["jars"]
    # POM coordinates are taken from the release notice and matched image paths,
    # not guessed from unversioned repository branches.
    def prepare(item):
        coordinate = item["coordinate"]
        group, artifact, version = coordinate.split(":")
        if item["path"].startswith("/opt/keycloak/lib/"):
            local = ROOT / "keycloak-image" / "lib" / item["path"].removeprefix("/opt/keycloak/lib/")
        elif item["path"].startswith("/opt/keycloak/bin/client/"):
            local = ROOT / "keycloak-image" / "client" / item["path"].removeprefix("/opt/keycloak/bin/client/")
        else:
            raise RuntimeError("JAR is outside the captured Keycloak distribution")
        original = local.read_bytes()
        if group.startswith("org.keycloak") or local.name.startswith("org.keycloak.keycloak-"):
            # A shaded dependency POM is not the enclosing Keycloak artifact's
            # identity. Retain it as embedded evidence instead of assigning
            # its license/source to the entire distribution JAR.
            filename = local.name.removeprefix("org.keycloak.")
            if filename.startswith("keycloak-") and filename.endswith("-26.7.4.jar"):
                root_coordinate = "org.keycloak:" + filename.removesuffix("-26.7.4.jar") + ":26.7.4"
            else:
                root_coordinate = coordinate
            return {"coordinate": root_coordinate, "imagePath": item["path"], "jarSHA256": hashlib.sha256(original).hexdigest(),
                    "sourceCommit": "aa9fe3fba0c6cd5770f19a49378c55f4378cf544", "licenses": ["Apache-2.0"],
                    "embeddedNoticeCoordinate": coordinate if coordinate != root_coordinate else None,
                    "embeddedNoticeLicenses": item["licenses"] if coordinate != root_coordinate else []}
        base = "https://repo.maven.apache.org/maven2/" + group.replace(".", "/") + "/" + artifact + "/" + version + "/"
        prefix = artifact + "-" + version
        directory = OUT / group / artifact / version
        # Native libraries ship distinct platform classifiers. Match the
        # distribution filename rather than comparing to the empty base JAR.
        filename = local.name.removeprefix(group + ".")
        if filename != prefix + ".jar" and not filename.startswith(prefix + "-"):
            raise RuntimeError("JAR filename does not match its exact coordinate: " + item["path"])
        jar = fetch(base + filename, directory / filename)
        metadata_only_difference = hashlib.sha256(jar).digest() != hashlib.sha256(original).digest()
        packaging_changes = []
        if metadata_only_difference:
            # The image normalizes ZIP metadata. Require every entry byte to
            # match, including manifests, signatures, notices and resources.
            with zipfile.ZipFile(io.BytesIO(original)) as image_zip, zipfile.ZipFile(io.BytesIO(jar)) as maven_zip:
                def entries(archive):
                    result = {}
                    for entry in archive.infolist():
                        result.setdefault(entry.filename, []).append(hashlib.sha256(archive.read(entry)).hexdigest())
                    return result
                # Preserve the order of repeated entries as well: Java readers
                # can choose one occurrence. Duplicate directory records are
                # present in upstream Commons Codec and are not code changes.
                image_entries, maven_entries = entries(image_zip), entries(maven_zip)
                if image_entries != maven_entries:
                    changed = {name for name in image_entries.keys() | maven_entries.keys() if image_entries.get(name) != maven_entries.get(name)}
                    # The exact official Keycloak image repackages H2: Java
                    # classes/resources remain identical; its manifest is
                    # rewritten and native-image reflection metadata removed.
                    allowed = {"META-INF/MANIFEST.MF", "META-INF/native-image/reflect-config.json"}
                    if coordinate != "com.h2database:h2:2.4.240" or not changed <= allowed:
                        raise RuntimeError("packaged JAR contents differ from exact Maven artifact: " + coordinate)
                    packaging_changes = [{"path": name, "imageEntrySHA256": image_entries.get(name), "mavenEntrySHA256": maven_entries.get(name)} for name in sorted(changed)]
        pom = fetch(base + prefix + ".pom", directory / (prefix + ".pom"))
        source = fetch(base + prefix + "-sources.jar", directory / (prefix + "-sources.jar"), optional=True)
        if source is None and any(any(family in license for family in ("GPL", "MPL", "EPL", "CDDL")) for license in item["licenses"]):
            raise RuntimeError("copyleft source JAR unavailable: " + coordinate)
        record = {"coordinate": coordinate, "imagePath": item["path"], "licenses": item["licenses"],
                  "jarURL": base + filename, "jarSHA256": hashlib.sha256(jar).hexdigest(),
                  "imageJarSHA256": hashlib.sha256(original).hexdigest(), "zipMetadataOnlyDifference": metadata_only_difference and not packaging_changes,
                  "upstreamPackagingChanges": packaging_changes,
                  "pomURL": base + prefix + ".pom", "pomSHA256": hashlib.sha256(pom).hexdigest(),
                  "sourceURL": base + prefix + "-sources.jar" if source else None,
                  "sourceSHA256": hashlib.sha256(source).hexdigest() if source else None}
        (directory / (filename + "-lock.json")).write_text(json.dumps(record, indent=2) + "\n")
        print("verified", coordinate, "sources=" + str(source is not None), flush=True)
        return record

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        records = list(executor.map(prepare, mappings))
    # Aggregate JARs contain separately versioned Maven components. Preserve
    # their identities and corresponding sources as well as the enclosing JAR.
    covered = {record["coordinate"] for record in records}
    parents = {record["imagePath"]: record for record in records}
    notice = json.loads((ROOT / "keycloak-notice-index.json").read_text())
    sbom = json.loads((ROOT / "sbom/keycloak.enriched.syft.json").read_text())
    embedded = []
    seen = set()
    for artifact in sbom["artifacts"]:
        if artifact["type"] != "java-archive":
            continue
        properties = artifact["metadata"].get("pomProperties", {})
        coordinate = ":".join(properties.get(key, "") for key in ("groupId", "artifactId", "version"))
        if coordinate == "::" or coordinate in covered or coordinate in seen:
            continue
        seen.add(coordinate)
        image_path = artifact["locations"][0]["path"]
        parent = parents[image_path]
        group, artifact_name, version = coordinate.split(":")
        if parent["coordinate"].split(":")[:2] == [group, artifact_name] and parent.get("sourceSHA256"):
            # SQL Server's POM uses 13.2.1 while its published Java 11 artifact
            # is 13.2.1.jre11. The already matched enclosing artifact supplies
            # the exact source; do not guess an unpublished base version.
            embedded.append({"coordinate": coordinate, "imagePath": image_path,
                             "enclosingCoordinate": parent["coordinate"], "sourceSHA256": parent["sourceSHA256"],
                             "sourceURL": parent["sourceURL"], "licenses": parent["licenses"]})
            continue
        if coordinate not in notice and parent.get("sourceSHA256"):
            # Publisher notices describe aggregate artifacts (e.g. angus-mail),
            # while the scanner also reports their internal POMs. Verify the
            # aggregate source includes every outer Java class before reusing
            # that exact source identity. This is not a new license approval.
            parent_group, parent_artifact, parent_version = parent["coordinate"].split(":")
            parent_source = OUT / parent_group / parent_artifact / parent_version / (parent_artifact + "-" + parent_version + "-sources.jar")
            parent_jar = ROOT / "keycloak-image/lib" / image_path.removeprefix("/opt/keycloak/lib/")
            with zipfile.ZipFile(parent_source) as source_zip, zipfile.ZipFile(parent_jar) as binary_zip:
                available = set(source_zip.namelist())
                required = {class_source(binary_zip.read(name)) for name in binary_zip.namelist()
                            if name.endswith(".class") and not name.startswith("META-INF/") and name != "module-info.class"}
                generated_inputs = []
                aggregate_covered = True
                for absent in sorted(required - available):
                    # This exact Narayana aggregate has five JBoss Logging
                    # processor outputs. Its annotated interfaces are the
                    # preferred source inputs, not hand-written logger files.
                    original = absent.removesuffix("_$logger.java") + ".java"
                    if parent["coordinate"] != "org.jboss.narayana.jta:narayana-jta:7.3.3.Final" or not absent.endswith("_$logger.java") or original not in available:
                        aggregate_covered = False
                        break
                    content = source_zip.read(original)
                    if b"@MessageLogger" not in content or b"org.jboss.logging.annotations" not in content:
                        raise RuntimeError("missing logger processor source declaration")
                    generated_inputs.append({"generatedSourceFile": absent, "preferredSourceFile": original,
                                             "preferredSourceSHA256": hashlib.sha256(content).hexdigest()})
            if aggregate_covered:
                embedded.append({"coordinate": coordinate, "imagePath": image_path, "enclosingCoordinate": parent["coordinate"],
                                 "sourceURL": parent["sourceURL"], "sourceSHA256": parent["sourceSHA256"],
                                 "licenses": parent["licenses"], "noticeScope": parent["coordinate"],
                                 "aggregateClassSourceFiles": len(required), "generatedSourceInputs": generated_inputs})
                continue
        base = "https://repo.maven.apache.org/maven2/" + group.replace(".", "/") + "/" + artifact_name + "/" + version + "/"
        prefix = artifact_name + "-" + version
        directory = OUT / group / artifact_name / version
        pom = fetch(base + prefix + ".pom", directory / (prefix + ".pom"))
        source = fetch(base + prefix + "-sources.jar", directory / (prefix + "-sources.jar"))
        declared_licenses = [{"name": element.findtext("{http://maven.apache.org/POM/4.0.0}name"),
                              "url": element.findtext("{http://maven.apache.org/POM/4.0.0}url")}
                             for element in ET.fromstring(pom).findall("{http://maven.apache.org/POM/4.0.0}licenses/{http://maven.apache.org/POM/4.0.0}license")]
        embedded.append({"coordinate": coordinate, "imagePath": image_path, "enclosingCoordinate": parent["coordinate"],
                         "pomURL": base + prefix + ".pom", "pomSHA256": hashlib.sha256(pom).hexdigest(),
                         "sourceURL": base + prefix + "-sources.jar", "sourceSHA256": hashlib.sha256(source).hexdigest(),
                         "licenses": notice.get(coordinate, {}).get("licenses"), "pomDeclaredLicenses": declared_licenses,
                         "licenseReviewRequired": coordinate not in notice})
        print("verified embedded sources", coordinate, flush=True)
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "jar-source-lock.json").write_text(json.dumps({"schemaVersion": 1, "artifacts": records, "embeddedArtifacts": embedded,
        "officialNoticeSHA256": hashlib.sha256((ROOT / "keycloak-third-party-notice-26.7.4.html").read_bytes()).hexdigest(),
        "qualificationPassed": False}, indent=2) + "\n")


if __name__ == "__main__":
    main()
