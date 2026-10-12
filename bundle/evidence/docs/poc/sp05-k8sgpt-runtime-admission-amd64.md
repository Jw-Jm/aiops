# Native amd64 fixed K8sGPT consumer

Unmodified v0.3.41 / f071b32aa85d77b197cd2d0f9868294f7b55c5eb was rebuilt with the repository's pinned Go 1.27.1 compiler, `GOPROXY=off`, `GOSUMDB=off`, CGO disabled and retained authenticated modules. Exact executable SHA-256 is e6ce7073f35889bc678443034169d4a64e94106442a5775fe3f59ca4ae6b66a4. The first Go 1.26.4 proof remains historical and does not bind this admission.

The complete source distribution retains all 233 original publisher module ZIPs, source notices and upstream archive, adds the complete pinned Go 1.27.1 source tree and an independent native executable/source lock. Historical ARM material remains unchanged.

The actual unchanged CLI passed fixed Pod, Node and PVC filters through the platform's ephemeral TLS read broker under finite CPU/memory cgroups and a read-only root filesystem. Native UIDs, Deployment/ReplicaSet owners, candidate decoding and no-provider/no-explain output were verified. The hash-verified private copy uses a bounded executable temporary mount. [Native fixture proof](../evidence/amd64-deployment-20261009/k8sgpt-fixed-native-result.json).

The earlier independent real-cluster Pod probe remains supplemental. This admission qualifies the fixed native process consumer; installed source registrations and durable Finding/Incident/Evidence acceptance remain separate. Arbitrary filters, provider/model invocation and deferred virtualization remain outside the consumer contract.
