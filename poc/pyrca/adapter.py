"""PoC-only bounded ranking adapter. No source clients, SQL, services or LLM."""
import hashlib
import json
from pathlib import Path

import numpy as np
import pandas as pd
from pyrca.analyzers.epsilon_diagnosis import EpsilonDiagnosis, EpsilonDiagnosisConfig

ROOT = Path(__file__).parent


def frozen_inputs():
    manifest = json.loads((ROOT / "fixtures/manifest.json").read_text())
    inputs = {}
    for name, digest in manifest["files"].items():
        raw = (ROOT / "fixtures" / name).read_bytes()
        if hashlib.sha256(raw).hexdigest() != digest:
            raise ValueError("frozen holdout drift")
        inputs[name] = json.loads(raw)
    training = inputs["training-v1.json"]
    holdout = inputs["holdout-v1.json"]["cases"]
    if any(training["window"]["to"] >= c["metricEvidence"]["window"]["from"] for c in holdout):
        raise ValueError("training/evaluation windows overlap")
    return training, holdout


def rank(model, metric_evidence, candidates):
    if metric_evidence["schemaVersion"] != "metric-evidence-holdout/v1" or metric_evidence["type"] != "metric":
        raise ValueError("versioned Metric Evidence required")
    if len(candidates) > 64 or any(c["source"] != "deterministic" for c in candidates):
        raise ValueError("bounded deterministic Candidates required")
    columns = metric_evidence["columns"]
    if len(metric_evidence["values"]) != 48 or len(columns) != 4:
        raise ValueError("frozen evaluation shape required")
    response = model.find_root_causes(pd.DataFrame(metric_evidence["values"], columns=columns))
    scores = dict(response.root_cause_nodes)
    # PyRCA can reorder only already supplied Candidate keys. It cannot emit a
    # new candidate, evidence reference, cause confirmation or lifecycle state.
    return sorted(candidates, key=lambda c: (-scores.get(c["metricColumn"], 0), -c["score"], c["candidateKey"]))


def evaluate_holdout():
    training, holdout = frozen_inputs()
    np.random.seed(5108)  # fixed before training; no label-based tuning
    model = EpsilonDiagnosis(EpsilonDiagnosisConfig(alpha=0.05, bootstrap_time=200, root_cause_top_k=3))
    model.train(pd.DataFrame(training["values"], columns=training["columns"]))
    results = []
    domains = {}
    for case in holdout:
        baseline = sorted(case["deterministicCandidates"], key=lambda c: (-c["score"], c["candidateKey"]))
        adjusted = rank(model, case["metricEvidence"], case["deterministicCandidates"])
        before = case["groundTruth"] in [c["candidateKey"] for c in baseline[:3]]
        after = case["groundTruth"] in [c["candidateKey"] for c in adjusted[:3]]
        row = {"caseId": case["caseId"], "domain": case["domain"], "baselineTop3Hit": before,
               "pyrcaTop3Hit": after, "baselineStatus": case["baselineStatus"],
               "pyrcaStatus": case["baselineStatus"], "adjustedKeys": [c["candidateKey"] for c in adjusted]}
        results.append(row)
        domains.setdefault(case["domain"], []).append(row)
    stats = lambda rows: {"cases": len(rows), "baselineTop3": sum(r["baselineTop3Hit"] for r in rows) / len(rows),
                          "pyrcaTop3": sum(r["pyrcaTop3Hit"] for r in rows) / len(rows)}
    total = stats(results)
    gain = 100 * (total["pyrcaTop3"] - total["baselineTop3"])
    return {"schemaVersion": "pyrca-gain-decision/v1", **total, "absoluteGainPercentagePoints": gain,
            "falseConfirmedIncrease": sum(r["pyrcaStatus"] == "confirmed" and r["baselineStatus"] != "confirmed" for r in results), "domainMetrics": {k: stats(v) for k, v in domains.items()},
            "results": results, "gainGate": gain >= 5,
            "resourceLatencyAdmission": "unverified-user-performance-waiver",
            "fullDependencyAdmission": False, "runtimeEnabled": False,
            "limitations": ["nine synthetic frozen cases, not live hardware/production validation",
                            "deterministic Top-3 baseline has a ceiling on this holdout",
                            "full PyRCA package dependencies not admitted for locked Python3.12",
                            "no resource, sustained load or P95 measurements executed"]}


if __name__ == "__main__":
    print(json.dumps(evaluate_holdout(), indent=2))
