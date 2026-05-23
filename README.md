# secret-distribution-operator

A Kubernetes operator that synchronizes Kubernetes Secrets with AWS Secrets Manager, augmenting External Secrets Operator (ESO) with platform-specific path policy.

## What it does

Two CRDs:

- `ExportSecret` — reads a Kubernetes Secret and writes its data into AWS Secrets Manager at a path of the form `platform/<env>/<namespace>/<secret-name>`. Creates the SM secret if absent, updates the version on change.
- `ImportSecret` — reads an AWS Secrets Manager secret by ARN and materializes a Kubernetes Secret in the target namespace.

Both controllers use IRSA to talk to AWS.

## Policy

Platform path policy is enforced in `internal/controller/policy.go`:

- The path must match `^platform/(dev|staging|prod|prod-dr)/[a-z0-9-]+/[a-z0-9-]+$`.
- The third component (the namespace slug) must equal the CRD's `metadata.namespace`.

Policy violations set a `PolicyDenied` condition and stop the reconcile before touching AWS or Kubernetes Secrets.

## Quick start

```bash
make manifests generate
make test
make docker-build IMG=ghcr.io/example/secret-distribution-operator:dev
make deploy IMG=ghcr.io/example/secret-distribution-operator:dev
```

## Helm

```bash
helm install sdo helm-chart/secret-distribution-operator \
  --namespace secret-distribution-operator --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/sdo
```

## Finalizer

`platform.mtkp/secret-distribution-finalizer`. On delete, the operator removes its derived Kubernetes Secret (for ImportSecret) but **never deletes the SM secret** — SM secrets outlive the CRD by policy.

## Metrics

Exposed on `:8080/metrics`:

- `secret_distribution_reconcile_duration_seconds`
- `secret_distribution_policy_violations_total`
- `secret_distribution_aws_api_errors_total`

Plus the controller-runtime defaults.
