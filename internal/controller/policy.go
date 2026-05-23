// Package controller holds the ExportSecret and ImportSecret reconcilers
// plus the platform path-policy enforcement shared between them.
package controller

import (
	"fmt"
	"regexp"
	"strings"
)

// PlatformPathPattern is the allowed shape of every secret path the operator
// is willing to touch. The components are:
//
//	platform / <env> / <namespace> / <secret-name>
//
// Where env is one of dev|staging|prod|prod-dr and both the namespace and
// secret-name slugs follow Kubernetes name rules (lower-case alnum + hyphen).
var PlatformPathPattern = regexp.MustCompile(`^platform/(dev|staging|prod|prod-dr)/([a-z0-9-]+)/([a-z0-9-]+)$`)

// ParsedPath is the structured form of a path that matched
// PlatformPathPattern. The fields correspond positionally to the regex's
// capture groups.
type ParsedPath struct {
	Env        string
	Namespace  string
	SecretName string
}

// ParsePlatformPath validates path against PlatformPathPattern and returns
// the parsed components. Returns an error suitable for surfacing into a
// PolicyDenied status condition.
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

// PolicyCheckExportPath enforces the platform path policy for ExportSecret.
// The namespace embedded in the path MUST equal the ExportSecret's own
// metadata.namespace — preventing a tenant in namespace A from exporting to
// the SM path reserved for tenant B.
func PolicyCheckExportPath(targetPath, crNamespace string) error {
	parsed, err := ParsePlatformPath(targetPath)
	if err != nil {
		return err
	}
	if parsed.Namespace != crNamespace {
		return fmt.Errorf(
			"path namespace component %q does not match ExportSecret namespace %q (cross-tenant write denied)",
			parsed.Namespace, crNamespace,
		)
	}
	return nil
}

// SecretNameFromArn extracts the secret-name portion of an AWS Secrets
// Manager ARN. The Secrets Manager ARN format is:
//
//	arn:aws:secretsmanager:<region>:<account>:secret:<name>[-<suffix>]
//
// AWS appends a random 6-char suffix when creating secrets via the console;
// our operator-managed secrets are created via the SDK with a clean name and
// no suffix. This function returns the literal text following "secret:" with
// no further trimming, so the caller is responsible for handling either
// shape — we then run the result through PlatformPathPattern which will
// reject the trailing "-XXXXXX" form if present.
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

// PolicyCheckImportArn enforces the platform path policy on the secret-name
// portion of an ImportSecret's SourceArn. The arn's secret name must satisfy
// PlatformPathPattern AND its namespace component must equal the
// ImportSecret's metadata.namespace.
//
// A tenant in namespace `payments-prod` MUST NOT be able to ImportSecret a
// path that decodes to namespace `wallet-prod`.
func PolicyCheckImportArn(sourceArn, crNamespace string) error {
	name, err := SecretNameFromArn(sourceArn)
	if err != nil {
		return err
	}
	parsed, err := ParsePlatformPath(name)
	if err != nil {
		return err
	}
	if parsed.Namespace != crNamespace {
		return fmt.Errorf(
			"arn secret-name namespace component %q does not match ImportSecret namespace %q (cross-tenant read denied)",
			parsed.Namespace, crNamespace,
		)
	}
	return nil
}
