# PRD-55: resolve the EKS-optimized ACCELERATED (NVIDIA) AL2023 AMI id
# from AWS's public SSM parameter, and pin the GPU node class to that
# concrete id. Two reasons:
#   1. Reproducibility — a benchmark platform must not have node recycles
#      silently pull a newer AMI that shifts results. An SSM lookup
#      captures a concrete ami-... in the plan (reviewable), while still
#      being trivially re-resolved on a deliberate re-apply.
#   2. Correctness — Karpenter's `al2023@latest` alias resolves the
#      STANDARD AL2023 AMI, NOT the GPU/accelerated one. GPU nodes need
#      the accelerated AMI (bundled NVIDIA driver), so the alias was
#      effectively wrong for the gpu node class.
# Path format per AWS docs (retrieve-ami-id):
#   /aws/service/eks/optimized-ami/<ver>/amazon-linux-2023/<arch>/<type>/recommended/image_id
data "aws_ssm_parameter" "gpu_ami" {
  name = "/aws/service/eks/optimized-ami/${var.kubernetes_version}/amazon-linux-2023/x86_64/nvidia/recommended/image_id"
}

# PRD-65: SOCI PARALLEL-PULL mode, shared by the single-node gpu class AND the
# multi-node classes (defined once, can't drift). Speeds large-image pulls (the
# 8.9 GB llm-d-aws PP image took ~4m6s pre-SOCI); all GPU classes share the
# accelerated AL2023 AMI, which SHIPS the soci-snapshotter binary + service.
#
# The prior version only sed'd tuning knobs into config.toml — a NO-OP, verified
# live: the snapshotter wasn't running, containerd wasn't wired to it, and the
# master enable flag was never set (default false = lazy loading, which we do NOT
# want). This version does the three things AWS's docs require to actually turn on
# PARALLEL-PULL (awslabs/soci-snapshotter docs/parallel-mode.md + the EKS +
# DLAMI blogs):
#   1. write /etc/soci-snapshotter-grpc/config.toml with
#      [pull_modes.parallel_pull_unpack] enable = true + AWS's ECR-recommended
#      tuning (containerd content store);
#   2. wire containerd to use soci as the CRI snapshotter via a drop-in
#      (proxy_plugins.soci socket + snapshotter = "soci");
#   3. enable+restart soci-snapshotter then restart containerd, so the running
#      kubelet/containerd use SOCI for subsequent image pulls.
# Runs as a cloud-init shellscript MIME part (AL2023). Injected as a jsonencode()'d
# scalar on each EC2NodeClass userData (valid YAML, avoids block-scalar pitfalls).
locals {
  soci_user_data = <<-EOT
    MIME-Version: 1.0
    Content-Type: multipart/mixed; boundary="BOUNDARY"

    --BOUNDARY
    Content-Type: text/x-shellscript; charset="us-ascii"

    #!/bin/bash
    set -euo pipefail

    # 1. SOCI parallel-pull config (enable flag is the master switch; default is
    #    false = lazy loading). Tuning = AWS's recommended ECR defaults.
    mkdir -p /etc/soci-snapshotter-grpc
    cat > /etc/soci-snapshotter-grpc/config.toml <<'SOCI'
    [content_store]
    type = "containerd"

    [pull_modes.parallel_pull_unpack]
    enable = true
    max_concurrent_downloads = -1
    max_concurrent_downloads_per_image = 20
    concurrent_download_chunk_size = "16mb"
    max_concurrent_unpacks = -1
    max_concurrent_unpacks_per_image = 10
    discard_unpacked_layers = true
    SOCI

    # 2. Wire containerd to use the soci snapshotter for CRI (drop-in merged by
    #    nodeadm/containerd). proxy_plugins.soci points at the grpc socket.
    mkdir -p /etc/containerd/config.d
    cat > /etc/containerd/config.d/soci.toml <<'CTRD'
    version = 2
    [proxy_plugins.soci]
    type = "snapshot"
    address = "/run/soci-snapshotter-grpc/soci-snapshotter-grpc.sock"
    [plugins."io.containerd.grpc.v1.cri".containerd]
    snapshotter = "soci"
    disable_snapshot_annotations = false
    CTRD

    # 3. Start SOCI, then restart containerd so it picks up the drop-in. The EKS
    #    AL2023 AMI ships the soci-snapshotter service; enable+start it first.
    systemctl enable --now soci-snapshotter 2>/dev/null || systemctl enable --now soci-snapshotter-grpc 2>/dev/null || true
    systemctl restart containerd || true

    --BOUNDARY--
  EOT
}

