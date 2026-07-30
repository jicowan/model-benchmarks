# PRD-55: multi-node serving prerequisites — LeaderWorkerSet + the
# Gateway API Inference stack (Gateway API CRDs, Inference Extension CRDs,
# Envoy Gateway, Envoy AI Gateway). All gated on enable_multinode so the
# single-instance platform is unaffected. Benchmark traffic is in-cluster
# (the inference-perf loadgen Job -> gateway by cluster DNS), so the Envoy
# proxy Service is ClusterIP — no NLB / AWS LB Controller involvement.
#
# Versions are pinned via variables (see variables.tf). Chart coordinates
# verified against each project's GitHub releases + official docs.

locals {
  multinode_serving = var.manage_cluster && var.enable_multinode
}

# ---------- LeaderWorkerSet controller ----------
# Gang-models the leader + N workers of one multi-node llm-d instance.
# v0.9.0 uses a built-in internal webhook cert — cert-manager NOT required.
# NOTE: chart version has no `v` prefix (release tag is v0.9.0).
resource "helm_release" "lws" {
  count = local.multinode_serving ? 1 : 0

  name             = "lws"
  namespace        = "lws-system"
  create_namespace = true
  repository       = "oci://registry.k8s.io/lws/charts"
  chart            = "lws"
  version          = var.lws_version

  wait    = true
  timeout = 300

  depends_on = [module.eks]
}

# ---------- Gateway API CRDs (standard channel) ----------
# Multi-doc manifest: fetch over HTTP, split, apply each doc server-side.
data "http" "gateway_api_crds" {
  count = local.multinode_serving ? 1 : 0
  url   = "https://github.com/kubernetes-sigs/gateway-api/releases/download/${var.gateway_api_version}/standard-install.yaml"
}

data "kubectl_file_documents" "gateway_api_crds" {
  count   = local.multinode_serving ? 1 : 0
  content = data.http.gateway_api_crds[0].response_body
}

resource "kubectl_manifest" "gateway_api_crds" {
  for_each = local.multinode_serving ? data.kubectl_file_documents.gateway_api_crds[0].manifests : {}

  yaml_body         = each.value
  server_side_apply = true
  wait              = true

  depends_on = [module.eks]
}

# ---------- Gateway API Inference Extension CRDs (InferencePool v1 GA) ----------
data "http" "inference_extension_crds" {
  count = local.multinode_serving ? 1 : 0
  url   = "https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/${var.inference_extension_version}/manifests.yaml"
}

data "kubectl_file_documents" "inference_extension_crds" {
  count   = local.multinode_serving ? 1 : 0
  content = data.http.inference_extension_crds[0].response_body
}

resource "kubectl_manifest" "inference_extension_crds" {
  for_each = local.multinode_serving ? data.kubectl_file_documents.inference_extension_crds[0].manifests : {}

  yaml_body         = each.value
  server_side_apply = true
  wait              = true

  depends_on = [module.eks]
}

# ---------- Envoy Gateway ----------
# The Gateway API implementation Envoy AI Gateway layers on. Installed with
# the AI-Gateway-specific values file (adds inference-pool + ext-proc wiring).
# Envoy Gateway's OWN CRDs (EnvoyProxy, Backend, SecurityPolicy, …).
# Applied via kubectl (server-side apply), NOT Helm, for two reasons:
#   1. The main gateway-helm chart ships the Gateway API CRDs in a raw
#      Helm `crds/` directory that Helm installs UNCONDITIONALLY (values
#      toggles never apply to `crds/` dir files — this is why the earlier
#      crds.gatewayAPI=false override was silently ineffective), and those
#      bundled Gateway API CRDs fail the v1.6.1 safe-upgrades admission
#      policy. So the controller runs with skip_crds=true (below).
#   2. The separate gateway-crds-helm chart renders to ~2.4MB, which
#      overflows Helm's release-state Secret (>1MiB k8s limit). kubectl
#      apply has no such limit.
# The manifest is vendored (rendered from gateway-crds-helm v1.8.3 with
# gatewayAPI=false, envoyGateway=true) so it contains ONLY Envoy's own
# CRDs — the Gateway API CRDs stay owned by kubectl_manifest.gateway_api_crds
# at v1.6.1. Re-render on version bump:
#   helm template eg-crds oci://docker.io/envoyproxy/gateway-crds-helm \
#     --version <ver> --set crds.gatewayAPI.enabled=false \
#     --set crds.envoyGateway.enabled=true > manifests/envoy-gateway-crds-<ver>.yaml
data "kubectl_file_documents" "envoy_gateway_crds" {
  count   = local.multinode_serving ? 1 : 0
  content = file("${path.module}/manifests/envoy-gateway-crds-v1.8.3.yaml")
}

resource "kubectl_manifest" "envoy_gateway_crds" {
  for_each = local.multinode_serving ? data.kubectl_file_documents.envoy_gateway_crds[0].manifests : {}

  yaml_body         = each.value
  server_side_apply = true
  wait              = true

  depends_on = [module.eks]
}

