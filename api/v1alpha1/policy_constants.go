// Package v1alpha1 holds the platform-path policy constants that are shared
// between the ValidatingAdmissionWebhooks in this package and the runtime
// policy checks in internal/controller. They live here (rather than in
// internal/controller) so the webhook side — which must avoid importing
// internal packages — has a stable, public source of truth. Drift between
// admission-time and reconcile-time enforcement is therefore impossible by
// construction.
package v1alpha1

import (
	"fmt"
	"regexp"
	"strings"
)

// PlatformPathPattern is the allowed shape of every secret path the operator
// is willing to touch. Components are:
//
//	platform / <env> / <namespace> / <secret-name>
//
// Where env is one of dev|staging|prod|prod-dr and both the namespace and
// secret-name slugs follow Kubernetes name rules (lower-case alnum + hyphen).
//
// This regex is the single source of truth for the platform-path policy; the
// controller's PolicyCheckExportPath, PolicyCheckImportArn, and the
// ExportSecret/ImportSecret webhook validators all consume it from here.
var PlatformPathPattern = regexp.MustCompile(`^platform/(dev|staging|prod|prod-dr)/([a-z0-9-]+)/([a-z0-9-]+)$`)

// SecretsManagerArnPattern matches the prefix portion of an AWS Secrets
// Manager ARN — through the ":secret:" delimiter but not into the secret
// name itself, which the caller still has to validate against
// PlatformPathPattern. We are deliberately permissive on partition
// (`aws[a-z-]*` covers aws, aws-cn, aws-us-gov, etc.) and region
// (`[a-z0-9-]+`) but strict on the account-id (exactly 12 digits).
var SecretsManagerArnPattern = regexp.MustCompile(`^arn:aws[a-z-]*:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:`)

// KmsKeyArnPattern matches the prefix of a KMS key ARN. As with the SM ARN
// it stops at `key/` rather than constraining the key-id format — both UUIDs
// and aliases (alias/<name>) parse the same way here, and AWS itself is the
// authority on whether the key actually exists.
var KmsKeyArnPattern = regexp.MustCompile(`^arn:aws[a-z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/`)

// ParsedPath is the structured form of a path that matched
// PlatformPathPattern. The fields correspond positionally to the regex's
// capture groups.
type ParsedPath struct {
	Env        string
	Namespace  string
	SecretName string
}

// ParsePlatformPath validates path against PlatformPathPattern and returns
// the parsed components. The error is suitable for surfacing into either a
// PolicyDenied status condition (controller) or an admission-denied response
// (webhook).
func ParsePlatformPath(path string) (ParsedPath, error) {
	m := PlatformPathPattern.FindStringSubmatch(path)
	if m == nil {
		return ParsedPath{}, fmt.Errorf(
			"path %q does not match required pattern platform/<env>/<namespace>/<secret-name> with env in [dev,staging,prod,prod-dr]",
			path,
		)
	}
	return ParsedPath{Env: m[1], Namespace: m[2], SecretName: m[3]}, nil
}

// SecretNameFromArn extracts the secret-name portion of an AWS Secrets
// Manager ARN. The Secrets Manager ARN format is:
//
//	arn:aws:secretsmanager:<region>:<account>:secret:<name>[-<suffix>]
//
// AWS appends a random 6-char suffix when creating secrets via the console;
// our operator-managed secrets are created via the SDK with a clean name and
// no suffix. This function returns the literal text following ":secret:" with
// no further trimming, so callers should run the result through
// PlatformPathPattern which will reject the trailing "-XXXXXX" form.
func SecretNameFromArn(arn string) (string, error) {
	const sep = ":secret:"
	idx := strings.Index(arn, sep)
	if idx < 0 {
		return "", fmt.Errorf("arn %q does not appear to be a Secrets Manager ARN (missing %q)", arn, sep)
	}
	name := arn[idx+len(sep):]
	if name == "" {
		return "", fmt.Errorf("arn %q has empty secret name", arn)
	}
	return name, nil
}