# PRD-53: state migrations for resources that became counted.
# helm_release.karpenter_crd + helm_release.karpenter are gated on
# install_controller; time_sleep.wait_for_karpenter follows them;
# kubectl_manifest.nvidia_device_plugin gates on
# install_nvidia_device_plugin; aws_iam_role_policy.karpenter_node_ecr_pullthrough
# gates on manage_pull_through_cache.
moved {
  from = helm_release.karpenter_crd
  to   = helm_release.karpenter_crd[0]
}

moved {
  from = helm_release.karpenter
  to   = helm_release.karpenter[0]
}

moved {
  from = time_sleep.wait_for_karpenter
  to   = time_sleep.wait_for_karpenter[0]
}

moved {
  from = kubectl_manifest.nvidia_device_plugin
  to   = kubectl_manifest.nvidia_device_plugin[0]
}

moved {
  from = aws_iam_role_policy.karpenter_node_ecr_pullthrough
  to   = aws_iam_role_policy.karpenter_node_ecr_pullthrough[0]
}

module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "~> 21.0"

  cluster_name = var.cluster_name

  # v21: Pod Identity is the default (the `enable_pod_identity` and
  # `enable_v1_permissions` toggles were removed). We still opt into
  # creating the association between the Karpenter controller SA and role.
  create_pod_identity_association = true

  # The v21 controller policy exceeds the 6144-char MANAGED-policy limit
  # (apply failed with `LimitExceeded: Cannot exceed quota for PolicySize:
  # 6144`). Emit it as an INLINE role policy instead (10240-char limit) —
  # the module's own sanctioned fix for exactly this error.
  enable_inline_policy = true

  node_iam_role_additional_policies = {
    AmazonSSMManagedInstanceCore = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
  }

  tags = var.tags
}

resource "helm_release" "karpenter_crd" {
  count      = var.install_controller ? 1 : 0
  namespace  = "kube-system"
  name       = "karpenter-crd"
  repository = "oci://public.ecr.aws/karpenter"
  chart      = "karpenter-crd"
  version    = var.karpenter_version
  wait       = true
  timeout    = 300
}

resource "helm_release" "karpenter" {
  count         = var.install_controller ? 1 : 0
  namespace     = "kube-system"
  name          = "karpenter"
  repository    = "oci://public.ecr.aws/karpenter"
  chart         = "karpenter"
  version       = var.karpenter_version
  wait          = true
  wait_for_jobs = true
  timeout       = 600

  values = [
    <<-EOT
    settings:
      clusterName: ${var.cluster_name}
      clusterEndpoint: ${var.cluster_endpoint}
      interruptionQueue: ${module.karpenter.queue_name}
      # PRD-33: enables capacityReservationSelectorTerms on EC2NodeClass
      # and 'reserved' as a capacity-type. Still beta in Karpenter 1.9.
      # PRD-55: staticCapacity enables spec.replicas on NodePools (the
      # multi-node static pools). Off by default in 1.14 — without it the
      # controller silently ignores replicas and provisions nothing.
      featureGates:
        reservedCapacity: true
        staticCapacity: ${var.enable_multinode}
    EOT
  ]

  depends_on = [module.karpenter, helm_release.karpenter_crd]
}

# PRD-53: only wait on the controller install when we're doing it. In
# brownfield mode the operator-owned Karpenter is already running, so
# no wait is needed — NodePool apply races against nothing.
resource "time_sleep" "wait_for_karpenter" {
  count           = var.install_controller ? 1 : 0
  depends_on      = [helm_release.karpenter]
  create_duration = "30s"
}

