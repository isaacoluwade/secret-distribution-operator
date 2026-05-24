// Package controller holds the ExportSecret and ImportSecret reconcilers
// plus the platform path-policy enforcement shared between them.
//
// The platform path *regex* and the low-level parse helpers
// (ParsedPath / ParsePlatformPath / SecretNameFromArn) now live in
// github.com/example/secret-distribution-operator/api/v1alpha1 so that the
// ValidatingAdmissionWebhooks defined in that package can share a single
// source of truth with the controller. The PolicyCheck* functions below
// are the controller-facing entry points; they layer the namespace-match
// (cross-tenant) check on top of the api package's parsers.
package controller

import (
	"fmt"

	secretsv1alpha1 "github.com/example/secret-distribution-operator/api/v1alpha1"
)

// PlatformPathPattern is re-exported here so existing controller tests and
// any external consumers that referenced controller.PlatformPathPattern keep
// compiling. The variable itself is owned by api/v1alpha1.
var PlatformPathPattern = secretsv1alpha1.PlatformPathPattern

// ParsedPath aliases the api-package type for the same reason as above.
type ParsedPath = secretsv1alpha1.ParsedPath

// ParsePlatformPath delegates to api/v1alpha1.ParsePlatformPath.
func ParsePlatformPath(path string) (ParsedPath, error) {
	return secretsv1alpha1.ParsePlatformPath(path)
}

// SecretNameFromArn delegates to api/v1alpha1.SecretNameFromArn.
func SecretNameFromArn(arn string) (string, error) {
	return secretsv1alpha1.SecretNameFromArn(arn)
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
