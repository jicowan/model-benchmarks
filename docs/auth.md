# Authentication (Cognito + ALB OIDC)

AccelBench supports placing the UI behind an ALB that authenticates users
against an Amazon Cognito User Pool. Flow:

1. User visits `https://<your-domain>`
2. ALB sees no session cookie and redirects to the Cognito Hosted UI
3. User signs in with username/password (admin-provisioned)
4. Cognito redirects back to the ALB, which sets a session cookie and forwards
   the request to the web pod
5. Subsequent requests carry the cookie and go straight to the app

Authentication is **disabled by default**. Existing installs are unaffected.

## Prerequisites

| Item | Notes |
|------|-------|
| Domain | FQDN you own (e.g. `accelbench.example.com`) |
| Route53 hosted zone | Public hosted zone for the domain, in the same AWS account |
| ACM certificate | Issued in **us-west-2**, covering the domain (exact or wildcard) |
| EKS cluster | Must have Auto Mode elastic load balancing enabled (see below) |

### One-time EKS Auto Mode setup

This chart uses the built-in Auto Mode ALB controller
(`eks.amazonaws.com/alb`). If your cluster was created before this feature
existed, enable it once:

```bash
aws eks update-cluster-config \
  --name <your-cluster> \
  --region <your-region> \
  --compute-config '{"enabled":true}' \
  --kubernetes-network-config '{"elasticLoadBalancing":{"enabled":true}}' \
  --storage-config '{"blockStorage":{"enabled":true}}'

# Wait for the cluster to return to ACTIVE (~5-10 min)
aws eks wait cluster-active --name <your-cluster> --region <your-region>
```

Clusters created fresh from this repo's Terraform already have this enabled.

## Enable

1. **Copy and edit tfvars** (not committed — `terraform.tfvars` is gitignored):
   ```bash
   cd terraform
   cp terraform.tfvars.example terraform.tfvars
   ```
   Set the auth block:
   ```hcl
   auth_enabled          = true
   domain_name           = "accelbench.example.com"
   hosted_zone_id        = "<ROUTE53_HOSTED_ZONE_ID>"
   acm_certificate_arn   = "<ACM_CERTIFICATE_ARN>"
   cognito_domain_prefix = "accelbench-auth"
   mfa_configuration     = "OFF"   # or OPTIONAL / ON

   # Optional — enable self-signup for specific email domains only.
   # Empty (default) = admins create users via CLI; signup link is hidden.
   # allowed_email_domains = ["amazon.com"]
   ```

2. **Apply Terraform**:
   ```bash
   terraform apply
   ```
   This creates:
   - Cognito User Pool, app client, hosted-UI domain
   - IAM policies on the EKS cluster role for `cognito-idp:DescribeUserPoolClient`
     and `elasticloadbalancing:SetSubnets` (required for ALB OIDC to work with
     Auto Mode)
   - external-dns deployment + IAM role (scoped to your hosted zone)

3. **Upgrade Helm release with auth enabled**:
   ```bash
   helm upgrade accelbench helm/accelbench -n accelbench \
     --reuse-values \
     --set auth.enabled=true \
     --set auth.userPoolArn=$(terraform -chdir=terraform output -raw cognito_user_pool_arn) \
     --set auth.userPoolClientId=$(terraform -chdir=terraform output -raw cognito_user_pool_client_id) \
     --set auth.userPoolDomain=$(terraform -chdir=terraform output -raw cognito_user_pool_domain) \
     --set ingress.certificateArn=$(terraform -chdir=terraform output -raw acm_certificate_arn) \
     --set ingress.host=$(terraform -chdir=terraform output -raw ingress_host)
   ```

4. **Create the first user** (password is never stored in Terraform state):
   ```bash
   USER_POOL_ID=$(terraform -chdir=terraform output -raw cognito_user_pool_id)

   aws cognito-idp admin-create-user \
     --user-pool-id "$USER_POOL_ID" \
     --username you@example.com \
     --user-attributes Name=email,Value=you@example.com Name=email_verified,Value=true \
     --temporary-password 'TempPass123!Aws' \
     --message-action SUPPRESS

   # Make the password permanent so the user isn't forced to reset on first login
   aws cognito-idp admin-set-user-password \
     --user-pool-id "$USER_POOL_ID" \
     --username you@example.com \
     --password 'YourRealPassword!' \
     --permanent
   ```

   Password policy: 12+ chars, at least one lowercase, uppercase, digit, symbol.

5. **Visit** `https://<domain_name>`. You'll be redirected to Cognito, sign in,
   and land on the Dashboard. Allow ~60 seconds after `helm upgrade` for
   external-dns to publish the Route53 record and the ALB to reach active state.

## How it's wired

- **Single ingress** with ALB OIDC annotations. `/healthz` and `/api/v1/status`
  don't need separate handling because ALB target-group health checks bypass
  OIDC automatically, and the frontend's status calls ride the session cookie.
