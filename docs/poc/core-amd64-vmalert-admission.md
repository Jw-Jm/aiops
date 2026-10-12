# Native amd64 vmalert material admission

The authenticated vmalert v1.116.0 native distribution has exact measured package name/version parity with the retained publisher source closure. All 250 source inventory files and 130 catalog notice digests were independently verified.

The isolated native rule `vector(1) > 0` was evaluated against native VictoriaMetrics in the same network namespace and reached firing state. Both runtimes used read-only root filesystems; external notifications were explicitly blackholed. [Native proof](../evidence/amd64-deployment-20261009/vmalert-native-poc-result.json) and [source integrity](../evidence/amd64-deployment-20261009/vmalert-shared-source-integrity.json).

This qualifies native material and rule evaluation. Release notification delivery and platform incident acceptance remain separate and unverified.
