# KubeVirt v1.9.0 + CDI v1.63.0 OrbStack PoC

Status: DEFERRED / NOT QUALIFIED. KubeVirt v1.9.0 and CDI v1.63.0 control planes reported healthy versions on the development OrbStack cluster. The DataVolume reached `Succeeded`, but the VM/VMI did not reach `Running`: KubeVirt reported unsupported AArch64 `host-passthrough` CPU emulation and an OrbStack pod route without a gateway. At the user's request on 2026-09-28, the KubeVirt/CDI installation was removed after confirming no VM, VMI, or DataVolume remained. Runtime compatibility is still unverified; no production compatibility claim follows from the control-plane or DataVolume results.

## Frozen compatibility evidence

- Kubernetes support matrix source: `https://github.com/kubevirt/sig-release/blob/47f8f09611f29fa2596fd9875a5794a8388da948/releases/k8s-support-matrix.md`
- Frozen source commit: `47f8f09611f29fa2596fd9875a5794a8388da948`; source SHA-256: `0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e`.
- KubeVirt v1.9.0 release commit: `79d34c1762169e5b0370ec491cef5ca12e6b504c`; its upstream release notes state support for Kubernetes 1.36 and the preceding two minors.
- CDI v1.63.0 release commit: `7182037fa3ce5a4922f9bc471ef22f97662b4556`. KubeVirt v1.9.0 source `kubevirtci/cluster-up/cluster/kind/common.sh` defaults `KUBEVIRT_CUSTOM_CDI_VERSION` to `v1.63.0` when deploying CDI; that source file SHA-256 is `23911aa71f8f4ed37954a8497a2599c8ecbc404726ec1db42773b3b37e72610e`.

## Initial read-only OrbStack probe

- Server: `v1.35.6+orb1`, commit `6ffe43029a0c56bfffd91b24535b22c6e37ab9d5`, `linux/arm64`.
- Existing KubeVirt/CDI CRDs and namespaces: none observed before installation.
- OrbStack node root `/dev/kvm`: absent; the PoC manifest explicitly sets `useEmulation: true`.
- This matrix decision is limited to the development OrbStack Server version. It is not production compatibility acceptance.

## Resolved Profile gate

Task 2.6 Step 4 could not produce a resolved virtualization profile. Reproduction command: `go run ./cmd/opsctl profile detect --context orbstack -o /tmp/task-2.6-detected-deployment-profile.yaml`; exit code 1, stdout empty, stderr `PROFILE_COMPONENT_CONFLICT: openbao: a candidate exists but its version, health, or required capability is incompatible or unknown` followed by `exit status 1`. A read-only `profile.Discover` diagnostic (temporary test `go test ./internal/profile -run '^TestTask26OpenBaoCandidateDiagnostic$' -count=1 -v`, exit 0; test source removed after capture) found:

```json
{"Namespace":"ops-system","Name":"ops-openbao","Endpoint":"https://ops-openbao.ops-system.svc.cluster.local:8200","Version":"","Digest":"sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6","Image":"ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6","Compatible":false,"Compatibility":"","Evidence":["Pod ops-system/ops-openbao-0 container[0] imageID docker-pullable://ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6","Service ops-system/ops-openbao port 8200","image version or runtime digest is unavailable"]}
```

The same digest is cataloged as OpenBao `2.7.0`; the Pod image reference is digest-only, and discovery does not infer the version from the catalog. Therefore the detector rejected this candidate before evaluating the template's bundled selection; the earlier bundled-conflict explanation was not established and is withdrawn. No detected or resolved profile was hand-authored or presented as generated evidence. CDI/KubeVirt exact versions and digests are recorded below and in the Component Catalog, but the resolved-profile deliverable remains incomplete.

## Upstream ARM64 software-emulation evidence

