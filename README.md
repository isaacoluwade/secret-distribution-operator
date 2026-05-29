# secret-distribution-operator

A Kubernetes operator that synchronizes Kubernetes Secrets with AWS Secrets
Manager — purpose-built to **augment** [External Secrets Operator (ESO)][eso]
with platform-enforced path policy, defense-in-depth validation, and a
supply chain you can audit.

> **Status**: production-grade design exhibit. Mirrors the architectural
> pattern of a custom certificate-distribution operator I authored for an
> enterprise multi-tenant Kubernetes platform.

[eso]: https://external-secrets.io/

---

## Why this exists

ESO is excellent at pulling secrets *into* the cluster. It is deliberately
opinionated about *not* enforcing where in the upstream secret store those
secrets live or how they're named. In a multi-tenant platform that's a gap:

- Tenants can write secrets to arbitrary AWS Secrets Manager paths,
  colliding with neighbors or violating the platform's IAM policy boundaries.
- There's no symmetric `Export` flow — nothing to push a Kubernetes-managed
  secret *back* to Secrets Manager for downstream consumers (other clusters,
  non-Kubernetes services, disaster recovery).
- Tenant-authored manifests can drift between cluster reality and the
  upstream store with no admission-time guardrail.

This operator closes those gaps with a small, focused surface: two CRDs, a
strict path policy, and a validating webhook that rejects bad specs before
the reconciler ever sees them.

## Design tradeoffs

| Decision | Alternative considered | Why this won |
|---|---|---|
| Pair with ESO instead of replacing it | Build a generic secret fetcher | ESO's import flow is mature. Build only what's missing: bidirectional sync + path policy. |
| Policy enforcement at admission *and* reconciliation | Admission webhook only | Reconciler defense protects against direct API patches that bypass the webhook (e.g. `kubectl edit` by a cluster-admin). Two layers, single source of truth. |
| Path schema baked into operator + Helm-tunable | Tenant-supplied regex | A tenant who controls their own policy isn't a policy. The path schema is platform-owned. |
| SLSA-aligned supply chain (cosign keyless + SBOM + Rekor) | Plain image push | The whole point of a secret-handling operator is trust. The supply chain has to back it. |
| Finalizers preserve Secrets Manager state on delete | Cascade delete | Deleting a CR shouldn't take out the secret material — a misclick on `kubectl delete` would otherwise cost an incident. |

## What's in the box

Two namespaced CRDs in `platform.mtkp/v1`:

### `ExportSecret`
Pushes a Kubernetes Secret to AWS Secrets Manager at a deterministic path.

```yaml
apiVersion: platform.mtkp/v1
kind: ExportSecret
metadata:
  name: api-credentials
  namespace: app-payments
spec:
  source:
    secretName: api-credentials      # K8s Secret to read
  target:
    path: platform/prod/payments/api-credentials
```

Path must match `^platform/(dev|staging|prod|prod-dr)/[a-z0-9-]+/[a-z0-9-]+$`
and the third segment must equal the resource's namespace. Anything else is
rejected at admission *and* reconciliation.

### `ImportSecret`
Materializes an AWS Secrets Manager secret into a Kubernetes Secret.

```yaml
apiVersion: platform.mtkp/v1
kind: ImportSecret
metadata:
  name: third-party-token
  namespace: app-payments
spec:
  source:
    secretArn: arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/third-party-token-xy7z9q
  target:
    secretName: third-party-token
```

Same path policy applies to the ARN's secret name. Same defense in depth.

## Architecture

```
                  ┌────────────────────────┐
   kubectl apply ─┤ ValidatingAdmission     │  rejected ─► returned to caller
                  │ Webhook (port 9443)     │              with policy diff
                  └────────────┬───────────┘
                               │ admitted
                               ▼
                  ┌────────────────────────┐
                  │ controller-runtime      │
                  │ Reconciler              │
                  │  • path re-validation   │  defense-in-depth — catches
                  │  • finalizer registered │  direct etcd patches that
                  │  • metrics emitted      │  bypass the webhook
                  └─────────┬────────────┬──┘
                            │            │
                            ▼            ▼
                  ┌───────────────┐  ┌─────────────────┐
                  │ K8s Secret    │  │ AWS Secrets Mgr │
                  │ (read/write)  │  │ (IRSA-scoped)   │
                  └───────────────┘  └─────────────────┘
```

Authentication to AWS Secrets Manager uses **IRSA** (IAM Roles for Service
Accounts). The operator's pod identity is the boundary — no static AWS
credentials on disk, no shared admin role. IAM policy on the role caps
which `platform/<env>/*` paths can be read or written.

## Quick start

```bash
make build                       # binary + image
make test                        # controller-runtime envtest
make deploy IMG=<your-acr>/secret-distribution-operator:v0.1.0
```

Helm install:

```bash
helm install secret-dist \
  ./helm-chart \
  --namespace secret-dist-system --create-namespace \
  --set aws.region=us-east-1 \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/secret-dist-operator
```

cert-manager handles webhook TLS automatically (chart includes the
Certificate resource).

## Observability

Three custom Prometheus metrics on `:8080/metrics`:

| Metric | Type | Labels |
|---|---|---|
| `secretdist_reconcile_total` | counter | `kind`, `result` (succeeded\|failed\|skipped) |
| `secretdist_reconcile_duration_seconds` | histogram | `kind` |
| `secretdist_aws_call_duration_seconds` | histogram | `operation` (GetSecretValue\|PutSecretValue\|CreateSecret) |

Plus the standard controller-runtime metrics (queue depth, work-queue
duration, worker count).

## Supply chain

Every release is built by GoReleaser and ships with:

- **Multi-arch images**: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`
- **Cosign keyless signatures**: signed via GitHub OIDC against the
  Sigstore Fulcio CA — no long-lived keys in the repo
- **Rekor transparency log entry** for every signature
- **SBOMs** in SPDX format, attached to each image
- **SLSA Level 3 provenance** generated by the GitHub Actions workflow

Verify a release:

```bash
cosign verify ghcr.io/isaacoluwade/secret-distribution-operator:v0.1.0 \
  --certificate-identity-regexp="https://github.com/isaacoluwade/.+" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"
```

## Releasing

Tag-driven via GoReleaser:

```bash
git tag v0.2.0 && git push origin v0.2.0
```

The workflow builds, signs, generates SBOM + provenance, pushes the image
+ Helm chart, and drafts a GitHub release with the cosign verification
snippet.

## Repository layout

```
secret-distribution-operator/
├── api/                             # CRD type definitions (kubebuilder)
├── internal/                        # reconcilers, webhook, policy, AWS client
├── cmd/                             # main.go
├── config/                          # kustomize manifests (rbac, webhook, crd)
├── helm-chart/                      # Helm chart
├── hack/                            # boilerplate + build helpers
├── .github/workflows/               # CI + GoReleaser release pipeline
├── .goreleaser.yml
├── Dockerfile
├── Makefile
├── PROJECT
├── VERSION
├── CHANGELOG.md
├── go.mod
└── go.sum
```

## License

MIT.
