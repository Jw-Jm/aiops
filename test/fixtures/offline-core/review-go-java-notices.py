"""Bind package license decisions to the exact prepared source and image files.

This is connected preparation evidence only. It neither qualifies Catalog
entries nor substitutes for the live core installation gate.
"""
import csv
import functools
import hashlib
import json
from pathlib import Path
import tarfile
import zipfile

ROOT = Path("artifacts/task27-materials")
OUT = ROOT / "license-review"
KNOWN = {"Apache-2.0", "MIT", "MIT-0", "BSD-2-Clause", "BSD-2-Clause-Views",
         "BSD-3-Clause", "BSD-4-Clause", "ISC", "MPL-2.0", "CC0-1.0",
         "EPL-1.0", "EPL-2.0", "LGPL-2.1-or-later", "Unicode-DFS-2016", "FSFAP"}
GO_OVERRIDES = {
    "github.com/google/flatbuffers/go": ("Apache-2.0", "third_party/licenses/seaweedfs/flatbuffers-parent-lock.json"),
    "github.com/jmespath/go-jmespath": ("Apache-2.0", "partial Apache header in exact module LICENSE"),
    "github.com/kurin/blazer": ("Apache-2.0", "partial Apache header in exact module LICENSE"),
    "github.com/ovh/kmip-go": ("Apache-2.0", "partial Apache header in exact module LICENSE"),
    "github.com/yeqown/reedsolomon": ("MIT", "third_party/licenses/openbao/reedsolomon-license-lock.json"),
    "github.com/boltdb/bolt": ("MPL-2.0", "actual local replacement internal/helper/stubbolt/bolt.go SPDX header"),
    "github.com/openbao/openbao/api/v2": ("MPL-2.0", "OpenBao root LICENSE and local API file headers"),
    "github.com/openbao/openbao/api/auth/kubernetes/v2": ("MPL-2.0", "OpenBao root LICENSE and local API file headers"),
    "github.com/openbao/openbao/sdk/v2": ("MPL-2.0", "OpenBao root LICENSE and local SDK file headers"),
    "github.com/valyala/gozstd": ("MIT AND BSD-3-Clause", "Go wrapper MIT; bundled zstd LICENSE offers BSD-3-Clause, selected over GPL choice"),
    "github.com/moby/sys/user": ("Apache-2.0", "exact module LICENSE"),
    "github.com/tianon/gosu": ("Apache-2.0", "upstream exact 1.19 full commit source LICENSE"),
    "golang.org/x/sys": ("BSD-3-Clause", "exact module LICENSE"),
    "github.com/apache/thrift": ("Apache-2.0 AND BSD-3-Clause AND FSFAP", "actual compiled Go source headers Apache-2.0; complete root LICENSE retains BSD and GNU all-permissive source notices"),
    "github.com/hashicorp/vic": ("Apache-2.0", "all four actual pkg/vsphere/tags source headers Apache-2.0; root third-party license catalog is retained verbatim, not treated as compiled dependencies"),
}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def verify(path, digest):
    data = Path(path).read_bytes()
    if sha(data) != digest:
        raise RuntimeError("prepared source changed: " + str(path))
    return data