- The [KubeVirt ARM64 guide](https://kubevirt.io/user-guide/cluster_admin/virtual_machines_on_Arm64/) says `host-passthrough` is the only supported ARM64 CPU model.
- The [KubeVirt software-emulation guide](https://github.com/kubevirt/kubevirt/blob/v1.9.0/docs/software-emulation.md) describes `useEmulation` as selecting QEMU when `/dev/kvm` is unavailable. It does not document a supported alternative ARM64 CPU model for this path.
- Upstream [KubeVirt issue #11917](https://github.com/kubevirt/kubevirt/issues/11917) records the same `host-passthrough`/AArch64/QEMU hypervisor error on Apple Silicon despite enabling software emulation. The live OrbStack event below reproduces that error on the selected KubeVirt v1.9.0 image.
- The live run also reported `no gateway address found in routes for eth0` during VMI synchronization. This is separately preserved in Attempt 6.
- No unsupported CPU model, guest architecture, or network workaround is used to call the VM lifecycle successful. The exact-version Kubernetes compatibility decision remains development-only; KubeVirt/CDI stay `candidate` pending this failed lifecycle PoC and outstanding license/dependency admission.

## PoC execution

Latest live attempt: `OPS_KUBEVIRT_ORBSTACK_POC=1 go test ./test/e2e -run '^TestKubeVirtPOCArtifactsAndOrbStackLifecycle$' -count=1 -timeout=20m -v` exited 1 after 54.46s. The VMI reached a terminal synchronization failure; the captured events include both the unsupported ARM64 `host-passthrough` CPU mode and `no gateway address found in routes for eth0`. The test cleaned its release-labeled namespace and objects. Detailed command/event evidence follows in Attempt 7.

### Attempt 1


- Run started: 2026-09-27T14:42:17Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 14m42s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "node-role.kubernetes.io/control-plane": "true",
                    "node.kubernetes.io/instance-type": "k3s"
                },
                "name": "orbstack",
                "resourceVersion": "70240",
                "uid": "cc6ee959-3391-432f-b413-a5e7f2e64756"
            },
            "spec": {
                "podCIDR": "192.168.194.0/25",
                "podCIDRs": [
                    "192.168.194.0/25"
                ],
                "providerID": "k3s://orbstack"
            },
            "status": {
                "addresses": [
                    {
                        "address": "192.168.139.2",
                        "type": "InternalIP"
                    },
                    {
                        "address": "fd07:b51a:cc66::2",

...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "53761",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

error=exit status 1
Error from server (NotFound): customresourcedefinitions.apiextensions.k8s.io "kubevirts.kubevirt.io" not found

### Pre-install inventory

crd/kubevirts.kubevirt.io absent

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

error=exit status 1
Error from server (NotFound): customresourcedefinitions.apiextensions.k8s.io "cdis.cdi.kubevirt.io" not found

### Pre-install inventory

crd/cdis.cdi.kubevirt.io absent

### $ kubectl --context orbstack get namespace/kubevirt -o json

error=exit status 1
Error from server (NotFound): namespaces "kubevirt" not found

### Pre-install inventory

namespace/kubevirt absent

### $ kubectl --context orbstack get namespace/cdi -o json

error=exit status 1
Error from server (NotFound): namespaces "cdi" not found

### Pre-install inventory

namespace/cdi absent

### $ kubectl --context orbstack get namespace/ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

### Pre-install inventory

namespace/ops-sp02-kubevirt-poc absent

### $ docker pull --platform linux/arm64 quay.io/kubevirt/pr-helper@sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431

exit=0
quay.io/kubevirt/pr-helper@sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431: Pulling from kubevirt/pr-helper
dc443079bbcf: Pulling fs layer
2780920e5dbf: Pulling fs layer
56334e624848: Pulling fs layer
3214acf345c0: Pulling fs layer
dd64bf2dd177: Pulling fs layer
bf7a4185f015: Pulling fs layer
36a0d76d579d: Pulling fs layer
990a9c434e5e: Pulling fs layer
39dc083afc39: Pulling fs layer
61f16819d365: Pulling fs layer
b7ba05ccb2c4: Pulling fs layer
069d1e267530: Pulling fs layer
81be07fea0a8: Pulling fs layer
52630fc75a18: Pulling fs layer
dcaa5a89b0cc: Pulling fs layer
b839dfae01f6: Pulling fs layer
02c923a6e8c9: Pulling fs layer
7c12895b777b: Pulling fs layer
45fe2a39da8e: Pulling fs layer
990a9c434e5e: Already exists
dcaa5a89b0cc: Already exists
7c12895b777b: Already exists
dd64bf2dd177: Already exists
bf7a4185f015: Already exists
b839dfae01f6: Already exists
069d1e267530: Already exists
52630fc75a18: Already exists
2780920e5dbf: Already exists
39dc083afc39: Already exists
3214acf345c0: Already exists
dc443079bbcf: Download complete
81be07fea0a8: Download complete
02c923a6e8c9: Download complete
b7ba05ccb2c4: Download complete
36a0d76d579d: Download complete
36a0d76d579d: Pull complete
45fe2a39da8e: Download complete
61f16819d365: Download complete
990a9c434e5e: Pull complete
bf7a4185f015: Pull complete
2780920e5dbf: Pull complete
39dc083afc39: Pull complete
7c12895b777b: Pull complete
dd64bf2dd177: Pull complete
52630fc75a18: Pull complete
3214acf345c0: Pull complete
dcaa5a89b0cc: Pull complete
b839dfae01f6: Pull complete
069d1e267530: Pull complete
45fe2a39da8e: Pull complete
61f16819d365: Pull complete
81be07fea0a8: Pull complete
02c923a6e8c9: Pull complete
b7ba05ccb2c4: Pull complete
56334e624848: Download complete
dc443079bbcf: Pull complete
56334e624848: Pull complete
Digest: sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431
Status: Downloaded newer image for quay.io/kubevirt/pr-helper@sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431
quay.io/kubevirt/pr-helper@sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431

### Image pre-pull

quay.io/kubevirt/pr-helper@sha256:4418ea6ed69607a840bac924edaa2dc9d9d02d7ffb562f8b6e191e940f799431 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/sidecar-shim@sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9

exit=0
quay.io/kubevirt/sidecar-shim@sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9: Pulling from kubevirt/sidecar-shim
abb8916d9329: Pulling fs layer
83d76cd407c4: Pulling fs layer
abb8916d9329: Download complete
83d76cd407c4: Download complete
abb8916d9329: Pull complete
83d76cd407c4: Pull complete
Digest: sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9
Status: Downloaded newer image for quay.io/kubevirt/sidecar-shim@sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9
quay.io/kubevirt/sidecar-shim@sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9

### Image pre-pull

quay.io/kubevirt/sidecar-shim@sha256:eef056a96876dbe21f946262c5033e9cd858b5a8142bc1b9a819a18b84e4c0f9 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-api@sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281

exit=0
quay.io/kubevirt/virt-api@sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281: Pulling from kubevirt/virt-api
6a92b24e60ef: Pulling fs layer
dc443079bbcf: Pulling fs layer
dc443079bbcf: Already exists
dc443079bbcf: Pull complete
6a92b24e60ef: Download complete
6a92b24e60ef: Pull complete
Digest: sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281
Status: Downloaded newer image for quay.io/kubevirt/virt-api@sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281
quay.io/kubevirt/virt-api@sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281

### Image pre-pull

quay.io/kubevirt/virt-api@sha256:2d8e32c7fda8f455c13696da6f3a65362f5870acc81313b37d0e88c42666a281 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-controller@sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba

exit=0
quay.io/kubevirt/virt-controller@sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba: Pulling from kubevirt/virt-controller
71977fccd4b8: Pulling fs layer
71977fccd4b8: Download complete
71977fccd4b8: Pull complete
Digest: sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba
Status: Downloaded newer image for quay.io/kubevirt/virt-controller@sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba
quay.io/kubevirt/virt-controller@sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba

### Image pre-pull

quay.io/kubevirt/virt-controller@sha256:0dfc2f54e4a3dc5d3fd390a1ac3fe68dc70086cfac600ab0c59f6570cd4f8bba verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-exportproxy@sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b

exit=0
quay.io/kubevirt/virt-exportproxy@sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b: Pulling from kubevirt/virt-exportproxy
af5b75df2d9c: Pulling fs layer
af5b75df2d9c: Download complete
af5b75df2d9c: Pull complete
Digest: sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b
Status: Downloaded newer image for quay.io/kubevirt/virt-exportproxy@sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b
quay.io/kubevirt/virt-exportproxy@sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b

### Image pre-pull

quay.io/kubevirt/virt-exportproxy@sha256:ba6873f5f5a2db48efb4ca5e5375528f22144b096a2246387319625afe2cc83b verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-exportserver@sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66

exit=0
quay.io/kubevirt/virt-exportserver@sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66: Pulling from kubevirt/virt-exportserver
06bb09173643: Pulling fs layer
ee6a9cf9481a: Pulling fs layer
35b1a2212849: Pulling fs layer
dc443079bbcf: Pulling fs layer
dc443079bbcf: Already exists
35b1a2212849: Download complete
06bb09173643: Download complete
ee6a9cf9481a: Download complete
ee6a9cf9481a: Pull complete
35b1a2212849: Pull complete
dc443079bbcf: Pull complete
06bb09173643: Pull complete
Digest: sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66
Status: Downloaded newer image for quay.io/kubevirt/virt-exportserver@sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66
quay.io/kubevirt/virt-exportserver@sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66

### Image pre-pull

quay.io/kubevirt/virt-exportserver@sha256:79cdfd7cc0924503370ae832a1684e135cfbb7b2fac7a5073007f60223cb4b66 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-handler@sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125

exit=0
quay.io/kubevirt/virt-handler@sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125: Pulling from kubevirt/virt-handler
91f695749a5c: Pulling fs layer
61d445807ed2: Pulling fs layer
28d8d17eabb4: Pulling fs layer
32c8be458041: Pulling fs layer
dc443079bbcf: Pulling fs layer
19ad511b3be0: Pulling fs layer
dc443079bbcf: Already exists
28d8d17eabb4: Download complete
32c8be458041: Download complete
19ad511b3be0: Download complete
91f695749a5c: Download complete
61d445807ed2: Download complete
61d445807ed2: Pull complete
dc443079bbcf: Pull complete
28d8d17eabb4: Pull complete
32c8be458041: Pull complete
19ad511b3be0: Pull complete
91f695749a5c: Pull complete
Digest: sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125
Status: Downloaded newer image for quay.io/kubevirt/virt-handler@sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125
quay.io/kubevirt/virt-handler@sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125

### Image pre-pull

quay.io/kubevirt/virt-handler@sha256:7906c9115575bc2b497ad71a3cc4de73be699d9c93a830bca775a3bdf38a9125 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4

exit=0
quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4: Pulling from kubevirt/virt-launcher
ae620fda1b6b: Pulling fs layer
32c8be458041: Pulling fs layer
9a18bb3e768e: Pulling fs layer
dc443079bbcf: Pulling fs layer
d147ec5e9632: Pulling fs layer
4fd1722252ad: Pulling fs layer
173d58520f97: Pulling fs layer
1eea4e62c081: Pulling fs layer
58161cddf08d: Pulling fs layer
f324b01a50e5: Pulling fs layer
32c8be458041: Already exists
dc443079bbcf: Already exists
d147ec5e9632: Download complete
1eea4e62c081: Download complete
173d58520f97: Download complete
f324b01a50e5: Download complete
58161cddf08d: Download complete
4fd1722252ad: Download complete
ae620fda1b6b: Download complete
9a18bb3e768e: Download complete
9a18bb3e768e: Pull complete
dc443079bbcf: Pull complete
d147ec5e9632: Pull complete
58161cddf08d: Pull complete
32c8be458041: Pull complete
173d58520f97: Pull complete
4fd1722252ad: Pull complete
1eea4e62c081: Pull complete
f324b01a50e5: Pull complete
ae620fda1b6b: Pull complete
Digest: sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4
Status: Downloaded newer image for quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4
quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4

### Image pre-pull

quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

exit=0
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074: Pulling from kubevirt/virt-operator
b53f87b0c51c: Pulling fs layer
b53f87b0c51c: Download complete
b53f87b0c51c: Pull complete
Digest: sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074
Status: Downloaded newer image for quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

### Image pre-pull

quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/virt-synchronization-controller@sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a

exit=0
quay.io/kubevirt/virt-synchronization-controller@sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a: Pulling from kubevirt/virt-synchronization-controller
4473b0736f76: Pulling fs layer
4473b0736f76: Download complete
4473b0736f76: Pull complete
Digest: sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a
Status: Downloaded newer image for quay.io/kubevirt/virt-synchronization-controller@sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a
quay.io/kubevirt/virt-synchronization-controller@sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a

### Image pre-pull

quay.io/kubevirt/virt-synchronization-controller@sha256:14aedc915943b528b5b92e68781a96348c36b045ad2a61493747c70abdaee67a verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-apiserver@sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126

exit=0
quay.io/kubevirt/cdi-apiserver@sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126: Pulling from kubevirt/cdi-apiserver
6cacbb0fe506: Pulling fs layer
dd1577e06f11: Pulling fs layer
6cacbb0fe506: Download complete
dd1577e06f11: Download complete
dd1577e06f11: Pull complete
6cacbb0fe506: Pull complete
Digest: sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126
Status: Downloaded newer image for quay.io/kubevirt/cdi-apiserver@sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126
quay.io/kubevirt/cdi-apiserver@sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126

### Image pre-pull

quay.io/kubevirt/cdi-apiserver@sha256:ed33049a6d4df69f57aae63ee3ce99a86fbe540ff0aaf6c6c6b1b08797465126 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-cloner@sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0

exit=0
quay.io/kubevirt/cdi-cloner@sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0: Pulling from kubevirt/cdi-cloner
e699d6ca6914: Pulling fs layer
e699d6ca6914: Download complete
e699d6ca6914: Pull complete
Digest: sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0
Status: Downloaded newer image for quay.io/kubevirt/cdi-cloner@sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0
quay.io/kubevirt/cdi-cloner@sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0

### Image pre-pull

quay.io/kubevirt/cdi-cloner@sha256:3e081676f2be46d6e4a13bc6ba41eadc6540f053e075a2aaddf82214b650dde0 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-controller@sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6

exit=0
quay.io/kubevirt/cdi-controller@sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6: Pulling from kubevirt/cdi-controller
37ae07516de2: Pulling fs layer
37ae07516de2: Download complete
37ae07516de2: Pull complete
Digest: sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6
Status: Downloaded newer image for quay.io/kubevirt/cdi-controller@sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6
quay.io/kubevirt/cdi-controller@sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6

### Image pre-pull

quay.io/kubevirt/cdi-controller@sha256:4cd26b1ea5d42c58f1eafec0232fff00386ef8d6f35d06c5f96b45864f574ff6 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae

exit=0
quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae: Pulling from kubevirt/cdi-importer
656d375ed3c7: Pulling fs layer
b67dd65e0032: Pulling fs layer
b67dd65e0032: Download complete
b67dd65e0032: Pull complete
656d375ed3c7: Download complete
656d375ed3c7: Pull complete
Digest: sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae
Status: Downloaded newer image for quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae
quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae

### Image pre-pull

quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

exit=0
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b: Pulling from kubevirt/cdi-operator
ea5f4123c698: Pulling fs layer
ea5f4123c698: Download complete
ea5f4123c698: Pull complete
Digest: sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b
Status: Downloaded newer image for quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### Image pre-pull

quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-uploadproxy@sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8

exit=0
quay.io/kubevirt/cdi-uploadproxy@sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8: Pulling from kubevirt/cdi-uploadproxy
8b04aa47f669: Pulling fs layer
8b04aa47f669: Download complete
8b04aa47f669: Pull complete
Digest: sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8
Status: Downloaded newer image for quay.io/kubevirt/cdi-uploadproxy@sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8
quay.io/kubevirt/cdi-uploadproxy@sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8

### Image pre-pull

quay.io/kubevirt/cdi-uploadproxy@sha256:87226bef5eb8aa89f58305950a267b5587e899cd68bc453ca0a6f510fed3ffb8 verified by Docker pull --platform linux/arm64

### $ docker pull --platform linux/arm64 quay.io/kubevirt/cdi-uploadserver@sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d

exit=0
quay.io/kubevirt/cdi-uploadserver@sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d: Pulling from kubevirt/cdi-uploadserver
e2453a08ee61: Pulling fs layer
ed86d7bf6681: Pulling fs layer
e2453a08ee61: Download complete
ed86d7bf6681: Download complete
ed86d7bf6681: Pull complete
e2453a08ee61: Pull complete
Digest: sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d
Status: Downloaded newer image for quay.io/kubevirt/cdi-uploadserver@sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d
quay.io/kubevirt/cdi-uploadserver@sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d

### Image pre-pull

quay.io/kubevirt/cdi-uploadserver@sha256:86050ff4c84100ccfccde75e55f5405b66e1a7362ab492446745f18b242cd63d verified by Docker pull --platform linux/arm64

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle2754651283/001/kubevirt-base-3962664663.yaml

exit=0
namespace/kubevirt created
customresourcedefinition.apiextensions.k8s.io/kubevirts.kubevirt.io created
priorityclass.scheduling.k8s.io/kubevirt-cluster-critical created
clusterrole.rbac.authorization.k8s.io/kubevirt.io:operator created
serviceaccount/kubevirt-operator created
role.rbac.authorization.k8s.io/kubevirt-operator created
rolebinding.rbac.authorization.k8s.io/kubevirt-operator-rolebinding created
clusterrole.rbac.authorization.k8s.io/kubevirt-operator created
clusterrolebinding.rbac.authorization.k8s.io/kubevirt-operator created
Warning: spec.template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].key: node-role.kubernetes.io/master is use "node-role.kubernetes.io/control-plane" instead
deployment.apps/virt-operator created

### $ kubectl --context orbstack wait --for=condition=Established crd/kubevirts.kubevirt.io --timeout=180s

exit=0
customresourcedefinition.apiextensions.k8s.io/kubevirts.kubevirt.io condition met

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle2754651283/003/cdi-base-2001557498.yaml

exit=0
namespace/cdi created
customresourcedefinition.apiextensions.k8s.io/cdis.cdi.kubevirt.io created
clusterrole.rbac.authorization.k8s.io/cdi-operator-cluster created
clusterrolebinding.rbac.authorization.k8s.io/cdi-operator created
serviceaccount/cdi-operator created
role.rbac.authorization.k8s.io/cdi-operator created
rolebinding.rbac.authorization.k8s.io/cdi-operator created
deployment.apps/cdi-operator created

### $ kubectl --context orbstack wait --for=condition=Established crd/cdis.cdi.kubevirt.io --timeout=180s

exit=0
customresourcedefinition.apiextensions.k8s.io/cdis.cdi.kubevirt.io condition met

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle2754651283/002/kubevirt-cr-3374865813.yaml

exit=0
kubevirt.kubevirt.io/kubevirt created

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle2754651283/004/cdi-cr-143917476.yaml

exit=0
cdi.cdi.kubevirt.io/cdi created

### $ kubectl --context orbstack -n kubevirt rollout status deployment/virt-operator --timeout=10m

exit=0
Waiting for deployment "virt-operator" rollout to finish: 0 of 2 updated replicas are available...
Waiting for deployment "virt-operator" rollout to finish: 1 of 2 updated replicas are available...
deployment "virt-operator" successfully rolled out

### $ kubectl --context orbstack -n cdi rollout status deployment/cdi-operator --timeout=10m

exit=0
deployment "cdi-operator" successfully rolled out

### $ kubectl --context orbstack -n kubevirt wait --for=condition=Available kubevirt/kubevirt --timeout=25m

exit=0
kubevirt.kubevirt.io/kubevirt condition met

### $ kubectl --context orbstack -n cdi wait --for=condition=Available cdi/cdi --timeout=25m

exit=0
cdi.cdi.kubevirt.io/cdi condition met

### $ kubectl --context orbstack -n kubevirt get kubevirt kubevirt -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "KubeVirt",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"KubeVirt\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirt\",\"namespace\":\"kubevirt\"},\"spec\":{\"configuration\":{\"developerConfiguration\":{\"useEmulation\":true},\"virtTemplateDeployment\":{\"enabled\":false}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "kubevirt.io/foregroundDeleteKubeVirt"
        ],
        "generation": 2,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "kubevirt",
        "namespace": "kubevirt",
        "resourceVersion": "72289",
        "uid": "f4973a31-161d-425b-bcfc-fbbf9d4107e1"
    },
    "spec": {
        "certificateRotateStrategy": {},
        "configuration": {
            "developerConfiguration": {
                "useEmulation": true
            },
            "virtTemplateDeployment": {
                "enabled": false
            }
        },
        "customizeComponents": {},
        "workloadUpdateStrategy": {}
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "True",
                "type": "Available"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Degraded"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": null,
                "message": "All resources were created.",
                "reason": "AllResourcesCreated",
                "status": "True",
                "type": "Created"
            }
        ],
        "defaultArchitecture": "arm64",
        "generations": [
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstances.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancepresets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancereplicasets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachines.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancemigrations.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshots.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshotcontents.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinerestores.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancetypes.instancetype.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {

...(truncated)

### $ kubectl --context orbstack -n cdi get cdi cdi -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "CDI",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/configAuthority": "",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"CDI\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"},\"spec\":{\"config\":{\"featureGates\":[\"HonorWaitForFirstConsumer\",\"WebhookPvcRendering\"]},\"imagePullPolicy\":\"IfNotPresent\",\"infra\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"},\"tolerations\":[{\"key\":\"CriticalAddonsOnly\",\"operator\":\"Exists\"}]},\"workload\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"}}}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "operator.cdi.kubevirt.io"
        ],
        "generation": 3,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "71350",
        "uid": "88997bf1-b017-4e8b-bd1a-7143eb16d457"
    },
    "spec": {
        "config": {
            "featureGates": [
                "HonorWaitForFirstConsumer",
                "WebhookPvcRendering"
            ]
        },
        "customizeComponents": {},
        "imagePullPolicy": "IfNotPresent",
        "infra": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            },
            "tolerations": [
                {
                    "key": "CriticalAddonsOnly",
                    "operator": "Exists"
                }
            ]
        },
        "workload": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "message": "Deployment Completed",
                "reason": "DeployCompleted",
                "status": "True",
                "type": "Available"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:08Z",
                "status": "False",
                "type": "Degraded"
            }
        ],
        "observedVersion": "v1.63.0",
        "operatorVersion": "v1.63.0",
        "phase": "Deployed",
        "targetVersion": "v1.63.0"
    }
}

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

---

### Attempt 2

- Run started: 2026-09-27T15:01:11Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 3s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "kubevirt.io/heartbeat": "2026-09-27T15:00:50Z",
                    "kubevirt.io/ksm-handler-managed": "false",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "cpumanager": "false",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "kubevirt.io/cpumanager": "false",
                    "kubevirt.io/ksm-enabled": "false",
                    "kubevirt.io/schedulable": "true",
                    "machine-type.node.kubevirt.io/virt": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.0.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.2.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.4.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.6.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.8.0": "true",
                    "nod
...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "53761",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### Pre-install inventory

crd/kubevirts.kubevirt.io present

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### Pre-install inventory

crd/cdis.cdi.kubevirt.io present

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/kubevirt present

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/cdi present

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Installation mode

reusing the complete Task 2.6 release after ownership labels matched; no existing cluster objects were applied or changed

### $ kubectl --context orbstack -n kubevirt get kubevirt kubevirt -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "KubeVirt",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"KubeVirt\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirt\",\"namespace\":\"kubevirt\"},\"spec\":{\"configuration\":{\"developerConfiguration\":{\"useEmulation\":true},\"virtTemplateDeployment\":{\"enabled\":false}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "kubevirt.io/foregroundDeleteKubeVirt"
        ],
        "generation": 2,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "kubevirt",
        "namespace": "kubevirt",
        "resourceVersion": "72303",
        "uid": "f4973a31-161d-425b-bcfc-fbbf9d4107e1"
    },
    "spec": {
        "certificateRotateStrategy": {},
        "configuration": {
            "developerConfiguration": {
                "useEmulation": true
            },
            "virtTemplateDeployment": {
                "enabled": false
            }
        },
        "customizeComponents": {},
        "workloadUpdateStrategy": {}
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "True",
                "type": "Available"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": "2026-09-27T14:56:58Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Degraded"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": null,
                "message": "All resources were created.",
                "reason": "AllResourcesCreated",
                "status": "True",
                "type": "Created"
            }
        ],
        "defaultArchitecture": "arm64",
        "generations": [
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstances.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancepresets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancereplicasets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachines.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancemigrations.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshots.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshotcontents.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinerestores.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancetypes.instancetype.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {

...(truncated)

### $ kubectl --context orbstack -n cdi get cdi cdi -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "CDI",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/configAuthority": "",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"CDI\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"},\"spec\":{\"config\":{\"featureGates\":[\"HonorWaitForFirstConsumer\",\"WebhookPvcRendering\"]},\"imagePullPolicy\":\"IfNotPresent\",\"infra\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"},\"tolerations\":[{\"key\":\"CriticalAddonsOnly\",\"operator\":\"Exists\"}]},\"workload\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"}}}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "operator.cdi.kubevirt.io"
        ],
        "generation": 3,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "71350",
        "uid": "88997bf1-b017-4e8b-bd1a-7143eb16d457"
    },
    "spec": {
        "config": {
            "featureGates": [
                "HonorWaitForFirstConsumer",
                "WebhookPvcRendering"
            ]
        },
        "customizeComponents": {},
        "imagePullPolicy": "IfNotPresent",
        "infra": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            },
            "tolerations": [
                {
                    "key": "CriticalAddonsOnly",
                    "operator": "Exists"
                }
            ]
        },
        "workload": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "message": "Deployment Completed",
                "reason": "DeployCompleted",
                "status": "True",
                "type": "Available"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:08Z",
                "status": "False",
                "type": "Degraded"
            }
        ],
        "observedVersion": "v1.63.0",
        "operatorVersion": "v1.63.0",
        "phase": "Deployed",
        "targetVersion": "v1.63.0"
    }
}

