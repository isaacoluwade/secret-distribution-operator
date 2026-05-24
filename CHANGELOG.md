# Changelog

All notable changes to `secret-distribution-operator` are documented here.
Format based on [Keep a Changelog](https://keepachangelog.com/); versioning
follows [SemVer](https://semver.org/).

This operator is pre-1.0. The API may break between minor versions; we will
ship a 1.0.0 once the `ExportSecret` and `ImportSecret` CRDs are stable enough
that downstream consumers can pin without expecting churn.

## [0.1.0] - 2026-05-24

### Added

- **ExportSecret CRD** (`api/v1alpha1/exportsecret_types.go`). Reads a
  Kubernetes Secret and writes it into AWS Secrets Manager at a
  platform-policy-derived path (`platform/<env>/<namespace>/<secret-name>`).
  Spec: `sourceSecretName`, `targetPath`, optional `kmsKeyArn`, optional
  `tags`. Status carries condition list + observed generation.
- **ImportSecret CRD** (`api/v1alpha1/importsecret_types.go`). Reads an
  AWS SM secret by ARN and materializes it as a Kubernetes Secret in the
  CR's namespace. Spec: `sourceArn`, `targetSecretName`, optional
  `refreshInterval`. Augments External Secrets Operator with platform-specific
  policy that ESO can't express.
- **Two reconcilers** (`internal/controller/exportsecret_controller.go`,
  `internal/controller/importsecret_controller.go`) wired with their own
  per-CRD finalizer (`platform.caas/secret-distribution-finalizer`).
- **Platform-path policy enforcement** (`internal/controller/policy.go`):
  `targetPath` (ExportSecret) and the parsed name component of `sourceArn`
  (ImportSecret) must match `^platform/(dev|staging|prod|prod-dr)/[a-z0-9-]+/[a-z0-9-]+$`
  AND the namespace component must equal the CR's `metadata.namespace`.
  Cross-tenant secret access is denied before any AWS call is made.
- **AWS SDK v2 wrapper** (`internal/controller/secretsmanager.go`):
  `EnsureSecretValue` (Describe → Create-or-Get → compare → Put with
  `Created` / `Unchanged` flags) and `GetSecretValue` (returns parsed
  JSON when applicable). Backed by an interface so tests can drop in
  an in-memory fake.
- **Prometheus metrics** (`internal/metrics/metrics.go`):
  `secret_distribution_reconcile_duration_seconds`,
  `secret_distribution_policy_violations_total`, and
  `secret_distribution_aws_api_errors_total`.
- **Two ValidatingAdmissionWebhooks** — one per CRD
  (`api/v1alpha1/exportsecret_webhook.go`, `api/v1alpha1/importsecret_webhook.go`).
  Reject invalid CRs at admission time, eliminating the
  "PolicyDenied status condition" round-trip.
  - ExportSecret rejects: malformed `targetPath`, namespace mismatch, empty
    or non-DNS-1123 `sourceSecretName`, malformed `kmsKeyArn`.
  - ImportSecret rejects: non-SM `sourceArn`, name-component mismatch,
    empty or non-DNS-1123 `targetSecretName`, unparseable `refreshInterval`
    or one outside the `[1m, 24h]` range.
- **Shared platform-path constant**: `PlatformPathPattern`,
  `SecretsManagerArnPattern`, and `KmsKeyArnPattern` moved from
  `internal/controller/policy.go` into `api/v1alpha1/policy_constants.go` so
  the webhook validators and the controller's policy check use a single
  source of truth (no admission/reconcile drift). `internal/controller/policy.go`
  re-exports the names as aliases so existing controller tests and callers
  keep compiling unchanged.
- **30 table-driven tests** across both webhooks covering every validator
  rule + happy paths.
- **Finalizer policy**: deletion of an ExportSecret removes the finalizer
  but does NOT delete the SM secret (operator policy — SM secrets outlive
  the CRD; too risky to auto-delete). Deletion of an ImportSecret removes
  the materialized K8s Secret only if it carries the
  `platform.caas/owned-by=ImportSecret` label.
- **Supply-chain release tooling**: `.goreleaser.yml` builds multi-arch
  binaries + container image, signs the image with `cosign` keyless via
  GitHub OIDC, generates SPDX SBOMs, attaches everything to the release.
  `.github/workflows/release.yml` runs goreleaser then pushes the Helm chart
  to `oci://ghcr.io/example/charts`. Dockerfile carries OCI source/revision/
  version labels.
- **Helm chart** (`helm-chart/secret-distribution-operator/`) — CRDs,
  RBAC, Deployment, ServiceAccount (IRSA-annotated), webhook
  ValidatingWebhookConfigurations + cert-manager Certificate when
  `webhook.enabled = true`.

### Module contract (initial)

- CRD group: `secrets.caas.platform/v1alpha1`
- Kinds: `ExportSecret`, `ImportSecret`
- Both CRDs are cluster-scoped only insofar as they live in a tenant
  namespace; the platform-path policy enforces tenant boundary.
- Finalizer (both): `platform.caas/secret-distribution-finalizer`
- Webhook port: 9443
- Metrics port: 8443

[0.1.0]: https://github.com/example/secret-distribution-operator/releases/tag/v0.1.0
