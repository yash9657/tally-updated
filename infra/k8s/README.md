# infra/k8s — Kustomize manifests for the three planes

Kustomize **base + overlays** for the Phase-0 deployables described in
`PLAN.md` A.2:

| Plane | What | Port | Stack |
|---|---|---|---|
| `cored` | Go modular monolith — system of record, atomic voucher post, ledger, compliance-derive, reporting. The keystroke write path. | 8080 | Go 1.26 |
| `statutoryd` | Separately-deployable compliance gateway (IRN / e-way bill / GSTR / IMS) on durable workflows. | 8080 | Go |
| `insightd` | AI insight plane — reads a read-replica, never in the keystroke loop. | 8000 | Python/FastAPI |

## Layout

```
base/
  namespace.yaml                     # `tally` ns, Pod Security Admission = restricted
  networkpolicy-default-deny.yaml    # default-deny ingress+egress, then per-plane allows + DNS
  cored.yaml                         # Deployment + Service + ServiceAccount
  statutoryd.yaml                    # Deployment + Service + ServiceAccount
  insightd.yaml                      # Deployment + Service + ServiceAccount
  kustomization.yaml
overlays/
  dev/
    kustomization.yaml               # dev image tags, single replica
```

Every container sets: resource requests **and** limits, liveness + readiness
probes (`/healthz`, `/readyz`), `runAsNonRoot`, `readOnlyRootFilesystem`, all
capabilities dropped, `seccompProfile: RuntimeDefault`. The namespace enforces
the `restricted` Pod Security Standard.

## Network policy

`default-deny-all` denies all ingress and egress in the namespace. Then:
- `allow-dns-egress` re-opens kube-dns (otherwise name resolution breaks).
- `cored-ingress` accepts HTTP only from the ingress controller namespace.
- `statutoryd-ingress` / `insightd-ingress` accept only from `cored` pods.

## Build / validate

```bash
kustomize build infra/k8s/overlays/dev
# or with kubectl's built-in kustomize:
kubectl kustomize infra/k8s/overlays/dev
```

CI runs this in the `iac` job. Image references (`cored:dev`, etc.) are
placeholders; CI / Argo CD rewrite them to a registry path + immutable digest.
Argo CD watches `overlays/<env>` per environment.