### $ kubectl --context orbstack -n kubevirt get deployment virt-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

### $ kubectl --context orbstack -n cdi get deployment cdi-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### Installed versions

KubeVirt.status.operatorVersion=v1.9.0 operatorImage=quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074; CDI.status.operatorVersion=v1.63.0 operatorImage=quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### $ kubectl --context orbstack get storageclass -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "storage.k8s.io/v1",
            "kind": "StorageClass",
            "metadata": {
                "annotations": {
                    "defaultVolumeType": "local",
                    "objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/4yRz47UMAyHXwX53JYpnamqSBxg0V4QEhJoObuJOzVN4ypxi0areXeUMqDhwJ9j8ov9xZ+fARd+ophYAhhIKhHPVE1dqlhebjUUMHFwYODTj+jBY0pQwEyKDhXBPAOGIIrKElI+Ohpw9fokfp3p82UhMODFoocCpP9KVhNpFVkqi6qeMokz4i+5fAsUy/M2gYGpSXfJVhcv3nNwr984J+GfLQLOv/5T3sb9r6K0oM2V09pTmS5JaYbipzCbrVQ5ioGUdnmcypuJco/BgMaV4FqAx5787upP3BHTCAbqrhmak21Pw9Db5tAe20MzHJuhPnUH19m2w1cOe3fMTX+bbEEd8+USZeO8XIpgIGKwI8UMuHtWQMwD8PxRPNsLGHhHnjRr2fYdvuXgOJw/iMuAL8j6KPGRY9IHCWmdKcL1ewAAAP//KQ1Ko0kCAAA",
                    "objectset.rio.cattle.io/id": "",
                    "objectset.rio.cattle.io/owner-gvk": "k3s.cattle.io/v1, Kind=Addon",
                    "objectset.rio.cattle.io/owner-name": "local-storage",
                    "objectset.rio.cattle.io/owner-namespace": "kube-system",
                    "storageclass.kubernetes.io/is-default-class": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:23Z",
                "labels": {
                    "objectset.rio.cattle.io/hash": "183f35c65ffbc3064603f43f1580d8c68a2dabd4"
                },
                "name": "local-path",
                "resourceVersion": "284",
                "uid": "23b126cb-6924-476a-be45-3bf087e57c75"
            },
            "provisioner": "rancher.io/local-path",
            "reclaimPolicy": "Delete",
            "volumeBindingMode": "WaitForFirstConsumer"
        }
    ],
    "kind": "List",
    "metadata": {
        "resourceVersion": ""
    }
}

---

### Attempt 3

- Status: INTERRUPTED during diagnosis; not a passing PoC.
- Command: `OPS_KUBEVIRT_ORBSTACK_POC=1 go test ./test/e2e -run '^TestKubeVirtPOCArtifactsAndOrbStackLifecycle$' -count=1 -timeout=70m -v`
- Result: interrupted with SIGINT after 70.221s after observing the bound volume dependency; no VM or VMI was created.
- Evidence: `kubectl get events -n ops-sp02-kubevirt-poc --sort-by=.lastTimestamp` reported `WaitForFirstConsumer` for the PVC and the CDI DataVolume phase remained `WaitForFirstConsumer`; this OrbStack `local-path` StorageClass requires a consumer before binding.
- Cleanup: `kubectl delete namespace ops-sp02-kubevirt-poc --wait=true --timeout=5m` succeeded after verifying namespace label `ops.platform.io/release=sp02-task-2-6`; a follow-up `kubectl get namespace ops-sp02-kubevirt-poc` returned NotFound. Only this release-labeled temporary namespace, PVC, and DataVolume were removed.
- Correction: the E2E now creates the VM before waiting for DataVolume `Succeeded`, matching the observed `WaitForFirstConsumer` behavior.

---

### Attempt 4

- Run started: 2026-09-28T00:20:55Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 32s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "kubevirt.io/heartbeat": "2026-09-28T00:20:18Z",
                    "kubevirt.io/ksm-handler-managed": "false",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "cpumanager": "false",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "kubevirt.io/cpumanager": "false",
                    "kubevirt.io/ksm-enabled": "false",
                    "kubevirt.io/schedulable": "true",
                    "machine-type.node.kubevirt.io/virt": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.0.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.2.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.4.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.6.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.8.0": "true",
                    "nod
...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "73898",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### Pre-install inventory

crd/kubevirts.kubevirt.io present

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### Pre-install inventory

crd/cdis.cdi.kubevirt.io present

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/kubevirt present

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/cdi present

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Installation mode

reusing the complete Task 2.6 release after ownership labels matched; no existing cluster objects were applied or changed

### $ kubectl --context orbstack -n kubevirt get kubevirt kubevirt -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "KubeVirt",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"KubeVirt\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirt\",\"namespace\":\"kubevirt\"},\"spec\":{\"configuration\":{\"developerConfiguration\":{\"useEmulation\":true},\"virtTemplateDeployment\":{\"enabled\":false}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "kubevirt.io/foregroundDeleteKubeVirt"
        ],
        "generation": 2,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "kubevirt",
        "namespace": "kubevirt",
        "resourceVersion": "74152",
        "uid": "f4973a31-161d-425b-bcfc-fbbf9d4107e1"
    },
    "spec": {
        "certificateRotateStrategy": {},
        "configuration": {
            "developerConfiguration": {
                "useEmulation": true
            },
            "virtTemplateDeployment": {
                "enabled": false
            }
        },
        "customizeComponents": {},
        "workloadUpdateStrategy": {}
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "True",
                "type": "Available"
            },
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Degraded"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": null,
                "message": "All resources were created.",
                "reason": "AllResourcesCreated",
                "status": "True",
                "type": "Created"
            }
        ],
        "defaultArchitecture": "arm64",
        "generations": [
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstances.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancepresets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancereplicasets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachines.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancemigrations.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshots.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshotcontents.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinerestores.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancetypes.instancetype.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {

...(truncated)

### $ kubectl --context orbstack -n cdi get cdi cdi -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "CDI",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/configAuthority": "",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"CDI\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"},\"spec\":{\"config\":{\"featureGates\":[\"HonorWaitForFirstConsumer\",\"WebhookPvcRendering\"]},\"imagePullPolicy\":\"IfNotPresent\",\"infra\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"},\"tolerations\":[{\"key\":\"CriticalAddonsOnly\",\"operator\":\"Exists\"}]},\"workload\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"}}}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "operator.cdi.kubevirt.io"
        ],
        "generation": 3,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "71350",
        "uid": "88997bf1-b017-4e8b-bd1a-7143eb16d457"
    },
    "spec": {
        "config": {
            "featureGates": [
                "HonorWaitForFirstConsumer",
                "WebhookPvcRendering"
            ]
        },
        "customizeComponents": {},
        "imagePullPolicy": "IfNotPresent",
        "infra": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            },
            "tolerations": [
                {
                    "key": "CriticalAddonsOnly",
                    "operator": "Exists"
                }
            ]
        },
        "workload": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "message": "Deployment Completed",
                "reason": "DeployCompleted",
                "status": "True",
                "type": "Available"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:08Z",
                "status": "False",
                "type": "Degraded"
            }
        ],
        "observedVersion": "v1.63.0",
        "operatorVersion": "v1.63.0",
        "phase": "Deployed",
        "targetVersion": "v1.63.0"
    }
}

### $ kubectl --context orbstack -n kubevirt get deployment virt-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

### $ kubectl --context orbstack -n cdi get deployment cdi-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### Installed versions

KubeVirt.status.operatorVersion=v1.9.0 operatorImage=quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074; CDI.status.operatorVersion=v1.63.0 operatorImage=quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### $ kubectl --context orbstack get storageclass -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "storage.k8s.io/v1",
            "kind": "StorageClass",
            "metadata": {
                "annotations": {
                    "defaultVolumeType": "local",
                    "objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/4yRz47UMAyHXwX53JYpnamqSBxg0V4QEhJoObuJOzVN4ypxi0areXeUMqDhwJ9j8ov9xZ+fARd+ophYAhhIKhHPVE1dqlhebjUUMHFwYODTj+jBY0pQwEyKDhXBPAOGIIrKElI+Ohpw9fokfp3p82UhMODFoocCpP9KVhNpFVkqi6qeMokz4i+5fAsUy/M2gYGpSXfJVhcv3nNwr984J+GfLQLOv/5T3sb9r6K0oM2V09pTmS5JaYbipzCbrVQ5ioGUdnmcypuJco/BgMaV4FqAx5787upP3BHTCAbqrhmak21Pw9Db5tAe20MzHJuhPnUH19m2w1cOe3fMTX+bbEEd8+USZeO8XIpgIGKwI8UMuHtWQMwD8PxRPNsLGHhHnjRr2fYdvuXgOJw/iMuAL8j6KPGRY9IHCWmdKcL1ewAAAP//KQ1Ko0kCAAA",
                    "objectset.rio.cattle.io/id": "",
                    "objectset.rio.cattle.io/owner-gvk": "k3s.cattle.io/v1, Kind=Addon",
                    "objectset.rio.cattle.io/owner-name": "local-storage",
                    "objectset.rio.cattle.io/owner-namespace": "kube-system",
                    "storageclass.kubernetes.io/is-default-class": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:23Z",
                "labels": {
                    "objectset.rio.cattle.io/hash": "183f35c65ffbc3064603f43f1580d8c68a2dabd4"
                },
                "name": "local-path",
                "resourceVersion": "284",
                "uid": "23b126cb-6924-476a-be45-3bf087e57c75"
            },
            "provisioner": "rancher.io/local-path",
            "reclaimPolicy": "Delete",
            "volumeBindingMode": "WaitForFirstConsumer"
        }
    ],
    "kind": "List",
    "metadata": {
        "resourceVersion": ""
    }
}

### StorageClass

default=local-path

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle3665171665/001/kubevirt-poc-namespace-3985253858.yaml

exit=0
namespace/ops-sp02-kubevirt-poc created

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle3665171665/002/kubevirt-poc-datavolume-320962801.yaml

error=exit status 1
error: error parsing /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle3665171665/002/kubevirt-poc-datavolume-320962801.yaml: error converting YAML to JSON: yaml: line 19: found a tab character that violates indentation

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"ops-sp02-kubevirt-poc\"}}\n"
        },
        "creationTimestamp": "2026-09-28T00:20:58Z",
        "labels": {
            "kubernetes.io/metadata.name": "ops-sp02-kubevirt-poc",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "ops-sp02-kubevirt-poc",
        "resourceVersion": "74509",
        "uid": "d429b6c0-a6c1-4b47-8f4f-05c13b33459d"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack delete namespace ops-sp02-kubevirt-poc --wait=true --timeout=5m

exit=0
namespace "ops-sp02-kubevirt-poc" deleted

### Cleanup

deleted release-labeled test namespace after interrupted or failed PoC

---

### Attempt 5

- Run started: 2026-09-28T00:27:13Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 1s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "kubevirt.io/heartbeat": "2026-09-28T00:26:21Z",
                    "kubevirt.io/ksm-handler-managed": "false",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "cpumanager": "false",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "kubevirt.io/cpumanager": "false",
                    "kubevirt.io/ksm-enabled": "false",
                    "kubevirt.io/schedulable": "true",
                    "machine-type.node.kubevirt.io/virt": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.0.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.2.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.4.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.6.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.8.0": "true",
                    "nod
...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "73898",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### Pre-install inventory

crd/kubevirts.kubevirt.io present

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### Pre-install inventory

crd/cdis.cdi.kubevirt.io present

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/kubevirt present

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/cdi present

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"ops-sp02-kubevirt-poc\"}}\n"
        },
        "creationTimestamp": "2026-09-28T00:22:05Z",
        "deletionTimestamp": "2026-09-28T00:26:13Z",
        "labels": {
            "kubernetes.io/metadata.name": "ops-sp02-kubevirt-poc",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "ops-sp02-kubevirt-poc",
        "resourceVersion": "75786",
        "uid": "e8c524dd-c09c-445f-8b7e-e3d923d7f2be"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "conditions": [
            {
                "lastTransitionTime": "2026-09-28T00:26:18Z",
                "message": "All resources successfully discovered",
                "reason": "ResourcesDiscovered",
                "status": "False",
                "type": "NamespaceDeletionDiscoveryFailure"
            },
            {
                "lastTransitionTime": "2026-09-28T00:26:18Z",
                "message": "All legacy kube types successfully parsed",
                "reason": "ParsedGroupVersions",
                "status": "False",
                "type": "NamespaceDeletionGroupVersionParsingFailure"
            },
            {
                "lastTransitionTime": "2026-09-28T00:26:18Z",
                "message": "All content successfully deleted, may be waiting on finalization",
                "reason": "ContentDeleted",
                "status": "False",
                "type": "NamespaceDeletionContentFailure"
            },
            {
                "lastTransitionTime": "2026-09-28T00:27:11Z",
                "message": "Some resources are remaining: virtualmachines.kubevirt.io has 1 resource instances",
                "reason": "SomeResourcesRemain",
                "status": "True",
                "type": "NamespaceContentRemaining"
            },
            {
                "lastTransitionTime": "2026-09-28T00:27:11Z",
                "message": "Some content in the namespace has finalizers remaining: kubevirt.io/virtualMachineControllerFinalize in 1 resource instances",
                "reason": "SomeFinalizersRemain",
                "status": "True",
                "type": "NamespaceFinalizersRemaining"
            }
        ],
        "phase": "Terminating"
    }
}

---

### Attempt 6

- Run started: 2026-09-28T00:28:53Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 1m10s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "kubevirt.io/heartbeat": "2026-09-28T00:28:00Z",
                    "kubevirt.io/ksm-handler-managed": "false",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "cpumanager": "false",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "kubevirt.io/cpumanager": "false",
                    "kubevirt.io/ksm-enabled": "false",
                    "kubevirt.io/schedulable": "true",
                    "machine-type.node.kubevirt.io/virt": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.0.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.2.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.4.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.6.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.8.0": "true",
                    "nod
...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "73898",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### Pre-install inventory

crd/kubevirts.kubevirt.io present

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### Pre-install inventory

crd/cdis.cdi.kubevirt.io present

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/kubevirt present

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/cdi present

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Installation mode

reusing the complete Task 2.6 release after ownership labels matched; no existing cluster objects were applied or changed

