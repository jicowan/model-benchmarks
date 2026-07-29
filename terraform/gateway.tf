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
resource "helm_release" "envoy_gateway" {
  count = local.multinode_serving ? 1 : 0

  name             = "eg"
  namespace        = "envoy-gateway-system"
  create_namespace = true
  repository       = "oci://docker.io/envoyproxy"
  chart            = "gateway-helm"
  version          = var.envoy_gateway_version

  # AI-Gateway integration values (inference-pool support, etc.).
  values = [data.http.envoy_gateway_values[0].response_body]

  wait    = true
  timeout = 600

  depends_on = [
    kubectl_manifest.gateway_api_crds,
    kubectl_manifest.inference_extension_crds,
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
