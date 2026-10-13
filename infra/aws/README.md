# Tally `cored` on AWS

```
browser ─▶ tallyweb.world (Vercel, static) ─rewrite /v1,/auth─▶ api.tallyweb.world
                                                                    │
                                    CloudFront (TLS, HTTP/3, 60 s origin timeout)
                                                                    │
                     Lambda Function URL ─▶ cored (Go, arm64) behind Lambda Web Adapter
                                                │  private subnets, egress via fck-nat
                                                ▼
                         Aurora Serverless v2 Postgres 17 (pgvector), 0–4 ACU, auto-pause
EventBridge Scheduler ─▶ invokes cored directly: /internal/cron/tick hourly, /anchor 21:30 UTC
```

Shared pieces (VPC, NAT instance, Aurora cluster, state bucket) live in
`~/code/aws-platform`; this stack reads them through `terraform_remote_state`.

## Why these choices

| Choice | Reason |
|---|---|
| Function URL + CloudFront, not API Gateway | HTTP API cuts requests at 30 s; cored's LLM calls allow 60 s. CloudFront also provides the custom domain. |
| Zip on `provided.al2023` + web adapter layer | Static Go binary, no Docker or ECR; the adapter runs as a Lambda extension. |
| Scheduler invokes the function directly | No 60 s CloudFront limit for the nightly pass; no extra resources. |
| Hourly tick (not every minute) | The cron wakes Aurora; hourly lets it pause between runs. In-process pollers still run while a request keeps the function warm. |
| fck-nat instead of NAT Gateway | ~$7/month vs ~$33/month. |

## Deploy

```bash
./build.sh                                # build/cored.zip
terraform init && terraform apply
```

### First-time database setup

Migrations need a direct connection (advisory lock), so they run from a
workstation through the SSM tunnel:

```bash
~/code/aws-platform/scripts/db-tunnel.sh &
~/code/aws-platform/scripts/with-admin-dsn.sh psql '$ADMIN_URL' -c 'create database tally'
# then, from backend/:
DB=tally ~/code/aws-platform/scripts/with-admin-dsn.sh sh -c '
  TALLY_APP_ROLE_PASSWORD=$(aws secretsmanager get-secret-value --secret-id tally/cored --query SecretString --output text | jq -r .TALLY_APP_ROLE_PASSWORD) \
  TALLY_STATUTORY_ROLE_PASSWORD=$(aws secretsmanager get-secret-value --secret-id tally/cored --query SecretString --output text | jq -r .TALLY_STATUTORY_ROLE_PASSWORD) \
  ADMIN_DSN="$ADMIN_URL" go run ./cmd/cored migrate'
```

## Cutover (api.tallyweb.world)

1. `terraform apply -var api_domain=api.tallyweb.world` creates the ACM
   certificate; add the `acm_validation` CNAME in Vercel DNS
   (`vercel dns add tallyweb.world <name-without-domain> CNAME <value>`).
2. Once the certificate is issued, apply again so CloudFront takes the alias.
3. Smoke test against the CloudFront domain: `/healthz`, login, `/v1/me`.
4. Repoint `api` in Vercel DNS to the CloudFront domain (CNAME). The Vercel
   `tally-cored` project stays up until the AWS side has run cleanly for a day.

## Not covered yet

- insightd and statutoryd (not deployed on Vercel either).
- The Function URL is reachable directly, bypassing CloudFront. The app's own
  auth still applies; an origin-verify header check in cored would close it.