- **external-dns** runs in `kube-system`, watches Ingress resources, filters to
  your hosted zone, and publishes A-alias records pointing at the ALB.
  Interval is 30s + event-triggered for fast propagation.
- **Cognito callback URL** is computed from `var.domain_name` at apply time, so
  changing the domain only requires `terraform apply` — no second step.

## Self-signup (optional)

By default the user pool is admin-provisioned only — users are created via the
AWS CLI and the Cognito Hosted UI hides the "Sign up" link.

To allow users to sign themselves up while still gatekeeping by email domain,
set `allowed_email_domains` in `terraform.tfvars`:

```hcl
allowed_email_domains = ["amazon.com"]       # one domain
# or
allowed_email_domains = ["amazon.com", "aws.dev"]   # multiple
```

What this does:

1. A pre-signup Lambda trigger is deployed (`<project>-cognito-pre-signup`)
2. The Cognito Hosted UI shows the "Sign up" link
3. When someone submits the signup form, Cognito invokes the Lambda
4. The Lambda rejects the signup unless the email ends in one of the allowed
   domains ("Sign-up is restricted to: amazon.com" error shown to the user)
5. Matching signups are auto-confirmed and auto-verified — the user can log
   in immediately without clicking a confirmation email

To close signup back up, set `allowed_email_domains = []` (or remove the line)
and re-apply. Existing users keep their access; only new signups are blocked.

## Managing users

```bash
# Add another user
aws cognito-idp admin-create-user \
  --user-pool-id "$USER_POOL_ID" \
  --username teammate@example.com \
  --user-attributes Name=email,Value=teammate@example.com Name=email_verified,Value=true \
  --temporary-password 'TempPass123!Aws' \
  --message-action SUPPRESS

# Disable a user (revokes access immediately)
aws cognito-idp admin-disable-user --user-pool-id "$USER_POOL_ID" --username user@example.com

# Delete a user
aws cognito-idp admin-delete-user --user-pool-id "$USER_POOL_ID" --username user@example.com

# Force a password reset
aws cognito-idp admin-reset-user-password --user-pool-id "$USER_POOL_ID" --username user@example.com
```

## Disable

To turn auth off without tearing down Cognito:
```bash
helm upgrade accelbench helm/accelbench -n accelbench --reuse-values --set auth.enabled=false
```
The ingress reverts to un-authenticated HTTPS. Cognito pool and users remain
intact and can be re-enabled by flipping the flag back.

## Destroy

To remove **all** auth resources (Cognito pool, users, external-dns role):
```bash
cd terraform
# Set auth_enabled = false in terraform.tfvars, then:
terraform apply
```
Terraform removes `module.auth[0]` and `module.external_dns[0]` cleanly. The
Route53 records they created are preserved (`policy=upsert-only` never deletes).

⚠️ Deleting the user pool is irreversible — all users lose access permanently.

## Troubleshooting

**"Invalid redirect_uri" on Cognito login page**

The app client's callback URL is set from `var.domain_name`. If you changed the
domain after apply, re-run `terraform apply` to update the client.

**Browser gets stuck or SSL error on Cognito page**

Confirm the ACM cert covers `domain_name` and is in us-west-2. ALB silently
fails OIDC if the listener cert doesn't match.

**Ingress shows no ADDRESS**

```bash
kubectl -n accelbench describe ingress accelbench | tail -30
```

Common causes:
- EKS Auto Mode elastic load balancing not enabled (see one-time setup above).
- The `alb` IngressClass missing — fixed automatically by this chart when
  `ingress.createIngressClass=true` (the default). Set `false` if another
  controller owns the class.
- Missing IAM permissions on the cluster role — Terraform's auth module adds
  them, so only an issue if you bypassed `terraform apply`.

**external-dns isn't creating the Route53 record**

```bash
kubectl -n kube-system logs -l app.kubernetes.io/name=external-dns --tail=50
```
Look for:
- `Applying provider record filter for domains: [...]` — should list your
  domain. Empty means the hosted zone filter isn't matching.
- `level=error` — often an IAM issue. Confirm the Pod Identity association
  resolved the role ARN.

If it still won't publish, restart it:
```bash
kubectl -n kube-system rollout restart deploy external-dns
```

**Login works but `/api/*` calls fail with 401/302**

The main ingress passes the Cognito session cookie through to the backend.
Browser-initiated `/api/*` calls from the SPA ride this cookie automatically.
If you're hitting the API from `curl` or a test harness, you'll need to send
the `AWSELBAuthSessionCookie-*` cookie returned after login.

## What this does NOT do (future work)

- API-level JWT validation. The ALB session cookie is the only auth boundary
  today. A follow-up PR can add `X-Amzn-Oidc-Data` validation middleware so
  the API is safe behind direct access too.
- Role-based access control. Every authenticated user has the same access.
