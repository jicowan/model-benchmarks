#!/usr/bin/env bash
# Pre-destroy cleanup for resources the AWS Load Balancer Controller and
# VPC-CNI create at RUNTIME but Terraform never manages, so `terraform
# destroy` cannot remove them — and they block VPC teardown (orphaned
# ALBs hold ENIs with public IPs; k8s-* security groups pin the VPC).
#
# Invoked as a destroy-time provisioner (see null_resource.lb_cleanup in
# main.tf). Idempotent and best-effort: it must NOT fail the destroy if
# there's nothing to clean, so every branch tolerates "not found".
#
# Args: $1 = region, $2 = cluster name (used to locate the cluster VPC).
set -uo pipefail

REGION="${1:?region required}"
CLUSTER="${2:?cluster name required}"

echo "[cleanup] region=$REGION cluster=$CLUSTER"

# Resolve the cluster's VPC. If the cluster is already gone, there's
# nothing controller-created left to worry about — exit clean.
VPC=$(aws eks describe-cluster --region "$REGION" --name "$CLUSTER" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || true)
if [[ -z "$VPC" || "$VPC" == "None" ]]; then
  echo "[cleanup] cluster/VPC not found — nothing to clean"
  exit 0
fi
echo "[cleanup] VPC=$VPC"

# 1. Delete ALBs/NLBs in the VPC (the LB controller's ingress LBs). This
#    releases their ENIs, which otherwise block subnet + IGW teardown.
for ARN in $(aws elbv2 describe-load-balancers --region "$REGION" \
  --query "LoadBalancers[?VpcId=='$VPC'].LoadBalancerArn" --output text 2>/dev/null); do
  echo "[cleanup] deleting load balancer $ARN"
  aws elbv2 delete-load-balancer --region "$REGION" --load-balancer-arn "$ARN" || true
done

# 2. Delete classic ELBs (belt-and-suspenders; the app uses ALB today).
for NAME in $(aws elb describe-load-balancers --region "$REGION" \
  --query "LoadBalancerDescriptions[?VPCId=='$VPC'].LoadBalancerName" --output text 2>/dev/null); do
  echo "[cleanup] deleting classic ELB $NAME"
  aws elb delete-load-balancer --region "$REGION" --load-balancer-name "$NAME" || true
done

# Give the ELB service a moment to detach the ENIs before we try SGs.
if [[ -n "$(aws ec2 describe-network-interfaces --region "$REGION" \
  --filters "Name=vpc-id,Values=$VPC" "Name=description,Values=ELB *" \
  --query 'NetworkInterfaces[0].NetworkInterfaceId' --output text 2>/dev/null | grep -v None)" ]]; then
  echo "[cleanup] waiting 60s for ELB ENIs to detach"
  sleep 60
fi

# 3. Delete k8s-* security groups the LB controller / VPC-CNI created.
#    These are not in TF state and pin the VPC. Retry a few times because
#    a just-deleted LB's SG may still be referenced for a few seconds.
for _ in 1 2 3; do
  LEFT=0
  for SG in $(aws ec2 describe-security-groups --region "$REGION" \
    --filters "Name=vpc-id,Values=$VPC" \
    --query "SecurityGroups[?GroupName!='default' && starts_with(GroupName,'k8s-')].GroupId" \
    --output text 2>/dev/null); do
    echo "[cleanup] deleting security group $SG"
    if ! aws ec2 delete-security-group --region "$REGION" --group-id "$SG" 2>/dev/null; then
      LEFT=1
    fi
  done
  [[ "$LEFT" -eq 0 ]] && break
  echo "[cleanup] some SGs still referenced; retrying in 15s"
  sleep 15
done

echo "[cleanup] done (best-effort)"
exit 0