### $ kubectl --context orbstack -n kubevirt get kubevirt kubevirt -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "KubeVirt",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"KubeVirt\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirt\",\"namespace\":\"kubevirt\"},\"spec\":{\"configuration\":{\"developerConfiguration\":{\"useEmulation\":true},\"virtTemplateDeployment\":{\"enabled\":false}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "kubevirt.io/foregroundDeleteKubeVirt"
        ],
        "generation": 2,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "kubevirt",
        "namespace": "kubevirt",
        "resourceVersion": "74152",
        "uid": "f4973a31-161d-425b-bcfc-fbbf9d4107e1"
    },
    "spec": {
        "certificateRotateStrategy": {},
        "configuration": {
            "developerConfiguration": {
                "useEmulation": true
            },
            "virtTemplateDeployment": {
                "enabled": false
            }
        },
        "customizeComponents": {},
        "workloadUpdateStrategy": {}
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "True",
                "type": "Available"
            },
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastProbeTime": "2026-09-28T00:18:55Z",
                "lastTransitionTime": "2026-09-28T00:18:55Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Degraded"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": null,
                "message": "All resources were created.",
                "reason": "AllResourcesCreated",
                "status": "True",
                "type": "Created"
            }
        ],
        "defaultArchitecture": "arm64",
        "generations": [
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstances.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancepresets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancereplicasets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachines.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancemigrations.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshots.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshotcontents.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinerestores.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancetypes.instancetype.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {

...(truncated)

### $ kubectl --context orbstack -n cdi get cdi cdi -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "CDI",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/configAuthority": "",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"CDI\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"},\"spec\":{\"config\":{\"featureGates\":[\"HonorWaitForFirstConsumer\",\"WebhookPvcRendering\"]},\"imagePullPolicy\":\"IfNotPresent\",\"infra\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"},\"tolerations\":[{\"key\":\"CriticalAddonsOnly\",\"operator\":\"Exists\"}]},\"workload\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"}}}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "operator.cdi.kubevirt.io"
        ],
        "generation": 3,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "71350",
        "uid": "88997bf1-b017-4e8b-bd1a-7143eb16d457"
    },
    "spec": {
        "config": {
            "featureGates": [
                "HonorWaitForFirstConsumer",
                "WebhookPvcRendering"
            ]
        },
        "customizeComponents": {},
        "imagePullPolicy": "IfNotPresent",
        "infra": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            },
            "tolerations": [
                {
                    "key": "CriticalAddonsOnly",
                    "operator": "Exists"
                }
            ]
        },
        "workload": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "message": "Deployment Completed",
                "reason": "DeployCompleted",
                "status": "True",
                "type": "Available"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:08Z",
                "status": "False",
                "type": "Degraded"
            }
        ],
        "observedVersion": "v1.63.0",
        "operatorVersion": "v1.63.0",
        "phase": "Deployed",
        "targetVersion": "v1.63.0"
    }
}

### $ kubectl --context orbstack -n kubevirt get deployment virt-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

### $ kubectl --context orbstack -n cdi get deployment cdi-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### Installed versions

KubeVirt.status.operatorVersion=v1.9.0 operatorImage=quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074; CDI.status.operatorVersion=v1.63.0 operatorImage=quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### $ kubectl --context orbstack get storageclass -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "storage.k8s.io/v1",
            "kind": "StorageClass",
            "metadata": {
                "annotations": {
                    "defaultVolumeType": "local",
                    "objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/4yRz47UMAyHXwX53JYpnamqSBxg0V4QEhJoObuJOzVN4ypxi0areXeUMqDhwJ9j8ov9xZ+fARd+ophYAhhIKhHPVE1dqlhebjUUMHFwYODTj+jBY0pQwEyKDhXBPAOGIIrKElI+Ohpw9fokfp3p82UhMODFoocCpP9KVhNpFVkqi6qeMokz4i+5fAsUy/M2gYGpSXfJVhcv3nNwr984J+GfLQLOv/5T3sb9r6K0oM2V09pTmS5JaYbipzCbrVQ5ioGUdnmcypuJco/BgMaV4FqAx5787upP3BHTCAbqrhmak21Pw9Db5tAe20MzHJuhPnUH19m2w1cOe3fMTX+bbEEd8+USZeO8XIpgIGKwI8UMuHtWQMwD8PxRPNsLGHhHnjRr2fYdvuXgOJw/iMuAL8j6KPGRY9IHCWmdKcL1ewAAAP//KQ1Ko0kCAAA",
                    "objectset.rio.cattle.io/id": "",
                    "objectset.rio.cattle.io/owner-gvk": "k3s.cattle.io/v1, Kind=Addon",
                    "objectset.rio.cattle.io/owner-name": "local-storage",
                    "objectset.rio.cattle.io/owner-namespace": "kube-system",
                    "storageclass.kubernetes.io/is-default-class": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:23Z",
                "labels": {
                    "objectset.rio.cattle.io/hash": "183f35c65ffbc3064603f43f1580d8c68a2dabd4"
                },
                "name": "local-path",
                "resourceVersion": "284",
                "uid": "23b126cb-6924-476a-be45-3bf087e57c75"
            },
            "provisioner": "rancher.io/local-path",
            "reclaimPolicy": "Delete",
            "volumeBindingMode": "WaitForFirstConsumer"
        }
    ],
    "kind": "List",
    "metadata": {
        "resourceVersion": ""
    }
}

### StorageClass

default=local-path

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle515845433/001/kubevirt-poc-namespace-2663008717.yaml

exit=0
namespace/ops-sp02-kubevirt-poc created

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle515845433/002/kubevirt-poc-datavolume-460295297.yaml

exit=0
datavolume.cdi.kubevirt.io/sp02-blank-volume created

### DataVolume binding

the observed default StorageClass uses WaitForFirstConsumer; create the consuming VM before waiting for DataVolume Succeeded

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle515845433/003/kubevirt-poc-vm-1408596865.yaml

exit=0
Warning: spec.running is deprecated, please use spec.runStrategy instead.
virtualmachine.kubevirt.io/sp02-lifecycle-vm created

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76091",
        "uid": "61eb070a-8bf0-43e1-96a0-41331fbe73c1"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "message": "PVC sp02-blank-volume Pending",
                "reason": "Pending",
                "status": "False",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "WaitForFirstConsumer",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=WaitForFirstConsumer

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76138",
        "uid": "61eb070a-8bf0-43e1-96a0-41331fbe73c1"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T00:29:00Z",
                "lastTransitionTime": "2026-09-28T00:29:00Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "PVCBound",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=PVCBound

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76198",
        "uid": "61eb070a-8bf0-43e1-96a0-41331fbe73c1"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T00:29:00Z",
                "lastTransitionTime": "2026-09-28T00:29:00Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:28:56Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:29:05Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "reason": "ContainerCreating",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "ImportScheduled",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=ImportScheduled

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76221",
        "uid": "61eb070a-8bf0-43e1-96a0-41331fbe73c1"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T00:29:00Z",
                "lastTransitionTime": "2026-09-28T00:29:00Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:29:09Z",
                "lastTransitionTime": "2026-09-28T00:29:09Z",
                "status": "True",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:29:07Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "message": "Import Complete",
                "reason": "Completed",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "Succeeded",
        "progress": "100.0%"
    }
}