resource "kubectl_manifest" "default_node_class" {
  yaml_body = <<-YAML
    apiVersion: karpenter.k8s.aws/v1
    kind: EC2NodeClass
    metadata:
      name: default
    spec:
      amiSelectorTerms:
        - alias: al2023@latest
      role: ${module.karpenter.node_iam_role_name}
      subnetSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      securityGroupSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      metadataOptions:
        httpEndpoint: enabled
        httpProtocolIPv6: disabled
        httpPutResponseHopLimit: 1
        httpTokens: required
      tags:
        NodeType: karpenter-node
        karpenter.sh/discovery: ${var.cluster_name}
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "general_purpose_node_pool" {
  yaml_body = <<-YAML
    apiVersion: karpenter.sh/v1
    kind: NodePool
    metadata:
      name: general-purpose
    spec:
      # PRD-53: weight=100 so Karpenter prefers this NodePool over
      # operator-owned NodePools with tied requirements on brownfield
      # clusters. Operator NodePools typically omit weight (defaults
      # to 0).
      weight: 100
      template:
        spec:
          requirements:
            - key: kubernetes.io/arch
              operator: In
              values: ["amd64"]
            - key: karpenter.k8s.aws/instance-family
              operator: In
              values: ["m6i"]
            - key: karpenter.sh/capacity-type
              operator: In
              values: ["on-demand"]
            - key: accelbench/node-type
              operator: In
              values: ["system"]
          # PRD-53: dedicated taint so AccelBench pods only land on our
          # nodes and operator pods never do. Our loadgen + cache-job
          # pods (see internal/manifest/templates/) apply a matching
          # toleration.
          taints:
            - key: accelbench.io/dedicated
              value: "true"
              effect: NoSchedule
          expireAfter: 720h
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: default
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmptyOrUnderutilized
        # Match the gpu/neuron pools so loadgen nodes reap fast after a
        # run finishes instead of sitting idle for the default 5m.
        consolidateAfter: 1m
        # Override Karpenter's default 10% budget so multiple empty
        # loadgen nodes can drain in parallel.
        budgets:
          - nodes: "100%"
  YAML

  depends_on = [kubectl_manifest.default_node_class]
}

resource "kubectl_manifest" "gpu_node_class" {
  yaml_body = <<-YAML
    apiVersion: karpenter.k8s.aws/v1
    kind: EC2NodeClass
    metadata:
      name: gpu
    spec:
      # Pinned to the EKS-optimized ACCELERATED (NVIDIA) AL2023 AMI id
      # resolved from SSM (see data.aws_ssm_parameter.gpu_ami above).
      # NOT `alias: al2023@latest`, which resolves the STANDARD AMI.
      # amiFamily is REQUIRED when amiSelectorTerms uses `id` rather than
      # `alias` (the alias implies the family); Karpenter rejects the
      # EC2NodeClass at reconcile time without it.
      amiFamily: AL2023
      amiSelectorTerms:
        - id: ${data.aws_ssm_parameter.gpu_ami.value}
      role: ${module.karpenter.node_iam_role_name}
      subnetSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      securityGroupSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      instanceStorePolicy: RAID0
      blockDeviceMappings:
        - deviceName: /dev/xvda
          ebs:
            volumeSize: 100Gi
            volumeType: gp3
            encrypted: true
            throughput: 1000
            iops: 16000
      userData: ${jsonencode(local.soci_user_data)}
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "neuron_node_class" {
  yaml_body = <<-YAML
    apiVersion: karpenter.k8s.aws/v1
    kind: EC2NodeClass
    metadata:
      name: neuron
    spec:
      amiSelectorTerms:
        - alias: al2023@latest
      role: ${module.karpenter.node_iam_role_name}
      subnetSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      securityGroupSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      blockDeviceMappings:
        - deviceName: /dev/xvda
          ebs:
            volumeSize: 500Gi
            volumeType: gp3
            encrypted: true
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "gpu_node_pool" {
  yaml_body = <<-YAML
    apiVersion: karpenter.sh/v1
    kind: NodePool
    metadata:
      name: gpu
    spec:
      # PRD-53: weight=100 preferred over operator NodePools.
      weight: 100
      template:
        spec:
          requirements:
            - key: kubernetes.io/arch
              operator: In
              values: ["amd64"]
            - key: karpenter.k8s.aws/instance-family
              operator: In
              values: ["g5", "g6", "g6e", "g7e", "gr6", "p4d", "p4de", "p5", "p5e", "p5en", "p6-b200", "p6-b300"]
            - key: karpenter.sh/capacity-type
              operator: In
              # PRD-33: include 'reserved' so ODCRs/Capacity Blocks attached
              # to the NodeClass are actually consumed. Karpenter prioritizes
              # reserved > on-demand, so non-reserved scale-outs keep working.
              values: ["reserved", "on-demand"]
          taints:
            - key: nvidia.com/gpu
              effect: NoSchedule
            # PRD-53: dedicated taint.
            - key: accelbench.io/dedicated
              value: "true"
              effect: NoSchedule
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: gpu
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmpty
        # GPU nodes are expensive ($0.80-$30+/hr); deprovision aggressively
        # once the benchmark's model Deployment + loadgen Job are torn down.
        consolidateAfter: 1m
        # Override Karpenter's default 10% budget so multiple empty GPU
        # nodes (e.g. the aftermath of several failed runs) can drain in
        # parallel instead of being serialized one at a time.
        budgets:
          - nodes: "100%"
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "neuron_node_pool" {
  yaml_body = <<-YAML
    apiVersion: karpenter.sh/v1
    kind: NodePool
    metadata:
      name: neuron
    spec:
      # PRD-53: weight=100 preferred over operator NodePools.
      weight: 100
      template:
        spec:
          requirements:
            - key: kubernetes.io/arch
              operator: In
              values: ["amd64"]
            - key: karpenter.k8s.aws/instance-family
              operator: In
              values: ["inf2", "trn1", "trn1n", "trn2"]
            - key: karpenter.sh/capacity-type
              operator: In
              # PRD-33: include 'reserved' so ODCRs/Capacity Blocks attached
              # to the NodeClass are actually consumed.
              values: ["reserved", "on-demand"]
          taints:
            - key: aws.amazon.com/neuron
              effect: NoSchedule
            # PRD-53: dedicated taint.
            - key: accelbench.io/dedicated
              value: "true"
              effect: NoSchedule
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: neuron
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmpty
        # Same reasoning as the gpu nodepool — Neuron instances (inf2/trn)
        # are also costly, so deprovision within a minute of becoming empty.
        consolidateAfter: 1m
        # Override Karpenter's default 10% budget so multiple empty nodes
        # can drain in parallel instead of being serialized one at a time.
        budgets:
          - nodes: "100%"
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

# ---------- NVIDIA Device Plugin ----------
# PRD-53: skipped in brownfield mode when the cluster already has it.
resource "kubectl_manifest" "nvidia_device_plugin" {
  count             = var.install_nvidia_device_plugin ? 1 : 0
  server_side_apply = true
  force_conflicts   = true
  wait              = false
  wait_for_rollout  = false

  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: nvidia-device-plugin-daemonset
      namespace: kube-system
    spec:
      selector:
        matchLabels:
          name: nvidia-device-plugin-ds
      updateStrategy:
        type: RollingUpdate
      template:
        metadata:
          labels:
            name: nvidia-device-plugin-ds
        spec:
          priorityClassName: system-node-critical
          affinity:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  # g/p instances, EXCEPT the multi-node DRA pool. The
                  # classic device plugin and the NVIDIA DRA driver cannot
                  # coexist on a node (KEP-5004); DRA-pool nodes are
                  # labeled accelbench.io/dra=true and run the DRA driver
                  # instead. (DCGM below is intentionally NOT narrowed — it
                  # reads GPU telemetry, doesn't allocate devices, and its
                  # metrics on the DRA pool are needed for PRD-56.)
                  - matchExpressions:
                      - key: karpenter.k8s.aws/instance-category
                        operator: In
                        values: ["g", "p"]
                      - key: accelbench.io/dra
                        operator: DoesNotExist
          tolerations:
            - key: nvidia.com/gpu
              operator: Exists
              effect: NoSchedule
            - key: accelbench.io/dedicated
              operator: Exists
              effect: NoSchedule
            - key: CriticalAddonsOnly
              operator: Exists
          containers:
            - name: nvidia-device-plugin-ctr
              image: nvcr.io/nvidia/k8s-device-plugin:v0.17.1
              env:
                - name: FAIL_ON_INIT_ERROR
                  value: "false"
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop: ["ALL"]
              volumeMounts:
                - name: device-plugin
                  mountPath: /var/lib/kubelet/device-plugins
          volumes:
            - name: device-plugin
              hostPath:
                path: /var/lib/kubelet/device-plugins
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

# ---------- Neuron Device Plugin ----------
resource "kubectl_manifest" "neuron_device_plugin_sa" {
  server_side_apply = true
  force_conflicts   = true

  yaml_body = <<-YAML
    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: neuron-device-plugin
      namespace: kube-system
  YAML

  depends_on = [kubectl_manifest.nvidia_device_plugin]
}

resource "kubectl_manifest" "neuron_device_plugin_role" {
  server_side_apply = true
  force_conflicts   = true

  yaml_body = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: neuron-device-plugin
    rules:
      - apiGroups: [""]
        resources: ["nodes"]
        verbs: ["get", "list", "watch"]
      - apiGroups: [""]
        resources: ["nodes/status"]
        verbs: ["patch"]
      - apiGroups: [""]
        resources: ["pods"]
        verbs: ["get", "list", "watch"]
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "neuron_device_plugin_binding" {
  server_side_apply = true
  force_conflicts   = true

  yaml_body = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: neuron-device-plugin
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: neuron-device-plugin
    subjects:
      - kind: ServiceAccount
        name: neuron-device-plugin
        namespace: kube-system
  YAML

  depends_on = [kubectl_manifest.neuron_device_plugin_role]
}

resource "kubectl_manifest" "neuron_device_plugin" {
  server_side_apply = true
  force_conflicts   = true
  wait              = false
  wait_for_rollout  = false

  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: neuron-device-plugin-daemonset
      namespace: kube-system
    spec:
      selector:
        matchLabels:
          name: neuron-device-plugin-ds
      updateStrategy:
        type: RollingUpdate
      template:
        metadata:
          labels:
            name: neuron-device-plugin-ds
        spec:
          priorityClassName: system-node-critical
          serviceAccountName: neuron-device-plugin
          affinity:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  - matchExpressions:
                      - key: karpenter.k8s.aws/instance-category
                        operator: In
                        values: ["inf", "trn"]
          tolerations:
            - key: aws.amazon.com/neuron
              operator: Exists
              effect: NoSchedule
            - key: accelbench.io/dedicated
              operator: Exists
              effect: NoSchedule
            - key: CriticalAddonsOnly
              operator: Exists
          containers:
            - name: neuron-device-plugin
              image: public.ecr.aws/neuron/neuron-device-plugin:2.22.4.0
              imagePullPolicy: Always
              env:
                - name: KUBECONFIG
                  value: /etc/kubernetes/kubelet.conf
                - name: NODE_NAME
                  valueFrom:
                    fieldRef:
                      fieldPath: spec.nodeName
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop: ["ALL"]
              volumeMounts:
                - name: device-plugin
                  mountPath: /var/lib/kubelet/device-plugins
                - name: infa-map
                  mountPath: /run
          volumes:
            - name: device-plugin
              hostPath:
                path: /var/lib/kubelet/device-plugins
            - name: infa-map
              hostPath:
                path: /run
  YAML

  depends_on = [kubectl_manifest.neuron_device_plugin_binding]
}

# ---------- DCGM Exporter for GPU Metrics ----------
resource "kubectl_manifest" "dcgm_exporter" {
  server_side_apply = true
  force_conflicts   = true
  wait              = false
  wait_for_rollout  = false

  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: dcgm-exporter
      namespace: kube-system
      labels:
        app.kubernetes.io/name: dcgm-exporter
    spec:
      selector:
        matchLabels:
          app.kubernetes.io/name: dcgm-exporter
      updateStrategy:
        type: RollingUpdate
      template:
        metadata:
          labels:
            app.kubernetes.io/name: dcgm-exporter
        spec:
          priorityClassName: system-node-critical
          affinity:
            nodeAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                nodeSelectorTerms:
                  - matchExpressions:
                      - key: karpenter.k8s.aws/instance-category
                        operator: In
                        values: ["g", "p"]
          tolerations:
            - key: nvidia.com/gpu
              operator: Exists
              effect: NoSchedule
            - key: accelbench.io/dedicated
              operator: Exists
              effect: NoSchedule
            # PRD-56: also tolerate the multi-node static GPU pool's taint so
            # the exporter lands on distributed-inference nodes — otherwise the
            # per-node DCGM scrape (orchestrator llmdServingNodeIPs -> :9400)
            # hits no exporter and reports 0% GPU metrics for distributed runs.
            - key: accelbench.io/multinode
              operator: Exists
              effect: NoSchedule
            - key: CriticalAddonsOnly
              operator: Exists
          containers:
            - name: dcgm-exporter
              image: nvcr.io/nvidia/k8s/dcgm-exporter:3.3.9-3.6.1-ubuntu22.04
              # Use the image's bundled DCP-enabled counters CSV so we get
              # DCGM_FI_PROF_SM_ACTIVE and DCGM_FI_PROF_PIPE_TENSOR_ACTIVE
              # in addition to the default NVML counters. See PRD-22.
              args:
                - "-f"
                - "/etc/dcgm-exporter/dcp-metrics-included.csv"
              ports:
                - name: metrics
                  containerPort: 9400
                  hostPort: 9400
              env:
                - name: DCGM_EXPORTER_LISTEN
                  value: ":9400"
                - name: DCGM_EXPORTER_KUBERNETES
                  value: "true"
              securityContext:
                runAsNonRoot: false
                runAsUser: 0
                capabilities:
                  add: ["SYS_ADMIN"]
              volumeMounts:
                - name: pod-resources
                  mountPath: /var/lib/kubelet/pod-resources
                  readOnly: true
          volumes:
            - name: pod-resources
              hostPath:
                path: /var/lib/kubelet/pod-resources
  YAML

  depends_on = [kubectl_manifest.nvidia_device_plugin]
}

# ---------- ECR Pull-through Cache permissions for Karpenter nodes (PRD-29) ----------
# AmazonEC2ContainerRegistryReadOnly covers normal ECR pulls but NOT the extra
# actions required to hydrate a pull-through cache on first pull:
#   ecr:CreateRepository         - auto-create the cached repo (e.g., dockerhub/vllm/vllm-openai)
#   ecr:BatchImportUpstreamImage - fetch the image from the upstream registry into ECR
resource "aws_iam_role_policy" "karpenter_node_ecr_pullthrough" {
  count = var.manage_pull_through_cache ? 1 : 0
  name  = "ECRPullThroughCache"
  role  = module.karpenter.node_iam_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ecr:CreateRepository",
        "ecr:BatchImportUpstreamImage",
      ]
      Resource = "*"
    }]
  })
}

# ---------- DRA drivers for the multi-node / distributed-inference pool (PRD-55) ----------
# Both drivers run ONLY on nodes labeled accelbench.io/dra=true (the
# static EFA GPU pool). The classic NVIDIA device plugin is excluded from
# those nodes (see its nodeAffinity above) because a DRA GPU driver and
# the classic device plugin cannot manage GPUs on the same node
# (KEP-5004). DCGM is intentionally left cluster-wide (telemetry only).

# NVIDIA DRA driver — GPU allocation via the gpu.nvidia.com DeviceClass.
# ComputeDomain (multi-node NVLink / IMEX, GB200) is DISABLED — out of
# scope until a GB200 PRD. gpuResourcesEnabledOverride=true is REQUIRED:
# the chart hard-errors if resources.gpus.enabled=true without it (its
# KEP-5004 safety guard against colliding with the classic device
# plugin, which we satisfy by node-partitioning).
resource "helm_release" "nvidia_dra_driver" {
  count = var.install_dra_drivers ? 1 : 0

  name             = "nvidia-dra-driver-gpu"
  namespace        = "nvidia-dra-driver-gpu"
  create_namespace = true
  repository       = "https://helm.ngc.nvidia.com/nvidia"
  chart            = "nvidia-dra-driver-gpu"
  version          = var.nvidia_dra_driver_version

  values = [yamlencode({
    gpuResourcesEnabledOverride = true
    # Host-provided driver: the EKS accelerated AL2023 AMI installs the
    # NVIDIA driver on the host at `/` (not operator-provided at
    # /run/nvidia/driver). This matches the chart default; set explicitly
    # so a future default change can't silently break GPU discovery.
    nvidiaDriverRoot = "/"
    resources = {
      gpus           = { enabled = true }
      computeDomains = { enabled = false }
    }
    # Pin the kubelet-plugin DaemonSet to the multi-node DRA pool only.
    kubeletPlugin = {
      nodeSelector = { "accelbench.io/dra" = "true" }
      tolerations = [
        { key = "nvidia.com/gpu", operator = "Exists", effect = "NoSchedule" },
        { key = "accelbench.io/multinode", operator = "Exists", effect = "NoSchedule" },
      ]
    }
  })]

  depends_on = [time_sleep.wait_for_karpenter]
}

# AWS EFA DRA driver (DRANET) — EFA allocation via the
# efa.networking.k8s.aws DeviceClass, topology-aligned to GPUs on the
# same PCIe root. DaemonSet `aws-dranet` in kube-system; requires EKS
# 1.34+. Node targeting is top-level nodeSelector/tolerations (no nested
# kubeletPlugin key in this chart).
resource "helm_release" "aws_dranet" {
  count = var.install_dra_drivers ? 1 : 0

  name       = "aws-dranet"
  namespace  = "kube-system"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-dranet"
  version    = var.aws_dranet_version

  values = [yamlencode({
    nodeSelector = { "accelbench.io/dra" = "true" }
    tolerations = [
      { key = "nvidia.com/gpu", operator = "Exists", effect = "NoSchedule" },
      { key = "accelbench.io/multinode", operator = "Exists", effect = "NoSchedule" },
    ]
  })]

  depends_on = [time_sleep.wait_for_karpenter]
}

# ---------- Multi-node static EFA GPU pools, one per AZ (PRD-55) ----------
# For each AZ the VPC spans, a dedicated EC2NodeClass + static NodePool:
#   - rests on the GPU instance-category (g/p); the orchestrator narrows the
#     pool to the run's selected instance type per run (setNodePoolInstanceType).
#     A static pool provisions from its OWN requirements (Karpenter ignores pods),
#     so this per-run narrowing is what makes the run form drive the hardware.
#   - pinned to ONE AZ (topology.kubernetes.io/zone requirement + subnet id)
#   - bound to that AZ's cluster placement group (co-location for EFA/NCCL)
#   - EFA network interfaces (efa-only on device index 1) for RDMA
#   - labeled accelbench.io/dra=true so the DRA drivers land here and the
#     classic device plugin stays off (KEP-5004)
#   - tainted accelbench.io/multinode so only distributed-inference pods run
#   - spec.replicas: 0 at rest. The orchestrator (PRD-56) scales the chosen
#     AZ's pool up per run and back to 0 on teardown; a future capacity
#     fallback can try another AZ's pool. Terraform ignores replicas so a
#     re-apply never fights a mid-run scale-out.
resource "kubectl_manifest" "multinode_node_class" {
  for_each = var.enable_multinode ? var.multinode_placement_groups : {}

  yaml_body = <<-YAML
    apiVersion: karpenter.k8s.aws/v1
    kind: EC2NodeClass
    metadata:
      name: multinode-${each.key}
    spec:
      # Accelerated (NVIDIA) AL2023 AMI, pinned via SSM (same as `gpu`).
      amiFamily: AL2023
      amiSelectorTerms:
        - id: ${data.aws_ssm_parameter.gpu_ami.value}
      role: ${module.karpenter.node_iam_role_name}
      # Single AZ: select this AZ's private subnet by ID. subnetSelectorTerms
      # has no availability-zone field and the subnets aren't zone-tagged,
      # so selecting the specific subnet id is what pins the pool to its AZ.
      subnetSelectorTerms:
        - id: ${var.multinode_subnets[each.key]}
      securityGroupSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      # Co-locate all nodes of this pool in this AZ's cluster placement
      # group (Karpenter selects an existing PG; Terraform creates it).
      placementGroupSelector:
        name: ${each.value}
      # EFA for multi-node NCCL/RDMA: standard ENA on device 0, an
      # efa-only (IP-less, RDMA) interface on device 1.
      networkInterfaces:
        - networkCardIndex: 0
          deviceIndex: 0
          interfaceType: interface
        - networkCardIndex: 0
          deviceIndex: 1
          interfaceType: efa-only
      instanceStorePolicy: RAID0
      blockDeviceMappings:
        - deviceName: /dev/xvda
          ebs:
            volumeSize: 100Gi
            volumeType: gp3
            encrypted: true
            throughput: 1000
            iops: 16000
      # PRD-65: SOCI parallel-pull tuning (shared local) — speeds the 8.9 GB
      # llm-d-aws image pull on fresh EFA multinode nodes.
      userData: ${jsonencode(local.soci_user_data)}
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

# TCP-mode node class (SINGLE, shared across AZs): identical to the EFA
# multinode-<az> classes EXCEPT it OMITS the efa-only networkInterfaces block AND
# the per-AZ placement group. An efa-only interface makes Karpenter launch ONLY
# EFA-capable instances (the scarce large g6/p types); a network_mode=tcp run
# doesn't use EFA, so it should be free to launch small, plentiful GPU instances
# (e.g. g6.2xlarge).
#
# Why ONE class, not per-AZ: AZ placement is enforced by the NodePool's own
# topology.kubernetes.io/zone requirement, and this class uses TAG-BASED subnet
# discovery (every AZ's private subnet carries karpenter.sh/discovery), so
# Karpenter picks the subnet matching the pool's zone. TCP needs no cluster
# placement group (that's an EFA/RDMA co-location optimization), so this class
# omits placementGroupSelector and can serve any AZ. The orchestrator points a
# pool's nodeClassRef at this class for TCP runs and at the per-AZ EFA class for
# EFA runs (see setNodePoolNetworkMode).
resource "kubectl_manifest" "multinode_node_class_tcp" {
  count = var.enable_multinode ? 1 : 0

  yaml_body = <<-YAML
    apiVersion: karpenter.k8s.aws/v1
    kind: EC2NodeClass
    metadata:
      name: multinode-tcp
    spec:
      amiFamily: AL2023
      amiSelectorTerms:
        - id: ${data.aws_ssm_parameter.gpu_ami.value}
      role: ${module.karpenter.node_iam_role_name}
      # Tag-based subnet discovery across all AZs; the NodePool's zone
      # requirement selects which one. No efa-only interface, no placement group.
      subnetSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      securityGroupSelectorTerms:
        - tags:
            karpenter.sh/discovery: ${var.cluster_name}
      instanceStorePolicy: RAID0
      blockDeviceMappings:
        - deviceName: /dev/xvda
          ebs:
            volumeSize: 100Gi
            volumeType: gp3
            encrypted: true
            throughput: 1000
            iops: 16000
      # PRD-65: SOCI parallel-pull tuning (shared local), same as the EFA class.
      userData: ${jsonencode(local.soci_user_data)}
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

resource "kubectl_manifest" "multinode_node_pool" {
  for_each = var.enable_multinode ? var.multinode_placement_groups : {}

  # Ignore ONLY spec.replicas server-side: Terraform creates the pool at
  # 0 and never reconciles the count afterwards (the orchestrator owns it
  # at runtime — a stray apply must not reset a mid-run scale-out). Every
  # other field (instance type, taints, PG) still reconciles normally.
  ignore_fields = ["spec.replicas"]

  # spec.replicas makes this a STATIC pool (fixed node count, no
  # scheduling simulation — the one Karpenter mode compatible with DRA).
  # Starts at 0; orchestrator-managed at runtime.
  yaml_body = <<-YAML
    apiVersion: karpenter.sh/v1
    kind: NodePool
    metadata:
      name: multinode-${each.key}
    spec:
      # NOTE: `weight` is NOT valid on static (replicas-based) NodePools —
      # Karpenter rejects it. Weight only applies to dynamic pools that
      # compete in scheduling. These pools are explicitly targeted by name.
      replicas: 0
      template:
        metadata:
          labels:
            accelbench.io/dra: "true"
        spec:
          requirements:
            - key: kubernetes.io/arch
              operator: In
              values: ["amd64"]
            # RESTING constraint: the GPU instance categories (g/p). This is a
            # static pool, so it provisions from the POOL's requirements (not the
            # pods') — the orchestrator NARROWS this to the run's exact instance
            # type at scale-out (setNodePoolInstanceType), replacing this category
            # key with node.kubernetes.io/instance-type=<selected>. So the run
            # form drives the actual hardware; this category is only the at-rest
            # superset (and the fallback if the orchestrator didn't set a type).
            # Subject to EFA capability — the node class attaches an efa-only
            # interface, so only EFA-capable instances launch. All of a run's
            # nodes share one type (single AZ + PG friendly).
            - key: karpenter.k8s.aws/instance-category
              operator: In
              values: ["g", "p"]
            - key: topology.kubernetes.io/zone
              operator: In
              values: ["${each.key}"]
            - key: karpenter.sh/capacity-type
              operator: In
              values: ["reserved", "on-demand"]
          taints:
            - key: nvidia.com/gpu
              effect: NoSchedule
            - key: accelbench.io/multinode
              value: "true"
              effect: NoSchedule
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: multinode-${each.key}
  YAML

  depends_on = [kubectl_manifest.multinode_node_class]
}
