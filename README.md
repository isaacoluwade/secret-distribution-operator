# secret-distribution-operator

A Kubernetes operator that synchronizes Kubernetes Secrets with AWS Secrets Manager, augmenting External Secrets Operator (ESO) with platform-specific path policy.

## What it does

Two CRDs:

- `ExportSecret` — reads a Kubernetes Secret and writes its data into AWS Secrets Manager at a path of the form `platform/<env>/<namespace>/<secret-name>`. Creates the SM secret if absent, updates the version on change.
- `ImportSecret` — reads an AWS Secrets Manager secret by ARN and materializes a Kubernetes Secret in the target namespace.

Both controllers use IRSA to talk to AWS.

## Policy

Platform path policy is enforced in two places that share the same regex
(`PlatformPathPattern`, defined in `api/v1alpha1/policy_constants.go`):

1. A **ValidatingAdmissionWebhook** rejects malformed `ExportSecret` /
   `ImportSecret` resources at admission time — so bad specs never reach
   etcd. See [Webhooks](#webhooks) below.
2. The **reconciler** in `internal/controller/policy.go` re-runs the same
   checks defensively (`PolicyCheckExportPath`, `PolicyCheckImportArn`) and
   surfaces violations through a `PolicyDenied` status condition for any
   pre-existing objects that pre-date the webhook.

Rules:

- The path must match `^platform/(dev|staging|prod|prod-dr)/[a-z0-9-]+/[a-z0-9-]+$`.
- The third component (the namespace slug) must equal the CRD's `metadata.namespace`.

## Webhooks

The operator ships two ValidatingAdmissionWebhooks (one per CRD), both
served on `:9443` from the manager pod.

### ExportSecret validator rejects when:

1. `spec.targetPath` does not match the platform-path regex.
2. The path's namespace segment does not equal `metadata.namespace`.
3. `spec.sourceSecretName` is empty or not a DNS-1123 label.
4. `spec.kmsKeyArn` is set but is not a valid KMS ARN.

### ImportSecret validator rejects when:

1. `spec.sourceArn` is not a Secrets Manager ARN.
2. The arn's secret-name component fails the platform-path regex, or its
   namespace segment does not equal `metadata.namespace`.
3. `spec.targetSecretName` is empty or not a DNS-1123 label.
4. `spec.refreshInterval` is set but is not a Go duration in `[1m, 24h]`.

`ValidateDelete` is a no-op on both — the finalizer in
`internal/controller/exportsecret_controller.go` owns deletion ordering, so
admission must always allow the delete to proceed.

### Certificates

TLS is provisioned by `cert-manager`. The Helm chart creates a self-signed
`Issuer` in the release namespace by default; override
`webhook.certManager.selfSigned: false` and set `webhook.certManager.issuerRef`
to use a cluster-wide CA. cert-manager injects the CA bundle into the
`ValidatingWebhookConfiguration` via the `cert-manager.io/inject-ca-from`
annotation.

### Webhook config

| Setting          | Value                                       |
|------------------|---------------------------------------------|
| Port             | 9443                                        |
| Cert dir         | `/tmp/k8s-webhook-server/serving-certs`     |
| `failurePolicy`  | `Fail`                                      |
| `sideEffects`    | `None`                                      |
| `timeoutSeconds` | `10`                                        |

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

## Releasing

Releases are driven by Git tags of the form `vX.Y.Z`. The
`.github/workflows/release.yml` workflow runs in three stages:

1. **verify** — confirms the tag matches `VERSION` (if present) and that
   `CHANGELOG.md` has a matching `## vX.Y.Z` heading. The matching section
   is extracted to `release-notes.md`.
2. **release** — runs [GoReleaser v2](https://goreleaser.com) against
   [`.goreleaser.yml`](.goreleaser.yml): cross-compiles linux + darwin x
   amd64 + arm64 binaries, builds & pushes multi-arch images to
   `ghcr.io/example/secret-distribution-operator`, generates SPDX-JSON
   SBOMs for every archive and image (via
   [syft](https://github.com/anchore/syft)), signs every image with
   [cosign](https://github.com/sigstore/cosign) keyless (Fulcio + Rekor)
   using the GitHub OIDC token, computes SHA256 checksums, and publishes
   a GitHub release with everything attached.
3. **helm** — packages and pushes the Helm chart to
   `oci://ghcr.io/<owner>/charts`.

Validate the GoReleaser config locally with:

```bash
goreleaser check
goreleaser release --snapshot --clean   # dry run; no push, no sign
```

## Supply chain

The release pipeline is fully [SLSA-aligned](https://slsa.dev/):

- **Signed images.** No private keys exist anywhere. cosign requests a
  short-lived signing certificate from Fulcio that binds the GitHub
  Actions OIDC identity, signs the image, and writes the signature to
  the Rekor transparency log. Verification recomputes the binding.
- **SBOMs.** One SPDX-JSON SBOM per release archive and per docker
  image is attached to the GitHub release as an artifact.
- **Provenance.** GoReleaser embeds build metadata (`-X main.version`,
  `-X main.commit`, `-X main.date`) and the docker images carry
  `org.opencontainers.image.{source,revision,version}` labels.

### Verifying an image

```bash
cosign verify ghcr.io/example/secret-distribution-operator:v1.0.0 \
  --certificate-identity-regexp '^https://github\.com/example/secret-distribution-operator/\.github/workflows/release\.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The `--certificate-identity-regexp` pins the signer to the
`release.yml` workflow on a `v*` tag in this repo. Any other identity —
a different workflow, a different repo, a non-tag ref — fails
verification. No public key is stored; trust comes from Fulcio + Rekor.

### Consuming the SBOM

```bash
gh release download v1.0.0 \
  --repo example/secret-distribution-operator \
  --pattern '*.spdx.json'

# Or attach the image SBOM to the image itself (one-time, signer-side):
syft attest ghcr.io/example/secret-distribution-operator:v1.0.0 \
  --output spdx-json | cosign attest --yes \
  --predicate /dev/stdin \
  --type spdxjson \
  ghcr.io/example/secret-distribution-operator:v1.0.0
```
