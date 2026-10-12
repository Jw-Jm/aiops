# Native amd64 Metrics Server material

The authenticated official v0.8.0 index contains the retained historical ARM leaf and selected native amd64 leaf. The selected image has exact parity across all 100 measured package name/version identities; complete unchanged publisher source and notices remain available.

The native official server used the real target Kubernetes API and kubelet with strict certificate verification and delegated bearer authentication. It returned one real Node metric and nine Pod metrics with current timestamps. A reviewed exact node CSR enabled kubelet IP certificate verification. The isolated probe created no global APIService, and its namespace, scoped temporary RBAC, token and private serving identity were removed. [Native probe](../evidence/amd64-deployment-20261009/metrics-server-native-poc-result.json) and [kubelet certificate prerequisite](../evidence/amd64-deployment-20261009/kubelet-serving-bootstrap-result.json).

This qualifies native distribution and metric reads. Installed addon aggregation trust, release network policies, source registration and durable Inspection/Evidence/API/MCP delivery remain separate and unverified.