@functools.lru_cache(maxsize=520)
def archive_notices(path):
    if str(path).endswith(".zip"):
        with zipfile.ZipFile(path) as archive:
            return [(name, archive.read(name)) for name in archive.namelist()
                    if Path(name).name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")) and not name.endswith("/")]
    with tarfile.open(path) as archive:
        return [(entry.name, archive.extractfile(entry).read()) for entry in archive
                if entry.isfile() and Path(entry.name).name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE"))]


def main():
    OUT.mkdir(exist_ok=True)
    rows = {}
    for component in ("seaweedfs", "openbao", "victoria-metrics", "victoria-logs"):
        rows[component] = list(csv.reader((ROOT / "go-sources" / (component + "-licenses.csv")).open()))
    records = []
    modules = json.loads((ROOT / "go-module-sources/source-module-lock.json").read_text())["modules"]
    for source in modules:
        if source.get("sourceKind") == "application-source":
            name = {"github.com/VictoriaMetrics/VictoriaLogs": "victoria-logs",
                    "github.com/VictoriaMetrics/VictoriaMetrics": "victoria-metrics",
                    "github.com/seaweedfs/seaweedfs": "seaweedfs"}.get(source["module"], "openbao")
            source["file"] = str(ROOT / "go-sources" / (name + ".tar.gz"))
        verify(source["file"], source["sha256"])
        module = source["module"]
        if module == "stdlib":
            expression, basis = "BSD-3-Clause", "official exact Go source archive LICENSE"
        elif module in GO_OVERRIDES:
            expression, basis = GO_OVERRIDES[module]
        else:
            kinds = set()
            for component in source["components"]:
                report = rows.get("victoria-metrics" if component == "vmalert" else component, [])
                kinds.update(row[2] for row in report if row[0] == module or row[0].startswith(module + "/"))
            if not kinds or not kinds <= KNOWN:
                raise RuntimeError("unreviewed compiled Go package license: " + module + " " + repr(kinds))
            expression, basis = " AND ".join(sorted(kinds)), "go-licenses v2.0.1 compiled upstream package report; original notices retained"
        notices = []
        supplements = {"github.com/google/flatbuffers/go": "third_party/licenses/seaweedfs/flatbuffers-parent-LICENSE",
                       "github.com/yeqown/reedsolomon": "third_party/licenses/openbao/reedsolomon-MIT-LICENSE"}
        source_notices = archive_notices(source["file"])
        if module in supplements:
            p = Path(supplements[module])
            source_notices = source_notices + [(str(p), p.read_bytes())]
        for path, data in source_notices:
            digest = sha(data)
            target = OUT / "go-notices" / (digest + ".txt")
            target.parent.mkdir(exist_ok=True)
            if not target.exists():
                target.write_bytes(data)
            notices.append({"path": path, "sha256": digest, "file": str(target)})
        if not notices:
            raise RuntimeError("source notice missing: " + module)
        records.append({**source, "reviewedLicense": expression, "licenseBasis": basis, "sourceNotices": notices})
    (OUT / "go-license-lock.json").write_text(json.dumps({"schemaVersion": 1, "modules": records, "qualificationPassed": False}, indent=2) + "\n")

    jar_lock = json.loads((ROOT / "keycloak-maven/jar-source-lock.json").read_text())
    notice = json.loads((ROOT / "keycloak-notice-index.json").read_text())
    jars = []
    for record in jar_lock["artifacts"] + jar_lock["embeddedArtifacts"]:
        item = dict(record)
        coordinate = item["coordinate"]
        licenses = item.get("licenses")
        evidence = None
        if coordinate == "org.aesh:terminal-api:2.6.3":
            if item["pomSHA256"] != "bed205966894eab308330098626f63dbf8c7ba3cd32470871c1932d805bef8fe" or item["sourceSHA256"] != "5a9c765b6a2e69d7fde0074dfa6a97271e477f1e17ef710f97dca734641d91df":
                raise RuntimeError("Aesh exact reviewed identity changed")
            licenses, evidence = ["Apache-2.0"], "exact standalone POM declares Apache-2.0; Connection.java carries Apache header"
        elif coordinate in ("org.openjdk.nashorn:nashorn-core:15.4", "io.smallrye.classfile:jdk-classfile-backport:26"):
            licenses = ["GPL-2.0-only WITH Classpath-exception-2.0"]
            evidence = "actual source headers and full upstream Classpath exception text retained"
        elif coordinate == "com.mysql:mysql-connector-j:9.6.0":
            licenses = ["GPL-2.0-only WITH Universal-FOSS-exception-1.0"]
            evidence = "exact source LICENSE and headers include Universal FOSS Exception 1.0; Keycloak corresponding source accompanies distribution"
        elif coordinate.startswith("org.jline:") and licenses == ["BSD-4-Clause"]:
            text = "\n".join(notice.get(item.get("noticeScope", coordinate), {}).get("texts", []))
            if "BSD-3-Clause" not in text or "Neither the name of JLine" not in text or "All advertising" in text:
                raise RuntimeError("JLine notice no longer matches the reviewed three-clause text")
            licenses, evidence = ["BSD-3-Clause"], "publisher notice label says BSD-4-Clause, but its verbatim terms and exact Java header are BSD-3-Clause"
        if not licenses or any(value not in KNOWN and " WITH " not in value for value in licenses):
            raise RuntimeError("unreviewed Java license: " + coordinate + " " + repr(licenses))
        item["reviewedLicense"] = " AND ".join(sorted(set(licenses)))
        item["licenseBasis"] = evidence or "official exact Keycloak release third-party notice and matching Maven/source identity"
        item["licenseReviewRequired"] = False
        texts = notice.get(item.get("noticeScope", coordinate), {}).get("texts", [])
        item["noticeTextSHA256"] = [sha(text.encode()) for text in texts]
        jars.append(item)
    (OUT / "java-license-lock.json").write_text(json.dumps({"schemaVersion": 1, "artifacts": jars,
        "officialNoticeSHA256": jar_lock["officialNoticeSHA256"], "qualificationPassed": False}, indent=2) + "\n")
    print("verified source/license bindings:", len(records), "Go identities;", len(jars), "Java identities")


if __name__ == "__main__":
    main()
