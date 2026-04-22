module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "~> 20.31"

  cluster_name = var.cluster_name

  enable_v1_permissions = true

  enable_pod_identity             = true
  create_pod_identity_association = true

  node_iam_role_additional_policies = {
    AmazonSSMManagedInstanceCore = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
  }

  tags = var.tags
}

resource "helm_release" "karpenter_crd" {
  namespace  = "kube-system"
  name       = "karpenter-crd"
  repository = "oci://public.ecr.aws/karpenter"
  chart      = "karpenter-crd"
  version    = var.karpenter_version
  wait       = true
  timeout    = 300
}

resource "helm_release" "karpenter" {
  namespace        = "kube-system"
  name             = "karpenter"
  repository       = "oci://public.ecr.aws/karpenter"
  chart            = "karpenter"
  version          = var.karpenter_version
  wait             = true
  wait_for_jobs    = true
  timeout          = 600

  values = [
    <<-EOT
    settings:
      clusterName: ${var.cluster_name}
      clusterEndpoint: ${var.cluster_endpoint}
      interruptionQueue: ${module.karpenter.queue_name}
      # PRD-33: enables capacityReservationSelectorTerms on EC2NodeClass
      # and 'reserved' as a capacity-type. Still beta in Karpenter 1.9.
      featureGates:
        reservedCapacity: true
    EOT
  ]

  depends_on = [module.karpenter, helm_release.karpenter_crd]
}

# Wait for Karpenter to be ready before creating node classes
resource "time_sleep" "wait_for_karpenter" {
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
          expireAfter: 720h
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: default
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmptyOrUnderutilized
        consolidateAfter: 5m
        budgets:
          - nodes: "10%"
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
      amiSelectorTerms:
        - alias: al2023@latest
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
      userData: |
        MIME-Version: 1.0
        Content-Type: multipart/mixed; boundary="BOUNDARY"

        --BOUNDARY
        Content-Type: text/x-shellscript; charset="us-ascii"

        #!/bin/bash
        # SOCI parallel-pull tuning lives in /etc/soci-snapshotter-grpc/config.toml
        # under [pull_modes.parallel_pull_unpack]. nodeadm cannot set these via
        # containerd.config, so we edit the file directly before nodeadm init.
        sed -i 's/^[[:space:]]*max_concurrent_downloads_per_image = .*/max_concurrent_downloads_per_image = 20/' /etc/soci-snapshotter-grpc/config.toml
        sed -i 's/^[[:space:]]*max_concurrent_unpacks_per_image = .*/max_concurrent_unpacks_per_image = 12/' /etc/soci-snapshotter-grpc/config.toml
        sed -i 's/^[[:space:]]*concurrent_download_chunk_size = .*/concurrent_download_chunk_size = "16mb"/' /etc/soci-snapshotter-grpc/config.toml
        sed -i 's/^[[:space:]]*discard_unpacked_layers = .*/discard_unpacked_layers = true/' /etc/soci-snapshotter-grpc/config.toml

        --BOUNDARY
        Content-Type: application/node.eks.aws

        apiVersion: node.eks.aws/v1alpha1
        kind: NodeConfig
        spec:
          featureGates:
            FastImagePull: true
        --BOUNDARY--
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
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: gpu
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmpty
        consolidateAfter: 10m
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
          nodeClassRef:
            group: karpenter.k8s.aws
            kind: EC2NodeClass
            name: neuron
      limits:
        cpu: "1000"
      disruption:
        consolidationPolicy: WhenEmpty
        consolidateAfter: 10m
  YAML

  depends_on = [time_sleep.wait_for_karpenter]
}

# ---------- NVIDIA Device Plugin ----------
resource "kubectl_manifest" "nvidia_device_plugin" {
  server_side_apply  = true
  force_conflicts    = true
  wait               = false
  wait_for_rollout   = false

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
                  - matchExpressions:
                      - key: karpenter.k8s.aws/instance-category
                        operator: In
                        values: ["g", "p"]
          tolerations:
            - key: nvidia.com/gpu
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
  server_side_apply  = true
  force_conflicts    = true
  wait               = false
  wait_for_rollout   = false

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
  server_side_apply  = true
  force_conflicts    = true
  wait               = false
  wait_for_rollout   = false

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
  name = "ECRPullThroughCache"
  role = module.karpenter.node_iam_role_name

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
