"""Freeze the ADR-0010 native notice review at exact image/package/file scope.

Only the seven explicitly reviewed arm64 images are allowed. The resulting
compiled registry cannot approve another package/image or a changed source,
notice, source bundle or ADR. No candidate state is changed by this script.
"""
import hashlib
import json
from pathlib import Path

ROOT = Path("artifacts/task27-materials")
IMAGES = {
    "postgresql": "75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257",
    "keycloak": "1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c",
    "seaweedfs": "d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e",
    "openbao": "4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6",
    "victoria-metrics": "b10c78f4bd9b52554b7f863ff416e480d931b1811f591049d166eea1fb247638",
    "victoria-logs": "47b820890d64c4575a2a0a46415dcd8a4fd59a0f1fcd6a377693d7aea639442e",
    "vmalert": "48e01bd36d098b9c8a1537d38235e0194013e38853196b446cb0eb1f17057311",
}
ADR = "docs/adr/0010-core-image-license-admission.md"
VERSIONS = {"postgresql": "17.11", "keycloak": "26.7.4", "seaweedfs": "4.47", "openbao": "2.7.0",
            "victoria-metrics": "v1.116.0", "victoria-logs": "v1.52.0", "vmalert": "v1.116.0"}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    adr = Path(ADR).read_bytes()
    if b"\nStatus: Accepted\n" not in adr:
        raise RuntimeError("native license policy must be Accepted before freezing review")
    native = json.loads((ROOT / "license-review/native-notices/native-notice-lock.json").read_text())
    source_bundles = {p["component"]: p for p in json.loads((ROOT / "corresponding-sources/source-bundle-lock.json").read_text())["components"]}
    records = []
    evidence = Path("third_party/licenses/native")
    evidence.mkdir(parents=True, exist_ok=True)
    for package in native["packages"]:
        component = package["component"]
        if component not in IMAGES or not package["sourceFiles"] or not package["noticeFiles"]:
            raise RuntimeError("native scope not covered by ADR review")
        source = package["sourceFiles"][0]
        identity = {"component": component, "componentVersion": VERSIONS[component], "imageDigest": "sha256:" + IMAGES[component],
                    "dependencyName": "native:" + package["package"], "version": package["version"],
                    "source": source.get("url", source.get("source")), "sourceArchiveSHA256": "sha256:" + source["sha256"],
                    "correspondingSourceBundleSHA256": "sha256:" + source_bundles[component]["sha256"],
                    "sourcePackage": package["sourcePackage"], "sourceVersion": package["sourceVersion"]}
        if not identity["source"] or not identity["source"].startswith("https://"):
            raise RuntimeError("native publisher source origin missing")
        blob = bytearray()
        for notice in package["noticeFiles"]:
            data = Path(notice["file"]).read_bytes()
            if sha(data) != notice["sha256"]:
                raise RuntimeError("reviewed notice changed")
            blob.extend(("\n===== Original publisher notice: " + notice["path"] + " =====\n").encode())
            blob.extend(data)
        identity["noticeDigest"] = "sha256:" + sha(blob)
        # The reference identifies this full exact permission/notice scope, not
        # a fabricated standard license or a general public-domain fallback.
        reference = "LicenseRef-Task27-Native-" + sha(json.dumps(identity, sort_keys=True).encode())
        identity["id"] = reference
        identity["noticePath"] = "third_party/licenses/native/" + reference + ".txt"
        identity["adr"] = ADR
        identity["adrDigest"] = "sha256:" + sha(adr)
        identity["obligations"] = ["retain-verbatim-publisher-notices", "accompany-complete-corresponding-source",
                                   "preserve-file-specific-exceptions-and-attribution", "allow-library-replacement-and-relinking"]
        Path(identity["noticePath"]).write_bytes(blob)
        embedded = Path("bundle/evidence") / identity["noticePath"]
        embedded.parent.mkdir(parents=True, exist_ok=True)
        embedded.write_bytes(blob)
        records.append(identity)
    destination = Path("internal/supplychain/licenses/native-core-reviewed.json")
    destination.parent.mkdir(exist_ok=True)
    destination.write_text(json.dumps({"schemaVersion": 1, "scope": "ADR-0010 unchanged pinned development arm64 native packages",
                                      "licenses": records}, indent=2, sort_keys=True) + "\n")
    adr_copy = Path("bundle/evidence") / ADR
    adr_copy.parent.mkdir(parents=True, exist_ok=True)
    adr_copy.write_bytes(adr)
    print("frozen exact native permission/notice scopes:", len(records))


if __name__ == "__main__":
    main()