### Lifecycle observed

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=Succeeded

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachineInstance",
    "metadata": {
        "annotations": {
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/pci-topology-version": "v3",
            "kubevirt.io/storage-observed-api-version": "v1",
            "kubevirt.io/vm-generation": "1"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize",
            "kubevirt.io/foregroundDeleteVirtualMachine"
        ],
        "generation": 7,
        "labels": {
            "kubevirt.io/domain": "sp02-lifecycle-vm",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "ownerReferences": [
            {
                "apiVersion": "kubevirt.io/v1",
                "blockOwnerDeletion": true,
                "controller": true,
                "kind": "VirtualMachine",
                "name": "sp02-lifecycle-vm",
                "uid": "80d083d9-5bdc-4d00-9886-7e3c4c7c66e8"
            }
        ],
        "resourceVersion": "76233",
        "uid": "0f6ded94-2aaf-480a-aebb-6ce4534486a1"
    },
    "spec": {
        "architecture": "arm64",
        "domain": {
            "cpu": {
                "cores": 1,
                "model": "host-passthrough"
            },
            "devices": {
                "disks": [
                    {
                        "disk": {
                            "bus": "virtio"
                        },
                        "name": "rootdisk"
                    }
                ],
                "interfaces": [
                    {
                        "bridge": {},
                        "name": "default"
                    }
                ]
            },
            "features": {
                "acpi": {
                    "enabled": true
                }
            },
            "firmware": {
                "bootloader": {
                    "efi": {
                        "secureBoot": false
                    }
                },
                "serial": "304fbb62-90d7-4db6-8849-17fed0c5a8e2",
                "uuid": "d6903137-52b2-4983-80a5-1e53d30ade6d"
            },
            "machine": {
                "type": "virt"
            },
            "memory": {
                "guest": "512Mi"
            },
            "resources": {
                "requests": {
                    "memory": "512Mi"
                }
            }
        },
        "evictionStrategy": "None",
        "networks": [
            {
                "name": "default",
                "pod": {}
            }
        ],
        "volumes": [
            {
                "dataVolume": {
                    "name": "sp02-blank-volume"
                },
                "name": "rootdisk"
            }
        ]
    },
    "status": {
        "activePods": {
            "be23f5e7-b336-4eeb-8df4-dfb519079b38": "orbstack"
        },
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T00:29:09Z",
                "lastTransitionTime": "2026-09-28T00:29:09Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            }
        ],
        "currentCPUTopology": {
            "cores": 1
        },
        "guestOSInfo": {},
        "launcherContainerImageVersion": "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4",
        "memory": {
            "guestAtBoot": "512Mi",
            "guestCurrent": "512Mi",
            "guestRequested": "512Mi",
            "memoryOverhead": "397Mi"
        },
        "phase": "Scheduling",
        "phaseTransitionTimestamps": [
            {
                "phase": "Pending",
                "phaseTransitionTimestamp": "2026-09-28T00:28:56Z"
            },
            {
                "phase": "Scheduling",
                "phaseTransitionTimestamp": "2026-09-28T00:29:09Z"
            }
        ],
        "qosClass": "Burstable",
        "runtimeUser": 107,
        "virtualMachineRevisionName": "revision-start-vm-80d083d9-5bdc-4d00-9886-7e3c4c7c66e8-1"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json status.phase=Scheduling

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachineInstance",
    "metadata": {
        "annotations": {
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/pci-topology-version": "v3",
            "kubevirt.io/storage-observed-api-version": "v1",
            "kubevirt.io/vm-generation": "1"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize",
            "kubevirt.io/foregroundDeleteVirtualMachine"
        ],
        "generation": 11,
        "labels": {
            "kubevirt.io/domain": "sp02-lifecycle-vm",
            "kubevirt.io/nodeName": "orbstack",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "ownerReferences": [
            {
                "apiVersion": "kubevirt.io/v1",
                "blockOwnerDeletion": true,
                "controller": true,
                "kind": "VirtualMachine",
                "name": "sp02-lifecycle-vm",
                "uid": "80d083d9-5bdc-4d00-9886-7e3c4c7c66e8"
            }
        ],
        "resourceVersion": "76268",
        "uid": "0f6ded94-2aaf-480a-aebb-6ce4534486a1"
    },
    "spec": {
        "architecture": "arm64",
        "domain": {
            "cpu": {
                "cores": 1,
                "model": "host-passthrough"
            },
            "devices": {
                "disks": [
                    {
                        "disk": {
                            "bus": "virtio"
                        },
                        "name": "rootdisk"
                    }
                ],
                "interfaces": [
                    {
                        "bridge": {},
                        "name": "default"
                    }
                ]
            },
            "features": {
                "acpi": {
                    "enabled": true
                }
            },
            "firmware": {
                "bootloader": {
                    "efi": {
                        "secureBoot": false
                    }
                },
                "serial": "304fbb62-90d7-4db6-8849-17fed0c5a8e2",
                "uuid": "d6903137-52b2-4983-80a5-1e53d30ade6d"
            },
            "machine": {
                "type": "virt"
            },
            "memory": {
                "guest": "512Mi"
            },
            "resources": {
                "requests": {
                    "memory": "512Mi"
                }
            }
        },
        "evictionStrategy": "None",
        "networks": [
            {
                "name": "default",
                "pod": {}
            }
        ],
        "volumes": [
            {
                "dataVolume": {
                    "name": "sp02-blank-volume"
                },
                "name": "rootdisk"
            }
        ]
    },
    "status": {
        "activePods": {
            "be23f5e7-b336-4eeb-8df4-dfb519079b38": "orbstack"
        },
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T00:29:09Z",
                "lastTransitionTime": "2026-09-28T00:29:09Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "InterfaceNotLiveMigratable",
                "status": "False",
                "type": "LiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "InterfaceNotLiveMigratable: cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "NotMigratable",
                "status": "False",
                "type": "StorageLiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": "2026-09-28T00:29:15Z",
                "message": "failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0",
                "reason": "Synchronizing with the Domain failed.",
                "status": "False",
                "type": "Synchronized"
            }
        ],
        "currentCPUTopology": {
            "cores": 1
...(truncated)

### Lifecycle blocked

-n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json
condition Synchronized: failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get events --sort-by=.lastTimestamp

exit=0
LAST SEEN   TYPE      REASON                  OBJECT                                      MESSAGE
7s          Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-qtkr6 to orbstack
11s         Normal    Scheduled               pod/importer-sp02-blank-volume              Successfully assigned ops-sp02-kubevirt-poc/importer-sp02-blank-volume to orbstack
16s         Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-j28j8   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-j28j8 to orbstack
21s         Normal    SuccessfulCreate        virtualmachine/sp02-lifecycle-vm            Started the virtual machine by creating the new virtual machine instance sp02-lifecycle-vm
21s         Normal    WaitForFirstConsumer    persistentvolumeclaim/sp02-blank-volume     waiting for first consumer to be created before binding
21s         Normal    Pending                 datavolume/sp02-blank-volume                PVC sp02-blank-volume Pending
20s         Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-j28j8
20s         Normal    ExternalProvisioning    persistentvolumeclaim/sp02-blank-volume     Waiting for a volume to be created either by the external provisioner 'rancher.io/local-path' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered.
20s         Normal    Provisioning            persistentvolumeclaim/sp02-blank-volume     External provisioner is provisioning volume for claim "ops-sp02-kubevirt-poc/sp02-blank-volume"
17s         Normal    ProvisioningSucceeded   persistentvolumeclaim/sp02-blank-volume     Successfully provisioned volume pvc-78667a58-b228-47fd-9665-e19ffdcd349b
17s         Normal    Bound                   datavolume/sp02-blank-volume                PVC sp02-blank-volume Bound
16s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
15s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container started
15s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container created
15s         Warning   ImportTargetInUse       persistentvolumeclaim/sp02-blank-volume     pod ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-j28j8 using PersistentVolumeClaim sp02-blank-volume
14s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
14s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container created
13s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container started
12s         Normal    SuccessfulDelete        virtualmachineinstance/sp02-lifecycle-vm    Deleted WaitForFirstConsumer temporary pod virt-launcher-sp02-lifecycle-vm-j28j8
12s         Normal    Killing                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Stopping container guest-console-log
11s         Normal    Pulled                  pod/importer-sp02-blank-volume              Container image "quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae" already present on machine and can be accessed by the pod
11s         Normal    Created                 pod/importer-sp02-blank-volume              Container created
10s         Normal    Started                 pod/importer-sp02-blank-volume              Container started
10s         Warning   Completed               datavolume/sp02-blank-volume                Import Complete
8s          Normal    ImportSucceeded         persistentvolumeclaim/sp02-blank-volume     Import Successful
8s          Normal    ImportSucceeded         datavolume/sp02-blank-volume                Successfully imported into PVC sp02-blank-volume
8s          Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-qtkr6
7s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
6s          Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container created
6s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container started
5s          Norma
...(truncated)

### Lifecycle warning events

LAST SEEN   TYPE      REASON                  OBJECT                                      MESSAGE
7s          Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-qtkr6 to orbstack
11s         Normal    Scheduled               pod/importer-sp02-blank-volume              Successfully assigned ops-sp02-kubevirt-poc/importer-sp02-blank-volume to orbstack
16s         Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-j28j8   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-j28j8 to orbstack
21s         Normal    SuccessfulCreate        virtualmachine/sp02-lifecycle-vm            Started the virtual machine by creating the new virtual machine instance sp02-lifecycle-vm
21s         Normal    WaitForFirstConsumer    persistentvolumeclaim/sp02-blank-volume     waiting for first consumer to be created before binding
21s         Normal    Pending                 datavolume/sp02-blank-volume                PVC sp02-blank-volume Pending
20s         Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-j28j8
20s         Normal    ExternalProvisioning    persistentvolumeclaim/sp02-blank-volume     Waiting for a volume to be created either by the external provisioner 'rancher.io/local-path' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered.
20s         Normal    Provisioning            persistentvolumeclaim/sp02-blank-volume     External provisioner is provisioning volume for claim "ops-sp02-kubevirt-poc/sp02-blank-volume"
17s         Normal    ProvisioningSucceeded   persistentvolumeclaim/sp02-blank-volume     Successfully provisioned volume pvc-78667a58-b228-47fd-9665-e19ffdcd349b
17s         Normal    Bound                   datavolume/sp02-blank-volume                PVC sp02-blank-volume Bound
16s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
15s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container started
15s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container created
15s         Warning   ImportTargetInUse       persistentvolumeclaim/sp02-blank-volume     pod ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-j28j8 using PersistentVolumeClaim sp02-blank-volume
14s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
14s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container created
13s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Container started
12s         Normal    SuccessfulDelete        virtualmachineinstance/sp02-lifecycle-vm    Deleted WaitForFirstConsumer temporary pod virt-launcher-sp02-lifecycle-vm-j28j8
12s         Normal    Killing                 pod/virt-launcher-sp02-lifecycle-vm-j28j8   Stopping container guest-console-log
11s         Normal    Pulled                  pod/importer-sp02-blank-volume              Container image "quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae" already present on machine and can be accessed by the pod
11s         Normal    Created                 pod/importer-sp02-blank-volume              Container created
10s         Normal    Started                 pod/importer-sp02-blank-volume              Container started
10s         Warning   Completed               datavolume/sp02-blank-volume                Import Complete
8s          Normal    ImportSucceeded         persistentvolumeclaim/sp02-blank-volume     Import Successful
8s          Normal    ImportSucceeded         datavolume/sp02-blank-volume                Successfully imported into PVC sp02-blank-volume
8s          Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-qtkr6
7s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
6s          Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container created
6s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container started
5s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
4s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container started
4s          Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-qtkr6   Container created
2s          Warning   SyncFailed              virtualmachineinstance/sp02-lifecycle-vm    server error. command SyncVMI failed: "LibvirtError(Code=67, Domain=10, Message='unsupported configuration: CPU mode 'host-passthrough' for aarch64 qemu domain on aarch64 host is not supported by hypervisor')"
2s          Normal    Deleted                 virtualmachineinstance/sp02-lifecycle-vm    Signaled Deletion
1s          Warning   SyncFailed              virtualmachineinstance/sp02-lifecycle-vm    failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"ops-sp02-kubevirt-poc\"}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "labels": {
            "kubernetes.io/metadata.name": "ops-sp02-kubevirt-poc",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76083",
        "uid": "3e2af8c7-cec5-4aca-bc2b-6ee0715b405f"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get virtualmachine/sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachine",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"VirtualMachine\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-lifecycle-vm\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"running\":true,\"template\":{\"metadata\":{\"labels\":{\"kubevirt.io/domain\":\"sp02-lifecycle-vm\",\"ops.platform.io/release\":\"sp02-task-2-6\"}},\"spec\":{\"architecture\":\"arm64\",\"domain\":{\"cpu\":{\"cores\":1},\"devices\":{\"disks\":[{\"disk\":{\"bus\":\"virtio\"},\"name\":\"rootdisk\"}]},\"resources\":{\"requests\":{\"memory\":\"512Mi\"}}},\"volumes\":[{\"dataVolume\":{\"name\":\"sp02-blank-volume\"},\"name\":\"rootdisk\"}]}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize"
        ],
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76269",
        "uid": "80d083d9-5bdc-4d00-9886-7e3c4c7c66e8"
    },
    "spec": {
        "running": true,
        "template": {
            "metadata": {
                "annotations": {
                    "kubevirt.io/pci-topology-version": "v3"
                },
                "labels": {
                    "kubevirt.io/domain": "sp02-lifecycle-vm",
                    "ops.platform.io/release": "sp02-task-2-6"
                }
            },
            "spec": {
                "architecture": "arm64",
                "domain": {
                    "cpu": {
                        "cores": 1
                    },
                    "devices": {
                        "disks": [
                            {
                                "disk": {
                                    "bus": "virtio"
                                },
                                "name": "rootdisk"
                            }
                        ]
                    },
                    "firmware": {
                        "serial": "304fbb62-90d7-4db6-8849-17fed0c5a8e2",
                        "uuid": "d6903137-52b2-4983-80a5-1e53d30ade6d"
                    },
                    "machine": {
                        "type": "virt"
                    },
                    "resources": {
                        "requests": {
                            "memory": "512Mi"
                        }
                    }
                },
                "volumes": [
                    {
                        "dataVolume": {
                            "name": "sp02-blank-volume"
                        },
                        "name": "rootdisk"
                    }
                ]
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T00:29:09Z",
                "lastTransitionTime": "2026-09-28T00:29:09Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "InterfaceNotLiveMigratable",
                "status": "False",
                "type": "LiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "InterfaceNotLiveMigratable: cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "NotMigratable",
                "status": "False",
                "type": "StorageLiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": "2026-09-28T00:29:15Z",
                "message": "failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0",
                "reason": "Synchronizing with the Domain failed.",
                "status": "False",
                "type": "Synchronized"
            }
        ],
        "created": true,

...(truncated)

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc delete virtualmachine/sp02-lifecycle-vm --wait=true --timeout=90s

exit=0
virtualmachine.kubevirt.io "sp02-lifecycle-vm" deleted from ops-sp02-kubevirt-poc namespace

### Cleanup

deleted release-labeled virtualmachine/sp02-lifecycle-vm

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get virtualmachineinstance/sp02-lifecycle-vm -o json

error=exit status 1
Error from server (NotFound): virtualmachineinstances.kubevirt.io "sp02-lifecycle-vm" not found

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume/sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T00:28:56Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "76221",
        "uid": "61eb070a-8bf0-43e1-96a0-41331fbe73c1"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T00:29:00Z",
                "lastTransitionTime": "2026-09-28T00:29:00Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:29:09Z",
                "lastTransitionTime": "2026-09-28T00:29:09Z",
                "status": "True",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T00:29:07Z",
                "lastTransitionTime": "2026-09-28T00:28:56Z",
                "message": "Import Complete",
                "reason": "Completed",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "Succeeded",
        "progress": "100.0%"
    }
}

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc delete datavolume/sp02-blank-volume --wait=true --timeout=90s

exit=0
datavolume.cdi.kubevirt.io "sp02-blank-volume" deleted from ops-sp02-kubevirt-poc namespace

### Cleanup

deleted release-labeled datavolume/sp02-blank-volume

### $ kubectl --context orbstack delete namespace ops-sp02-kubevirt-poc --wait=true --timeout=5m

exit=0
namespace "ops-sp02-kubevirt-poc" deleted

### Cleanup

deleted release-labeled test namespace after interrupted or failed PoC

---

### Attempt 7

- Run started: 2026-09-28T01:43:52Z
- Status: FAILED; see command evidence below
- Context: `orbstack`
- Duration: 54s

### $ kubectl --context orbstack get --raw /version

exit=0
{
  "major": "1",
  "minor": "35",
  "emulationMajor": "1",
  "emulationMinor": "35",
  "minCompatibilityMajor": "1",
  "minCompatibilityMinor": "34",
  "gitVersion": "v1.35.6+orb1",
  "gitCommit": "6ffe43029a0c56bfffd91b24535b22c6e37ab9d5",
  "gitTreeState": "clean",
  "buildDate": "2026-07-31T07:54:36Z",
  "goVersion": "go1.25.11",
  "compiler": "gc",
  "platform": "linux/arm64"
}

### Compatibility decision

server=v1.35.6+orb1 platform=linux/arm64 commit=6ffe43029a0c56bfffd91b24535b22c6e37ab9d5 decision=supported kubevirt=1.9.0 cdi=1.63.0 source=sha256:0538f9446ffa897a02bdc917d0b130f5d11edbec40af978ce4257c1b61c4b26e

