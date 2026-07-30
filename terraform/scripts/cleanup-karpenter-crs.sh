#!/usr/bin/env bash
# Pre-destroy cleanup for Karpenter custom resources.
#
# On teardown, the `karpenter-crd` Helm release's uninstall hangs
# (`context deadline exceeded`) because Karpenter's CRs — NodePools,
# EC2NodeClasses, NodeClaims — carry finalizers that only the Karpenter
# CONTROLLER can clear. Terraform tears the controller down around the
# same time as the CRD chart, so those finalizers are never processed and
# CRD deletion blocks until timeout. This has forced a second
# `terraform destroy` on every teardown.
#
# Fix: while the controller is still up, delete the CRs and strip their
# finalizers so the CRD chart can uninstall cleanly in one pass. Ordered
# to run BEFORE the Helm releases are removed (see null_resource in
# main.tf: this resource depends_on the Karpenter helm releases, so on
# destroy — reverse dependency order — it runs first).
#
# Idempotent, best-effort: must never fail the destroy. Every branch
# tolerates "nothing there" / "cluster already gone".
#
# Args: $1 = region, $2 = cluster name.
set -uo pipefail

REGION="${1:?region required}"
CLUSTER="${2:?cluster name required}"

echo "[karpenter-cleanup] region=$REGION cluster=$CLUSTER"

# If the cluster is already gone, nothing to clean.
if ! aws eks describe-cluster --region "$REGION" --name "$CLUSTER" \
  --query 'cluster.status' --output text >/dev/null 2>&1; then
  echo "[karpenter-cleanup] cluster not found — nothing to clean"
  exit 0
fi

# Point kubectl at this cluster in an isolated kubeconfig so we don't
# disturb the user's current context.
KUBECONFIG_TMP="$(mktemp "${TMPDIR:-/tmp}/kcfg.XXXXXX")" || exit 0
export KUBECONFIG="$KUBECONFIG_TMP"
trap 'rm -f "$KUBECONFIG_TMP"' EXIT
aws eks update-kubeconfig --region "$REGION" --name "$CLUSTER" >/dev/null 2>&1 || {
  echo "[karpenter-cleanup] could not build kubeconfig — skipping"; exit 0; }

# Delete Karpenter CRs, then strip finalizers on any that linger so their
# deletion can complete without the controller. Order: workload-bearing
# CRs first (nodeclaims), then pools, then classes.
for kind in nodeclaims.karpenter.sh nodepools.karpenter.sh ec2nodeclasses.karpenter.k8s.aws; do
  names=$(kubectl get "$kind" -o name 2>/dev/null) || continue
  [ -z "$names" ] && continue
  echo "[karpenter-cleanup] deleting $kind"
  # Best-effort delete with a short wait, then force finalizer removal.
  kubectl delete "$kind" --all --wait=false --timeout=30s 2>/dev/null || true
  while IFS= read -r res; do
    [ -z "$res" ] && continue
    kubectl patch "$res" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
  done <<< "$names"
done

echo "[karpenter-cleanup] done (best-effort)"
exit 0