resource "helm_release" "envoy_gateway" {
  count = local.multinode_serving ? 1 : 0

  name             = "eg"
  namespace        = "envoy-gateway-system"
  create_namespace = true
  repository       = "oci://docker.io/envoyproxy"
  chart            = "gateway-helm"
  version          = var.envoy_gateway_version

  # Controller only — do NOT install the chart's bundled CRDs. They live in
  # a raw `crds/` directory that Helm applies unconditionally (ignoring any
  # values toggle), and they include an OLDER Gateway API set the v1.6.1
  # safe-upgrades admission policy rejects. Gateway API CRDs come from
  # kubectl_manifest.gateway_api_crds; Envoy's own from envoy_gateway_crds.
  skip_crds = true

  # AI-Gateway integration values (inference-pool support, etc.).
  values = [data.http.envoy_gateway_values[0].response_body]

  wait    = true
  timeout = 600

  depends_on = [
    kubectl_manifest.gateway_api_crds,
    kubectl_manifest.inference_extension_crds,
    kubectl_manifest.envoy_gateway_crds,
  ]
}

data "http" "envoy_gateway_values" {
  count = local.multinode_serving ? 1 : 0
  url   = "https://raw.githubusercontent.com/envoyproxy/ai-gateway/${var.envoy_ai_gateway_version}/manifests/envoy-gateway-values.yaml"
}

# ---------- Envoy AI Gateway (CRDs chart, then controller chart) ----------
resource "helm_release" "envoy_ai_gateway_crds" {
  count = local.multinode_serving ? 1 : 0

  name             = "aieg-crd"
  namespace        = "envoy-ai-gateway-system"
  create_namespace = true
  repository       = "oci://docker.io/envoyproxy"
  chart            = "ai-gateway-crds-helm"
  version          = var.envoy_ai_gateway_version

  depends_on = [helm_release.envoy_gateway]
}

resource "helm_release" "envoy_ai_gateway" {
  count = local.multinode_serving ? 1 : 0

  name      = "aieg"
  namespace = "envoy-ai-gateway-system"
  # namespace created by the CRDs release above.
  repository = "oci://docker.io/envoyproxy"
  chart      = "ai-gateway-helm"
  version    = var.envoy_ai_gateway_version

  wait    = true
  timeout = 600

  depends_on = [helm_release.envoy_ai_gateway_crds]
}

# ---------- ClusterIP EnvoyProxy config ----------
# Benchmark traffic is in-cluster, so the dynamically-created Envoy proxy
# Service must be ClusterIP, not the default LoadBalancer (which would
# provision an NLB via the AWS LB Controller). The EnvoyProxy CRD is
# referenced by the GatewayClass parametersRef; the llm-d gateway recipe
# (PRD-56) wires that ref. Here we just create the shared config object.
resource "kubectl_manifest" "envoy_proxy_clusterip" {
  count = local.multinode_serving ? 1 : 0

  yaml_body = <<-YAML
    apiVersion: gateway.envoyproxy.io/v1alpha1
    kind: EnvoyProxy
    metadata:
      name: accelbench-clusterip
      namespace: envoy-gateway-system
    spec:
      provider:
        type: Kubernetes
        kubernetes:
          envoyService:
            type: ClusterIP
  YAML

  server_side_apply = true
  depends_on        = [helm_release.envoy_gateway]
}

# ---------- Shared GatewayClass + Gateway (PRD-56) ----------
# The llm-d deploy path (PRD-56) renders a per-run HTTPRoute whose parentRef is
# this ONE shared, long-lived Gateway — created here, not per-run, so a single
# Envoy fronts every distributed model regardless of which AZ it lands in
# (answers PRD-55's "can 1 gateway serve models in any AZ?" — yes). The
# GatewayClass points at the ClusterIP EnvoyProxy config above via
# parametersRef, so the provisioned Envoy Service is ClusterIP (loadgen is
# in-cluster; no NLB).
resource "kubectl_manifest" "accelbench_gatewayclass" {
  count = local.multinode_serving ? 1 : 0

  yaml_body = <<-YAML
    apiVersion: gateway.networking.k8s.io/v1
    kind: GatewayClass
    metadata:
      name: accelbench
    spec:
      controllerName: gateway.envoyproxy.io/gatewayclass-controller
      parametersRef:
        group: gateway.envoyproxy.io
        kind: EnvoyProxy
        name: accelbench-clusterip
        namespace: envoy-gateway-system
  YAML

  server_side_apply = true
  depends_on = [
    kubectl_manifest.envoy_proxy_clusterip,
    kubectl_manifest.gateway_api_crds,
  ]
}

resource "kubectl_manifest" "accelbench_gateway" {
  count = local.multinode_serving ? 1 : 0

  # Name/namespace match the orchestrator's defaults (LLMD_GATEWAY_NAME /
  # LLMD_GATEWAY_NAMESPACE). The orchestrator resolves the gateway Service by
  # DNS at <name>.<namespace>.svc.cluster.local:80.
  yaml_body = <<-YAML
    apiVersion: gateway.networking.k8s.io/v1
    kind: Gateway
    metadata:
      name: accelbench-gateway
      namespace: envoy-gateway-system
    spec:
      gatewayClassName: accelbench
      listeners:
        - name: http
          protocol: HTTP
          port: 80
          allowedRoutes:
            # llm-d HTTPRoutes live in the accelbench namespace; the Gateway is
            # in envoy-gateway-system, so cross-namespace routes must be allowed.
            namespaces:
              from: All
  YAML

  server_side_apply = true
  depends_on        = [kubectl_manifest.accelbench_gatewayclass]
}