### $ kubectl --context orbstack get nodes -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Node",
            "metadata": {
                "annotations": {
                    "alpha.kubernetes.io/provided-node-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "flannel.alpha.coreos.com/backend-data": "null",
                    "flannel.alpha.coreos.com/backend-type": "host-gw",
                    "flannel.alpha.coreos.com/kube-subnet-manager": "true",
                    "flannel.alpha.coreos.com/public-ip": "192.168.139.2",
                    "k3s.io/hostname": "orbstack",
                    "k3s.io/internal-ip": "192.168.139.2,fd07:b51a:cc66::2",
                    "k3s.io/node-args": "[\"server\",\"--apiVersion\",\"kubelet.config.k8s.io/v1beta1\",\"--housekeepingInterval\",\"5m\",\"--kind\",\"KubeletConfiguration\",\"--volumeStatsAggPeriod\",\"5m\",\"--disable\",\"metrics-server,traefik,coredns\",\"--disable-helm-controller\",\"--https-listen-port\",\"26443\",\"--lb-server-port\",\"26444\",\"--docker\",\"--container-runtime-endpoint\",\"unix:///var/run/docker.sock.real\",\"--protect-kernel-defaults\",\"--flannel-backend\",\"host-gw\",\"--disable-network-policy\",\"--cluster-cidr\",\"192.168.194.0/25\",\"--service-cidr\",\"192.168.194.128/25\",\"--tls-san\",\"k8s.orb.local\",\"--tls-san\",\"docker.orb.local\",\"--write-kubeconfig\",\"/run/kubeconfig.yml\",\"--kube-apiserver-arg\",\"event-ttl=30m\",\"--kube-proxy-arg\",\"iptables-sync-period=5m\",\"--kube-proxy-arg\",\"iptables-min-sync-period=5s\",\"--kubelet-arg\",\"--allowed-unsafe-sysctls\",\"net.*\",\"--kubelet-arg\",\"--node-status-update-frequency\",\"1m\",\"--kubelet-arg\",\"--file-check-frequency\",\"1m\",\"--kubelet-arg\",\"--feature-gates\",\"PodAndContainerStatsFromCRI=true\",\"--kubelet-arg\",\"--config\",\"/etc/kubelet.conf\",\"--kube-controller-manager-arg\",\"controllers=*,tokencleaner,-validatingadmissionpolicy-status-controller\",\"--kube-controller-manager-arg\",\"node-cidr-mask-size-ipv4=25\",\"--kube-controller-manager-arg\",\"node-monitor-period=30s\",\"--kube-controller-manager-arg\",\"node-monitor-grace-period=3m\",\"--kube-controller-manager-arg\",\"pvclaimbinder-sync-period=5m\",\"--kube-controller-manager-arg\",\"concurrent-cron-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-daemonset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-deployment-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ephemeralvolume-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-gc-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-horizontal-pod-autoscaler-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-job-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-namespace-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-replicaset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-resource-quota-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-service-endpoint-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-serviceaccount-token-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-statefulset-syncs=1\",\"--kube-controller-manager-arg\",\"concurrent-ttl-after-finished-syncs=1\",\"--kube-controller-manager-arg\",\"mirroring-concurrent-service-endpoint-syncs=1\"]",
                    "k3s.io/node-config-hash": "L3TF3SR3PR4HBGS4B66JK55CLNXGJIKUF5F5VITISTEB3RN6PMBQ====",
                    "k3s.io/node-env": "{}",
                    "kubevirt.io/heartbeat": "2026-09-28T01:43:36Z",
                    "kubevirt.io/ksm-handler-managed": "false",
                    "node.alpha.kubernetes.io/ttl": "0",
                    "volumes.kubernetes.io/controller-managed-attach-detach": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:20Z",
                "finalizers": [
                    "wrangler.cattle.io/node"
                ],
                "labels": {
                    "beta.kubernetes.io/arch": "arm64",
                    "beta.kubernetes.io/instance-type": "k3s",
                    "beta.kubernetes.io/os": "linux",
                    "cpumanager": "false",
                    "kubernetes.io/arch": "arm64",
                    "kubernetes.io/hostname": "orbstack",
                    "kubernetes.io/os": "linux",
                    "kubevirt.io/cpumanager": "false",
                    "kubevirt.io/ksm-enabled": "false",
                    "kubevirt.io/schedulable": "true",
                    "machine-type.node.kubevirt.io/virt": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.0.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.2.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.4.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.6.0": "true",
                    "machine-type.node.kubevirt.io/virt-rhel9.8.0": "true",
                    "nod
...(truncated)

### Node

name=orbstack architecture=arm64

### $ kubectl --context orbstack -n monitoring get pods -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {
                "creationTimestamp": "2026-09-25T05:21:25Z",
                "generateName": "victoria-logs-85d7d5c6d6-",
                "generation": 1,
                "labels": {
                    "app": "victoria-logs",
                    "pod-template-hash": "85d7d5c6d6"
                },
                "name": "victoria-logs-85d7d5c6d6-kh4x9",
                "namespace": "monitoring",
                "ownerReferences": [
                    {
                        "apiVersion": "apps/v1",
                        "blockOwnerDeletion": true,
                        "controller": true,
                        "kind": "ReplicaSet",
                        "name": "victoria-logs-85d7d5c6d6",
                        "uid": "58de4790-d76c-4d8e-b49a-e754ee6fb125"
                    }
                ],
                "resourceVersion": "77159",
                "uid": "0758c194-d2d2-4785-9eff-e94baa3b02c8"
            },
            "spec": {
                "containers": [
                    {
                        "args": [
                            "-storageDataPath=/vlogs-data",
                            "-retentionPeriod=7d"
                        ],
                        "image": "victoriametrics/victoria-logs:v1.52.0",
                        "imagePullPolicy": "IfNotPresent",
                        "name": "victoria-logs",
                        "ports": [
                            {
                                "containerPort": 9428,
                                "name": "http",
                                "protocol": "TCP"
                            }
                        ],
                        "resources": {
                            "limits": {
                                "cpu": "1",
                                "memory": "2Gi"
                            },
                            "requests": {
                                "cpu": "200m",
                                "memory": "1Gi"
                            }
                        },
                        "terminationMessagePath": "/dev/termination-log",
                        "terminationMessagePolicy": "File",
                        "volumeMounts": [
                            {
                                "mountPath": "/vlogs-data",
                                "name": "data"
                            },
                            {
                                "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
                                "name": "kube-api-access-cm2pd",
                                "readOnly": true
                            }
                        ]
                    }
                ],
                "dnsPolicy": "ClusterFirst",
                "enableServiceLinks": true,
                "nodeName": "orbstack",
                "preemptionPolicy": "PreemptLowerPriority",
                "priority": 0,
                "restartPolicy": "Always",
                "schedulerName": "default-scheduler",
                "securityContext": {},
                "serviceAccount": "default",
                "serviceAccountName": "default",
                "terminationGracePeriodSeconds": 30,
                "tolerations": [
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/not-ready",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    },
                    {
                        "effect": "NoExecute",
                        "key": "node.kubernetes.io/unreachable",
                        "operator": "Exists",
                        "tolerationSeconds": 300
                    }
                ],
                "volumes": [
                    {
                        "emptyDir": {},
                        "name": "data"
                    },
                    {
                        "name": "kube-api-access-cm2pd",
                        "projected": {
                            "defaultMode": 420,
                            "sources": [
                                {
                                    "serviceAccountToken": {
                                        "expirationSeconds": 3607,
                                        "path": "token"
                                    }
                                },
                                {
                                    "configMap": {
                                        "items": [
                                            {
                                                "key": "ca.crt",
                                                "path": "ca.crt"
                                            }
                                        ],

...(truncated)

### $ kubectl --context orbstack -n monitoring exec vm-prometheus-node-exporter-gp8pq -- sh -c if test -c /host/root/dev/kvm; then echo present; else echo absent; fi

exit=0
absent

### KVM capability

/host/root/dev/kvm=absent; useEmulation=true

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### Pre-install inventory

crd/kubevirts.kubevirt.io present

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### Pre-install inventory

crd/cdis.cdi.kubevirt.io present

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/kubevirt present

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Pre-install inventory

namespace/cdi present

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

error=exit status 1
Error from server (NotFound): namespaces "ops-sp02-kubevirt-poc" not found

### $ kubectl --context orbstack get crd/kubevirts.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{},\"labels\":{\"operator.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirts.kubevirt.io\"},\"spec\":{\"group\":\"kubevirt.io\",\"names\":{\"categories\":[\"all\"],\"kind\":\"KubeVirt\",\"plural\":\"kubevirts\",\"shortNames\":[\"kv\",\"kvs\"],\"singular\":\"kubevirt\"},\"scope\":\"Namespaced\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"KubeVirt represents the object deploying all KubeVirt resources\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"properties\":{\"certificateRotateStrategy\":{\"properties\":{\"selfSigned\":{\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"caOverlapInterval\":{\"description\":\"Deprecated. Use CA.Duration and CA.RenewBefore instead\",\"type\":\"string\"},\"caRotateInterval\":{\"description\":\"Deprecated. Use CA.Duration instead\",\"type\":\"string\"},\"certRotateInterval\":{\"description\":\"Deprecated. Use Server.Duration instead\",\"type\":\"string\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's \\\"notAfter\\\"\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"configuration\":{\"description\":\"holds kubevirt configurations.\\nsame as the virt-configMap\",\"properties\":{\"additionalGuestMemoryOverheadRatio\":{\"description\":\"AdditionalGuestMemoryOverheadRatio can be used to increase the virtualization infrastructure\\noverhead. This is useful, since the calculation of this overhead is not accurate and cannot\\nbe entirely known in advance. The ratio that is being set determines by which factor to increase\\nthe overhead calculated by Kubevirt. A higher ratio means that the VMs would be less compromised\\nby node pressures, but would mean that fewer VMs could be scheduled to a node.\\nIf not set, the default is 1.\",\"type\":\"string\"},\"apiConfiguration\":{\"description\":\"ReloadableComponentConfiguration holds all generic k8s configuration options which can\\nbe reloaded by components without requiring a restart.\",\"properties\":{\"restClient\":{\"description\":\"RestClient can be used to tune certain aspects of the k8s client in use.\",\"properties\":{\"rateLimiter\":{\"description\":\"RateLimiter allows selecting and configuring different rate limiters for the k8s client.\",\"properties\":{\"tokenBucketRateLimiter\":{\"properties\":{\"burst\":{\"description\":\"Maximum burst for throttle.\\nIf it's zero, the component default will be used\",\"type\":\"integer\"},\"qps\":{\"description\":\"QPS indicates the maximum QPS to the apiserver from this client.\\nIf it's zero, the component default will be used\",\"type\":\"number\"}},\"required\":[\"burst\",\"qps\"],\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"}},\"type\":\"object\"},\"architectureConfiguration\":{\"properties\":{\"amd64\":{\"properties\":{\"emulatedMachines\":{\"items\":{\"type\":\"string\"},\"type\":\"array\",\"x-kubernetes-list-type\":\"atomic\"},\"machineType\":{\"type\":\"string\"},\"ovmfPath\":{\"type\":\"string\"}},\"type\":\"objec
...(truncated)

### $ kubectl --context orbstack get crd/cdis.cdi.kubevirt.io -o json

exit=0
{
    "apiVersion": "apiextensions.k8s.io/v1",
    "kind": "CustomResourceDefinition",
    "metadata": {
        "annotations": {
            "controller-gen.kubebuilder.io/version": "v0.14.0",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"apiextensions.k8s.io/v1\",\"kind\":\"CustomResourceDefinition\",\"metadata\":{\"annotations\":{\"controller-gen.kubebuilder.io/version\":\"v0.14.0\"},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdis.cdi.kubevirt.io\"},\"spec\":{\"group\":\"cdi.kubevirt.io\",\"names\":{\"kind\":\"CDI\",\"listKind\":\"CDIList\",\"plural\":\"cdis\",\"shortNames\":[\"cdi\",\"cdis\"],\"singular\":\"cdi\"},\"scope\":\"Cluster\",\"versions\":[{\"additionalPrinterColumns\":[{\"jsonPath\":\".metadata.creationTimestamp\",\"name\":\"Age\",\"type\":\"date\"},{\"jsonPath\":\".status.phase\",\"name\":\"Phase\",\"type\":\"string\"}],\"name\":\"v1alpha1\",\"schema\":{\"openAPIV3Schema\":{\"description\":\"CDI is the CDI Operator CRD\",\"properties\":{\"apiVersion\":{\"description\":\"APIVersion defines the versioned schema of this representation of an object.\\nServers should convert recognized schemas to the latest internal value, and\\nmay reject unrecognized values.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources\",\"type\":\"string\"},\"kind\":{\"description\":\"Kind is a string value representing the REST resource this object represents.\\nServers may infer this from the endpoint the client submits requests to.\\nCannot be updated.\\nIn CamelCase.\\nMore info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds\",\"type\":\"string\"},\"metadata\":{\"type\":\"object\"},\"spec\":{\"description\":\"CDISpec defines our specification for the CDI installation\",\"properties\":{\"certConfig\":{\"description\":\"certificate configuration\",\"properties\":{\"ca\":{\"description\":\"CA configuration\\nCA certs are kept in the CA bundle as long as they are valid\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"client\":{\"description\":\"Client configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"},\"server\":{\"description\":\"Server configuration\\nCerts are rotated and discarded\",\"properties\":{\"duration\":{\"description\":\"The requested 'duration' (i.e. lifetime) of the Certificate.\",\"type\":\"string\"},\"renewBefore\":{\"description\":\"The amount of time before the currently issued certificate's `notAfter`\\ntime that we will begin to attempt to renew the certificate.\",\"type\":\"string\"}},\"type\":\"object\"}},\"type\":\"object\"},\"cloneStrategyOverride\":{\"description\":\"Clone strategy override: should we use a host-assisted copy even if snapshots are available?\",\"enum\":[\"copy\",\"snapshot\",\"csi-clone\"],\"type\":\"string\"},\"config\":{\"description\":\"CDIConfig at CDI level\",\"properties\":{\"dataVolumeTTLSeconds\":{\"description\":\"DataVolumeTTLSeconds is the time in seconds after DataVolume completion it can be garbage collected. Disabled by default.\\nDeprecated: Removed in v1.62.\",\"format\":\"int32\",\"type\":\"integer\"},\"featureGates\":{\"description\":\"FeatureGates are a list of specific enabled feature gates\",\"items\":{\"type\":\"string\"},\"type\":\"array\"},\"filesystemOverhead\":{\"description\":\"FilesystemOverhead describes the space reserved for overhead when using Filesystem volumes. A value is between 0 and 1, if not defined it is 0.06 (6% overhead)\",\"properties\":{\"global\":{\"description\":\"Global is how much space of a Filesystem volume should be reserved for overhead. This value is used unless overridden by a more specific value (per storageClass)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"storageClass\":{\"additionalProperties\":{\"description\":\"Percent is a string that can only be a value between [0,1)\\n(Note: we actually rely on reconcile to reject invalid values)\",\"pattern\":\"^(0(?:\\\\.\\\\d{1,3})?|1)$\",\"type\":\"string\"},\"description\":\"StorageClass specifies how much space of a Filesystem volume should be reserved for safety. The keys are the storageClass and the values are the overhead. This value overrides the global value\",\"type\":\"object\"}},\"type\":\"object\"},\"imagePullSecrets\":{\"description\":\"The i
...(truncated)

### $ kubectl --context orbstack get namespace/kubevirt -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\",\"pod-security.kubernetes.io/enforce\":\"privileged\"},\"name\":\"kubevirt\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:03Z",
        "labels": {
            "kubernetes.io/metadata.name": "kubevirt",
            "kubevirt.io": "",
            "openshift.io/cluster-monitoring": "true",
            "ops.platform.io/release": "sp02-task-2-6",
            "pod-security.kubernetes.io/enforce": "privileged"
        },
        "name": "kubevirt",
        "resourceVersion": "71531",
        "uid": "054b3131-0459-4859-8c64-7a82bb8f87b8"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack get namespace/cdi -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"cdi.kubevirt.io\":\"\",\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:04Z",
        "labels": {
            "cdi.kubevirt.io": "",
            "kubernetes.io/metadata.name": "cdi",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "70818",
        "uid": "d1f78539-8ad8-446b-9183-e49332c4d347"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### Installation mode

reusing the complete Task 2.6 release after ownership labels matched; no existing cluster objects were applied or changed

### $ kubectl --context orbstack -n kubevirt get kubevirt kubevirt -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "KubeVirt",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"KubeVirt\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"kubevirt\",\"namespace\":\"kubevirt\"},\"spec\":{\"configuration\":{\"developerConfiguration\":{\"useEmulation\":true},\"virtTemplateDeployment\":{\"enabled\":false}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "kubevirt.io/foregroundDeleteKubeVirt"
        ],
        "generation": 2,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "kubevirt",
        "namespace": "kubevirt",
        "resourceVersion": "77328",
        "uid": "f4973a31-161d-425b-bcfc-fbbf9d4107e1"
    },
    "spec": {
        "certificateRotateStrategy": {},
        "configuration": {
            "developerConfiguration": {
                "useEmulation": true
            },
            "virtTemplateDeployment": {
                "enabled": false
            }
        },
        "customizeComponents": {},
        "workloadUpdateStrategy": {}
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T01:41:38Z",
                "lastTransitionTime": "2026-09-28T01:41:38Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "True",
                "type": "Available"
            },
            {
                "lastProbeTime": "2026-09-28T01:41:38Z",
                "lastTransitionTime": "2026-09-28T01:41:38Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastProbeTime": "2026-09-28T01:41:38Z",
                "lastTransitionTime": "2026-09-28T01:41:38Z",
                "message": "All components are ready.",
                "reason": "AllComponentsReady",
                "status": "False",
                "type": "Degraded"
            },
            {
                "lastProbeTime": "2026-09-27T14:56:58Z",
                "lastTransitionTime": null,
                "message": "All resources were created.",
                "reason": "AllResourcesCreated",
                "status": "True",
                "type": "Created"
            }
        ],
        "defaultArchitecture": "arm64",
        "generations": [
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstances.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancepresets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancereplicasets.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachines.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancemigrations.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshots.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinesnapshotcontents.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachinerestores.snapshot.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {
                "group": "apiextensions.k8s.io/v1",
                "lastGeneration": 1,
                "name": "virtualmachineinstancetypes.instancetype.kubevirt.io",
                "resource": "customresourcedefinitions"
            },
            {

...(truncated)

### $ kubectl --context orbstack -n cdi get cdi cdi -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "CDI",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/configAuthority": "",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"CDI\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"cdi\"},\"spec\":{\"config\":{\"featureGates\":[\"HonorWaitForFirstConsumer\",\"WebhookPvcRendering\"]},\"imagePullPolicy\":\"IfNotPresent\",\"infra\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"},\"tolerations\":[{\"key\":\"CriticalAddonsOnly\",\"operator\":\"Exists\"}]},\"workload\":{\"nodeSelector\":{\"kubernetes.io/os\":\"linux\"}}}}\n"
        },
        "creationTimestamp": "2026-09-27T14:53:07Z",
        "finalizers": [
            "operator.cdi.kubevirt.io"
        ],
        "generation": 3,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "cdi",
        "resourceVersion": "71350",
        "uid": "88997bf1-b017-4e8b-bd1a-7143eb16d457"
    },
    "spec": {
        "config": {
            "featureGates": [
                "HonorWaitForFirstConsumer",
                "WebhookPvcRendering"
            ]
        },
        "customizeComponents": {},
        "imagePullPolicy": "IfNotPresent",
        "infra": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            },
            "tolerations": [
                {
                    "key": "CriticalAddonsOnly",
                    "operator": "Exists"
                }
            ]
        },
        "workload": {
            "nodeSelector": {
                "kubernetes.io/os": "linux"
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "message": "Deployment Completed",
                "reason": "DeployCompleted",
                "status": "True",
                "type": "Available"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:32Z",
                "status": "False",
                "type": "Progressing"
            },
            {
                "lastHeartbeatTime": "2026-09-27T14:53:32Z",
                "lastTransitionTime": "2026-09-27T14:53:08Z",
                "status": "False",
                "type": "Degraded"
            }
        ],
        "observedVersion": "v1.63.0",
        "operatorVersion": "v1.63.0",
        "phase": "Deployed",
        "targetVersion": "v1.63.0"
    }
}

### $ kubectl --context orbstack -n kubevirt get deployment virt-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074

### $ kubectl --context orbstack -n cdi get deployment cdi-operator -o jsonpath={.spec.template.spec.containers[0].image}

exit=0
quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### Installed versions

KubeVirt.status.operatorVersion=v1.9.0 operatorImage=quay.io/kubevirt/virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074; CDI.status.operatorVersion=v1.63.0 operatorImage=quay.io/kubevirt/cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b

### VirtualizationCapability

{"kvm":false,"emulation":true,"kubevirtVersion":"v1.9.0","cdiVersion":"v1.63.0","architectures":["linux/arm64"]}

### $ kubectl --context orbstack get storageclass -o json

exit=0
{
    "apiVersion": "v1",
    "items": [
        {
            "apiVersion": "storage.k8s.io/v1",
            "kind": "StorageClass",
            "metadata": {
                "annotations": {
                    "defaultVolumeType": "local",
                    "objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/4yRz47UMAyHXwX53JYpnamqSBxg0V4QEhJoObuJOzVN4ypxi0areXeUMqDhwJ9j8ov9xZ+fARd+ophYAhhIKhHPVE1dqlhebjUUMHFwYODTj+jBY0pQwEyKDhXBPAOGIIrKElI+Ohpw9fokfp3p82UhMODFoocCpP9KVhNpFVkqi6qeMokz4i+5fAsUy/M2gYGpSXfJVhcv3nNwr984J+GfLQLOv/5T3sb9r6K0oM2V09pTmS5JaYbipzCbrVQ5ioGUdnmcypuJco/BgMaV4FqAx5787upP3BHTCAbqrhmak21Pw9Db5tAe20MzHJuhPnUH19m2w1cOe3fMTX+bbEEd8+USZeO8XIpgIGKwI8UMuHtWQMwD8PxRPNsLGHhHnjRr2fYdvuXgOJw/iMuAL8j6KPGRY9IHCWmdKcL1ewAAAP//KQ1Ko0kCAAA",
                    "objectset.rio.cattle.io/id": "",
                    "objectset.rio.cattle.io/owner-gvk": "k3s.cattle.io/v1, Kind=Addon",
                    "objectset.rio.cattle.io/owner-name": "local-storage",
                    "objectset.rio.cattle.io/owner-namespace": "kube-system",
                    "storageclass.kubernetes.io/is-default-class": "true"
                },
                "creationTimestamp": "2026-09-24T14:09:23Z",
                "labels": {
                    "objectset.rio.cattle.io/hash": "183f35c65ffbc3064603f43f1580d8c68a2dabd4"
                },
                "name": "local-path",
                "resourceVersion": "284",
                "uid": "23b126cb-6924-476a-be45-3bf087e57c75"
            },
            "provisioner": "rancher.io/local-path",
            "reclaimPolicy": "Delete",
            "volumeBindingMode": "WaitForFirstConsumer"
        }
    ],
    "kind": "List",
    "metadata": {
        "resourceVersion": ""
    }
}

### StorageClass

default=local-path

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle1078581903/001/kubevirt-poc-namespace-4011676320.yaml

exit=0
namespace/ops-sp02-kubevirt-poc created

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle1078581903/002/kubevirt-poc-datavolume-285330080.yaml

exit=0
datavolume.cdi.kubevirt.io/sp02-blank-volume created

### DataVolume binding

the observed default StorageClass uses WaitForFirstConsumer; create the consuming VM before waiting for DataVolume Succeeded

### $ kubectl --context orbstack apply -f /var/folders/71/5876xm8s6d37d5873yq80dc80000gn/T/TestKubeVirtPOCArtifactsAndOrbStackLifecycle1078581903/003/kubevirt-poc-vm-833935986.yaml

exit=0
Warning: spec.running is deprecated, please use spec.runStrategy instead.
virtualmachine.kubevirt.io/sp02-lifecycle-vm created

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77725",
        "uid": "2b3783df-852b-49aa-8a66-63ae0ad978cf"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "message": "PVC sp02-blank-volume Pending",
                "reason": "Pending",
                "status": "False",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "WaitForFirstConsumer",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=WaitForFirstConsumer

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77776",
        "uid": "2b3783df-852b-49aa-8a66-63ae0ad978cf"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T01:43:59Z",
                "lastTransitionTime": "2026-09-28T01:43:59Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "PVCBound",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=PVCBound

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77833",
        "uid": "2b3783df-852b-49aa-8a66-63ae0ad978cf"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T01:43:59Z",
                "lastTransitionTime": "2026-09-28T01:43:59Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:43:54Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:44:04Z",
                "lastTransitionTime": "2026-09-28T01:43:54Z",
                "reason": "ContainerCreating",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "ImportScheduled",
        "progress": "N/A"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=ImportScheduled

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77859",
        "uid": "2b3783df-852b-49aa-8a66-63ae0ad978cf"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T01:43:59Z",
                "lastTransitionTime": "2026-09-28T01:43:59Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:44:08Z",
                "lastTransitionTime": "2026-09-28T01:44:08Z",
                "status": "True",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:44:07Z",
                "lastTransitionTime": "2026-09-28T01:44:07Z",
                "message": "Import Complete",
                "reason": "Completed",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "Succeeded",
        "progress": "100.0%"
    }
}

### Lifecycle observed

-n ops-sp02-kubevirt-poc get datavolume sp02-blank-volume -o json status.phase=Succeeded

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachineInstance",
    "metadata": {
        "annotations": {
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/pci-topology-version": "v3",
            "kubevirt.io/storage-observed-api-version": "v1",
            "kubevirt.io/vm-generation": "1"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize",
            "kubevirt.io/foregroundDeleteVirtualMachine"
        ],
        "generation": 7,
        "labels": {
            "kubevirt.io/domain": "sp02-lifecycle-vm",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "ownerReferences": [
            {
                "apiVersion": "kubevirt.io/v1",
                "blockOwnerDeletion": true,
                "controller": true,
                "kind": "VirtualMachine",
                "name": "sp02-lifecycle-vm",
                "uid": "a5f46363-cf66-4b5b-aaa4-eacda3cfccce"
            }
        ],
        "resourceVersion": "77871",
        "uid": "572244c6-9f94-4628-a87f-8383e721a69c"
    },
    "spec": {
        "architecture": "arm64",
        "domain": {
            "cpu": {
                "cores": 1,
                "model": "host-passthrough"
            },
            "devices": {
                "disks": [
                    {
                        "disk": {
                            "bus": "virtio"
                        },
                        "name": "rootdisk"
                    }
                ],
                "interfaces": [
                    {
                        "bridge": {},
                        "name": "default"
                    }
                ]
            },
            "features": {
                "acpi": {
                    "enabled": true
                }
            },
            "firmware": {
                "bootloader": {
                    "efi": {
                        "secureBoot": false
                    }
                },
                "serial": "7847f430-c4ce-4d0b-847d-a7165d38dd55",
                "uuid": "a3a2f83a-9314-4f4d-ac39-15d46eea6098"
            },
            "machine": {
                "type": "virt"
            },
            "memory": {
                "guest": "512Mi"
            },
            "resources": {
                "requests": {
                    "memory": "512Mi"
                }
            }
        },
        "evictionStrategy": "None",
        "networks": [
            {
                "name": "default",
                "pod": {}
            }
        ],
        "volumes": [
            {
                "dataVolume": {
                    "name": "sp02-blank-volume"
                },
                "name": "rootdisk"
            }
        ]
    },
    "status": {
        "activePods": {
            "b52fd4e9-b86d-44e1-9cb2-06731347ced5": "orbstack"
        },
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T01:44:08Z",
                "lastTransitionTime": "2026-09-28T01:44:08Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            }
        ],
        "currentCPUTopology": {
            "cores": 1
        },
        "guestOSInfo": {},
        "launcherContainerImageVersion": "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4",
        "memory": {
            "guestAtBoot": "512Mi",
            "guestCurrent": "512Mi",
            "guestRequested": "512Mi",
            "memoryOverhead": "397Mi"
        },
        "phase": "Scheduling",
        "phaseTransitionTimestamps": [
            {
                "phase": "Pending",
                "phaseTransitionTimestamp": "2026-09-28T01:43:54Z"
            },
            {
                "phase": "Scheduling",
                "phaseTransitionTimestamp": "2026-09-28T01:44:08Z"
            }
        ],
        "qosClass": "Burstable",
        "runtimeUser": 107,
        "virtualMachineRevisionName": "revision-start-vm-a5f46363-cf66-4b5b-aaa4-eacda3cfccce-1"
    }
}

### Lifecycle pending

-n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json status.phase=Scheduling

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachineInstance",
    "metadata": {
        "annotations": {
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/pci-topology-version": "v3",
            "kubevirt.io/storage-observed-api-version": "v1",
            "kubevirt.io/vm-generation": "1"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize",
            "kubevirt.io/foregroundDeleteVirtualMachine"
        ],
        "generation": 11,
        "labels": {
            "kubevirt.io/domain": "sp02-lifecycle-vm",
            "kubevirt.io/nodeName": "orbstack",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "ownerReferences": [
            {
                "apiVersion": "kubevirt.io/v1",
                "blockOwnerDeletion": true,
                "controller": true,
                "kind": "VirtualMachine",
                "name": "sp02-lifecycle-vm",
                "uid": "a5f46363-cf66-4b5b-aaa4-eacda3cfccce"
            }
        ],
        "resourceVersion": "77902",
        "uid": "572244c6-9f94-4628-a87f-8383e721a69c"
    },
    "spec": {
        "architecture": "arm64",
        "domain": {
            "cpu": {
                "cores": 1,
                "model": "host-passthrough"
            },
            "devices": {
                "disks": [
                    {
                        "disk": {
                            "bus": "virtio"
                        },
                        "name": "rootdisk"
                    }
                ],
                "interfaces": [
                    {
                        "bridge": {},
                        "name": "default"
                    }
                ]
            },
            "features": {
                "acpi": {
                    "enabled": true
                }
            },
            "firmware": {
                "bootloader": {
                    "efi": {
                        "secureBoot": false
                    }
                },
                "serial": "7847f430-c4ce-4d0b-847d-a7165d38dd55",
                "uuid": "a3a2f83a-9314-4f4d-ac39-15d46eea6098"
            },
            "machine": {
                "type": "virt"
            },
            "memory": {
                "guest": "512Mi"
            },
            "resources": {
                "requests": {
                    "memory": "512Mi"
                }
            }
        },
        "evictionStrategy": "None",
        "networks": [
            {
                "name": "default",
                "pod": {}
            }
        ],
        "volumes": [
            {
                "dataVolume": {
                    "name": "sp02-blank-volume"
                },
                "name": "rootdisk"
            }
        ]
    },
    "status": {
        "activePods": {
            "b52fd4e9-b86d-44e1-9cb2-06731347ced5": "orbstack"
        },
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T01:44:08Z",
                "lastTransitionTime": "2026-09-28T01:44:08Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "InterfaceNotLiveMigratable",
                "status": "False",
                "type": "LiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "InterfaceNotLiveMigratable: cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "NotMigratable",
                "status": "False",
                "type": "StorageLiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": "2026-09-28T01:44:12Z",
                "message": "failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0",
                "reason": "Synchronizing with the Domain failed.",
                "status": "False",
                "type": "Synchronized"
            }
        ],
        "currentCPUTopology": {
            "cores": 1
...(truncated)

### Lifecycle blocked

-n ops-sp02-kubevirt-poc get vmi sp02-lifecycle-vm -o json
condition Synchronized: failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get events --sort-by=.lastTimestamp

exit=0
LAST SEEN   TYPE      REASON                  OBJECT                                      MESSAGE
15s         Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-vszk9   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-vszk9 to orbstack
6s          Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-ctxxg to orbstack
10s         Normal    Scheduled               pod/importer-sp02-blank-volume              Successfully assigned ops-sp02-kubevirt-poc/importer-sp02-blank-volume to orbstack
21s         Normal    ExternalProvisioning    persistentvolumeclaim/sp02-blank-volume     Waiting for a volume to be created either by the external provisioner 'rancher.io/local-path' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered.
21s         Normal    SuccessfulCreate        virtualmachine/sp02-lifecycle-vm            Started the virtual machine by creating the new virtual machine instance sp02-lifecycle-vm
21s         Normal    Pending                 datavolume/sp02-blank-volume                PVC sp02-blank-volume Pending
21s         Normal    Provisioning            persistentvolumeclaim/sp02-blank-volume     External provisioner is provisioning volume for claim "ops-sp02-kubevirt-poc/sp02-blank-volume"
21s         Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-vszk9
21s         Normal    WaitForFirstConsumer    persistentvolumeclaim/sp02-blank-volume     waiting for first consumer to be created before binding
16s         Normal    ProvisioningSucceeded   persistentvolumeclaim/sp02-blank-volume     Successfully provisioned volume pvc-dd3d570f-0b22-4423-8929-f2194a413045
16s         Normal    Bound                   datavolume/sp02-blank-volume                PVC sp02-blank-volume Bound
15s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
14s         Warning   ImportTargetInUse       persistentvolumeclaim/sp02-blank-volume     pod ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-vszk9 using PersistentVolumeClaim sp02-blank-volume
14s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container created
14s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container started
13s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container started
13s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container created
13s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
12s         Normal    Killing                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Stopping container guest-console-log
12s         Normal    SuccessfulDelete        virtualmachineinstance/sp02-lifecycle-vm    Deleted WaitForFirstConsumer temporary pod virt-launcher-sp02-lifecycle-vm-vszk9
10s         Normal    Created                 pod/importer-sp02-blank-volume              Container created
10s         Normal    Pulled                  pod/importer-sp02-blank-volume              Container image "quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae" already present on machine and can be accessed by the pod
10s         Normal    Started                 pod/importer-sp02-blank-volume              Container started
9s          Normal    ImportInProgress        datavolume/sp02-blank-volume                Import into sp02-blank-volume in progress
8s          Warning   Completed               datavolume/sp02-blank-volume                Import Complete
7s          Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-ctxxg
7s          Normal    ImportSucceeded         datavolume/sp02-blank-volume                Successfully imported into PVC sp02-blank-volume
7s          Normal    ImportSucceeded         persistentvolumeclaim/sp02-blank-volume     Import Successful
6s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
5s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container s
...(truncated)

### Lifecycle warning events

LAST SEEN   TYPE      REASON                  OBJECT                                      MESSAGE
15s         Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-vszk9   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-vszk9 to orbstack
6s          Normal    Scheduled               pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Successfully assigned ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-ctxxg to orbstack
10s         Normal    Scheduled               pod/importer-sp02-blank-volume              Successfully assigned ops-sp02-kubevirt-poc/importer-sp02-blank-volume to orbstack
21s         Normal    ExternalProvisioning    persistentvolumeclaim/sp02-blank-volume     Waiting for a volume to be created either by the external provisioner 'rancher.io/local-path' or manually by the system administrator. If volume creation is delayed, please verify that the provisioner is running and correctly registered.
21s         Normal    SuccessfulCreate        virtualmachine/sp02-lifecycle-vm            Started the virtual machine by creating the new virtual machine instance sp02-lifecycle-vm
21s         Normal    Pending                 datavolume/sp02-blank-volume                PVC sp02-blank-volume Pending
21s         Normal    Provisioning            persistentvolumeclaim/sp02-blank-volume     External provisioner is provisioning volume for claim "ops-sp02-kubevirt-poc/sp02-blank-volume"
21s         Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-vszk9
21s         Normal    WaitForFirstConsumer    persistentvolumeclaim/sp02-blank-volume     waiting for first consumer to be created before binding
16s         Normal    ProvisioningSucceeded   persistentvolumeclaim/sp02-blank-volume     Successfully provisioned volume pvc-dd3d570f-0b22-4423-8929-f2194a413045
16s         Normal    Bound                   datavolume/sp02-blank-volume                PVC sp02-blank-volume Bound
15s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
14s         Warning   ImportTargetInUse       persistentvolumeclaim/sp02-blank-volume     pod ops-sp02-kubevirt-poc/virt-launcher-sp02-lifecycle-vm-vszk9 using PersistentVolumeClaim sp02-blank-volume
14s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container created
14s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container started
13s         Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container started
13s         Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container created
13s         Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-vszk9   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
12s         Normal    Killing                 pod/virt-launcher-sp02-lifecycle-vm-vszk9   Stopping container guest-console-log
12s         Normal    SuccessfulDelete        virtualmachineinstance/sp02-lifecycle-vm    Deleted WaitForFirstConsumer temporary pod virt-launcher-sp02-lifecycle-vm-vszk9
10s         Normal    Created                 pod/importer-sp02-blank-volume              Container created
10s         Normal    Pulled                  pod/importer-sp02-blank-volume              Container image "quay.io/kubevirt/cdi-importer@sha256:6bf3045ea156fb753a3380d402a6ec5dcaa35d72a1d1f259f8157733c5a97cae" already present on machine and can be accessed by the pod
10s         Normal    Started                 pod/importer-sp02-blank-volume              Container started
9s          Normal    ImportInProgress        datavolume/sp02-blank-volume                Import into sp02-blank-volume in progress
8s          Warning   Completed               datavolume/sp02-blank-volume                Import Complete
7s          Normal    SuccessfulCreate        virtualmachineinstance/sp02-lifecycle-vm    Created virtual machine pod virt-launcher-sp02-lifecycle-vm-ctxxg
7s          Normal    ImportSucceeded         datavolume/sp02-blank-volume                Successfully imported into PVC sp02-blank-volume
7s          Normal    ImportSucceeded         persistentvolumeclaim/sp02-blank-volume     Import Successful
6s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
5s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container started
5s          Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container created
4s          Normal    Pulled                  pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container image "quay.io/kubevirt/virt-launcher@sha256:b58f07a90ce6f7b72ea891af4cf8a1a6dbd7d97cb01b76f8372575ea81b339c4" already present on machine and can be accessed by the pod
4s          Normal    Created                 pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container created
4s          Normal    Started                 pod/virt-launcher-sp02-lifecycle-vm-ctxxg   Container started
3s          Normal    Deleted                 virtualmachineinstance/sp02-lifecycle-vm    Signaled Deletion
3s          Warning   SyncFailed              virtualmachineinstance/sp02-lifecycle-vm    server error. command SyncVMI failed: "LibvirtError(Code=67, Domain=10, Message='unsupported configuration: CPU mode 'host-passthrough' for aarch64 qemu domain on aarch64 host is not supported by hypervisor')"
1s          Warning   SyncFailed              virtualmachineinstance/sp02-lifecycle-vm    failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0

### $ kubectl --context orbstack get namespace ops-sp02-kubevirt-poc -o json

exit=0
{
    "apiVersion": "v1",
    "kind": "Namespace",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Namespace\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"ops-sp02-kubevirt-poc\"}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "labels": {
            "kubernetes.io/metadata.name": "ops-sp02-kubevirt-poc",
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77717",
        "uid": "acb85aa8-2f29-453a-99e3-a09ceb5a1dc4"
    },
    "spec": {
        "finalizers": [
            "kubernetes"
        ]
    },
    "status": {
        "phase": "Active"
    }
}

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get virtualmachine/sp02-lifecycle-vm -o json

exit=0
{
    "apiVersion": "kubevirt.io/v1",
    "kind": "VirtualMachine",
    "metadata": {
        "annotations": {
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"VirtualMachine\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-lifecycle-vm\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"running\":true,\"template\":{\"metadata\":{\"labels\":{\"kubevirt.io/domain\":\"sp02-lifecycle-vm\",\"ops.platform.io/release\":\"sp02-task-2-6\"}},\"spec\":{\"architecture\":\"arm64\",\"domain\":{\"cpu\":{\"cores\":1},\"devices\":{\"disks\":[{\"disk\":{\"bus\":\"virtio\"},\"name\":\"rootdisk\"}]},\"resources\":{\"requests\":{\"memory\":\"512Mi\"}}},\"volumes\":[{\"dataVolume\":{\"name\":\"sp02-blank-volume\"},\"name\":\"rootdisk\"}]}}}}\n",
            "kubevirt.io/latest-observed-api-version": "v1",
            "kubevirt.io/storage-observed-api-version": "v1"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "finalizers": [
            "kubevirt.io/virtualMachineControllerFinalize"
        ],
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-lifecycle-vm",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77903",
        "uid": "a5f46363-cf66-4b5b-aaa4-eacda3cfccce"
    },
    "spec": {
        "running": true,
        "template": {
            "metadata": {
                "annotations": {
                    "kubevirt.io/pci-topology-version": "v3"
                },
                "labels": {
                    "kubevirt.io/domain": "sp02-lifecycle-vm",
                    "ops.platform.io/release": "sp02-task-2-6"
                }
            },
            "spec": {
                "architecture": "arm64",
                "domain": {
                    "cpu": {
                        "cores": 1
                    },
                    "devices": {
                        "disks": [
                            {
                                "disk": {
                                    "bus": "virtio"
                                },
                                "name": "rootdisk"
                            }
                        ]
                    },
                    "firmware": {
                        "serial": "7847f430-c4ce-4d0b-847d-a7165d38dd55",
                        "uuid": "a3a2f83a-9314-4f4d-ac39-15d46eea6098"
                    },
                    "machine": {
                        "type": "virt"
                    },
                    "resources": {
                        "requests": {
                            "memory": "512Mi"
                        }
                    }
                },
                "volumes": [
                    {
                        "dataVolume": {
                            "name": "sp02-blank-volume"
                        },
                        "name": "rootdisk"
                    }
                ]
            }
        }
    },
    "status": {
        "conditions": [
            {
                "lastProbeTime": "2026-09-28T01:44:08Z",
                "lastTransitionTime": "2026-09-28T01:44:08Z",
                "message": "Guest VM is not reported as running",
                "reason": "GuestNotRunning",
                "status": "False",
                "type": "Ready"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "All of the VMI's DVs are bound and ready",
                "reason": "AllDVsReady",
                "status": "True",
                "type": "DataVolumesReady"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "InterfaceNotLiveMigratable",
                "status": "False",
                "type": "LiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": null,
                "message": "InterfaceNotLiveMigratable: cannot migrate VMI which does not use masquerade or a migratable plugin to connect to the pod network",
                "reason": "NotMigratable",
                "status": "False",
                "type": "StorageLiveMigratable"
            },
            {
                "lastProbeTime": null,
                "lastTransitionTime": "2026-09-28T01:44:12Z",
                "message": "failed to configure vmi network: setup failed, err: no gateway address found in routes for eth0",
                "reason": "Synchronizing with the Domain failed.",
                "status": "False",
                "type": "Synchronized"
            }
        ],
        "created": true,

...(truncated)

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc delete virtualmachine/sp02-lifecycle-vm --wait=true --timeout=90s

exit=0
virtualmachine.kubevirt.io "sp02-lifecycle-vm" deleted from ops-sp02-kubevirt-poc namespace

### Cleanup

deleted release-labeled virtualmachine/sp02-lifecycle-vm

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get virtualmachineinstance/sp02-lifecycle-vm -o json

error=exit status 1
Error from server (NotFound): virtualmachineinstances.kubevirt.io "sp02-lifecycle-vm" not found

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc get datavolume/sp02-blank-volume -o json

exit=0
{
    "apiVersion": "cdi.kubevirt.io/v1beta1",
    "kind": "DataVolume",
    "metadata": {
        "annotations": {
            "cdi.kubevirt.io/storage.usePopulator": "false",
            "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"cdi.kubevirt.io/v1beta1\",\"kind\":\"DataVolume\",\"metadata\":{\"annotations\":{},\"labels\":{\"ops.platform.io/release\":\"sp02-task-2-6\"},\"name\":\"sp02-blank-volume\",\"namespace\":\"ops-sp02-kubevirt-poc\"},\"spec\":{\"source\":{\"blank\":{}},\"storage\":{\"accessModes\":[\"ReadWriteOnce\"],\"resources\":{\"requests\":{\"storage\":\"1Gi\"}},\"storageClassName\":\"local-path\",\"volumeMode\":\"Filesystem\"}}}\n"
        },
        "creationTimestamp": "2026-09-28T01:43:54Z",
        "generation": 1,
        "labels": {
            "ops.platform.io/release": "sp02-task-2-6"
        },
        "name": "sp02-blank-volume",
        "namespace": "ops-sp02-kubevirt-poc",
        "resourceVersion": "77859",
        "uid": "2b3783df-852b-49aa-8a66-63ae0ad978cf"
    },
    "spec": {
        "source": {
            "blank": {}
        },
        "storage": {
            "accessModes": [
                "ReadWriteOnce"
            ],
            "resources": {
                "requests": {
                    "storage": "1Gi"
                }
            },
            "storageClassName": "local-path",
            "volumeMode": "Filesystem"
        }
    },
    "status": {
        "claimName": "sp02-blank-volume",
        "conditions": [
            {
                "lastHeartbeatTime": "2026-09-28T01:43:59Z",
                "lastTransitionTime": "2026-09-28T01:43:59Z",
                "message": "PVC sp02-blank-volume Bound",
                "reason": "Bound",
                "status": "True",
                "type": "Bound"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:44:08Z",
                "lastTransitionTime": "2026-09-28T01:44:08Z",
                "status": "True",
                "type": "Ready"
            },
            {
                "lastHeartbeatTime": "2026-09-28T01:44:07Z",
                "lastTransitionTime": "2026-09-28T01:44:07Z",
                "message": "Import Complete",
                "reason": "Completed",
                "status": "False",
                "type": "Running"
            }
        ],
        "phase": "Succeeded",
        "progress": "100.0%"
    }
}

### $ kubectl --context orbstack -n ops-sp02-kubevirt-poc delete datavolume/sp02-blank-volume --wait=true --timeout=90s

exit=0
datavolume.cdi.kubevirt.io "sp02-blank-volume" deleted from ops-sp02-kubevirt-poc namespace

### Cleanup

deleted release-labeled datavolume/sp02-blank-volume

### $ kubectl --context orbstack delete namespace ops-sp02-kubevirt-poc --wait=true --timeout=5m

exit=0
namespace "ops-sp02-kubevirt-poc" deleted

### Cleanup

deleted release-labeled test namespace after interrupted or failed PoC

## 2026-09-28 Deferred development and uninstall record

The user deferred further KubeVirt development for this work session. Keep KubeVirt and CDI as `candidate`; virtualization/full Profile runtime capability remains disabled or unverified. Do not treat this development cluster's support-matrix intersection as a production compatibility acceptance.

### Pre-uninstall inventory

- Current context was `orbstack`; Kubernetes Server was `v1.35.6+orb1`, `linux/arm64`.
- The `kubevirt` and `cdi` namespaces both carried `ops.platform.io/release=sp02-task-2-6`. The KubeVirt and CDI operator custom resources carried the same release label.
- A cluster-wide inventory found no VirtualMachine, VirtualMachineInstance, or DataVolume objects. The dedicated `ops-sp02-kubevirt-poc` namespace had already been deleted by the PoC cleanup.
- The only PVCs retained for the existing monitoring and platform workloads were `monitoring/vmsingle-vm` (Bound, 10Gi) and `ops-system/data-ops-openbao-0` (Bound, 2Gi). Neither was deleted.
- KubeVirt had created 65 default VirtualMachineClusterInstanceTypes and 54 default VirtualMachineClusterPreferences at `2026-09-27T14:56:57Z` / `14:56:58Z`. CDI had created one `CDIConfig` and one `StorageProfile` at `2026-09-27T14:53:22Z`. The 32 KubeVirt/CDI CRDs were created between `2026-09-27T14:53:04Z` and `14:53:26Z`, during this installation.

### Uninstall actions and result

The KubeVirt and CDI custom resources were deleted first, then the two release-labeled namespaces were removed. Namespace deletion completed without manual finalizer removal. The operators removed their generated default instance-type/preference objects and 30 operator-managed CRDs. The remaining release-labeled ClusterRoles, ClusterRoleBindings, and PriorityClass were removed; after confirming that no VM/VMI/DataVolume or other custom resource remained, the two bootstrap CRDs were deleted explicitly:

```text
kubectl delete kubevirt.kubevirt.io/kubevirt -n kubevirt --wait=true --timeout=3m  # exit 0
kubectl delete cdi.cdi.kubevirt.io/cdi --wait=true --timeout=3m                  # exit 0
kubectl delete namespace kubevirt cdi --wait=true --timeout=5m                   # exit 0
kubectl delete clusterroles,clusterrolebindings,priorityclasses \
  -l ops.platform.io/release=sp02-task-2-6 --wait=true --timeout=3m              # exit 0
kubectl delete crd kubevirts.kubevirt.io cdis.cdi.kubevirt.io \
  --wait=true --timeout=3m                                                       # exit 0
```

Post-uninstall checks returned no KubeVirt/CDI CRDs, `kubevirt`/`cdi` namespaces, or release-labeled ClusterRoles, ClusterRoleBindings, or PriorityClasses. The monitoring and OpenBao PVCs listed above remained Bound. No code or compatibility decision was changed as part of this uninstall.
