# Native amd64 SeaweedFS material admission

The authenticated SeaweedFS 4.47 native distribution has exact measured package name/version parity with the retained publisher source closure. All 665 source inventory files and 239 catalog notice digests were independently verified.

The isolated native master/volume/filer probe passed actual file write, read and delete with network disabled and a read-only root filesystem. Writable data was limited to dedicated ephemeral mounts. [Native proof](../evidence/amd64-deployment-20261009/seaweedfs-native-poc-result.json) and [source integrity](../evidence/amd64-deployment-20261009/seaweedfs-shared-source-integrity.json).

Probe setup failures remain preserved. This qualifies native distribution and storage operations; release S3/TLS/retention and platform archive bootstrap remain separate acceptance work.
