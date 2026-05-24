// This file wires up the ValidatingAdmissionWebhook for ImportSecret. It
// mirrors exportsecret_webhook.go: same shared platform-path policy
// (PlatformPathPattern) plus an SM-ARN shape check on top, since ImportSecret
// references SM secrets by ARN rather than by path.
//
// No Defaulter is registered — the only defaultable field, RefreshInterval,
// is handled by the CRD's kubebuilder:default tag.

package v1alpha1

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Refresh interval bounds enforced at admission time. The lower bound stops
// users from hammering Secrets Manager and burning IRSA-credentialed
// API quota; the upper bound ensures we still re-converge at least daily,
// which catches rotations done out-of-band.
const (
	minRefreshInterval = 1 * time.Minute
	maxRefreshInterval = 24 * time.Hour
)

var importSecretLog = logf.Log.WithName("importsecret-webhook")

// SetupImportSecretWebhookWithManager registers the validator with the
// manager's webhook server.
func SetupImportSecretWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&ImportSecret{}).
		WithValidator(&importSecretValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-secrets-mtkp-platform-v1alpha1-importsecret,mutating=false,failurePolicy=fail,sideEffects=None,groups=secrets.mtkp.platform,resources=importsecrets,verbs=create;update,versions=v1alpha1,name=vimportsecret.kb.io,admissionReviewVersions=v1

type importSecretValidator struct{}

var _ webhook.CustomValidator = &importSecretValidator{}

func (v *importSecretValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	is, ok := obj.(*ImportSecret)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ImportSecret, got %T", obj))
	}
	importSecretLog.V(1).Info("ValidateCreate", "namespace", is.Namespace, "name", is.Name)
	return nil, validateImportSecret(is)
}

func (v *importSecretValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	is, ok := newObj.(*ImportSecret)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ImportSecret, got %T", newObj))
	}
	importSecretLog.V(1).Info("ValidateUpdate", "namespace", is.Namespace, "name", is.Name)
	return nil, validateImportSecret(is)
}

// ValidateDelete is intentionally a no-op for the same reason as the
// ExportSecret validator: the controller's finalizer owns deletion ordering.
func (v *importSecretValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateImportSecret runs the four rules in the design doc:
//
//  1. spec.sourceArn matches SecretsManagerArnPattern.
//  2. The secret-name component of sourceArn satisfies PlatformPathPattern
//     AND its namespace segment equals metadata.namespace.
//  3. spec.targetSecretName is a valid DNS-1123 label.
//  4. spec.refreshInterval (if set) parses as a time.Duration in
//     [1m, 24h].
func validateImportSecret(is *ImportSecret) error {
	gk := schema.GroupKind{Group: GroupVersion.Group, Kind: "ImportSecret"}

	var causes []string

	// Rule 1: SM ARN shape.
	arnLooksLikeSM := SecretsManagerArnPattern.MatchString(is.Spec.SourceArn)
	if !arnLooksLikeSM {
		causes = append(causes, fmt.Sprintf(
			"spec.sourceArn: %q is not a valid Secrets Manager ARN (expected arn:aws*:secretsmanager:<region>:<acct>:secret:<name>)",
			is.Spec.SourceArn,
		))
	}

	// Rule 2: secret-name → platform path → namespace match. Only run if
	// the ARN prefix already parsed; otherwise the error from rule 1 is
	// sufficient and downstream messages would just be confusing.
	if arnLooksLikeSM {
		name, err := SecretNameFromArn(is.Spec.SourceArn)
		if err != nil {
			causes = append(causes, fmt.Sprintf("spec.sourceArn: %s", err.Error()))
		} else {
			parsed, perr := ParsePlatformPath(name)
			if perr != nil {
				causes = append(causes, fmt.Sprintf("spec.sourceArn: secret name %q: %s", name, perr.Error()))
			} else if parsed.Namespace != is.Namespace {
				causes = append(causes, fmt.Sprintf(
					"spec.sourceArn: secret-name namespace component %q does not match metadata.namespace %q (cross-tenant read denied)",
					parsed.Namespace, is.Namespace,
				))
			}
		}
	}

	// Rule 3: targetSecretName DNS-1123 label.
	if is.Spec.TargetSecretName == "" {
		causes = append(causes, "spec.targetSecretName: must not be empty")
	} else if errs := validation.IsDNS1123Label(is.Spec.TargetSecretName); len(errs) > 0 {
		causes = append(causes, fmt.Sprintf("spec.targetSecretName: invalid DNS-1123 label: %v", errs))
	}

	// Rule 4: refreshInterval bounds (only when explicitly set; the CRD
	// already defaults empty to "1h").
	if is.Spec.RefreshInterval != "" {
		d, err := time.ParseDuration(is.Spec.RefreshInterval)
		switch {
		case err != nil:
			causes = append(causes, fmt.Sprintf(
				"spec.refreshInterval: %q is not a valid Go duration: %v",
				is.Spec.RefreshInterval, err,
			))
		case d < minRefreshInterval:
			causes = append(causes, fmt.Sprintf(
				"spec.refreshInterval: %s is below the minimum of %s",
				d, minRefreshInterval,
			))
		case d > maxRefreshInterval:
			causes = append(causes, fmt.Sprintf(
				"spec.refreshInterval: %s exceeds the maximum of %s",
				d, maxRefreshInterval,
			))
		}
	}

	if len(causes) == 0 {
		return nil
	}
	return apierrors.NewInvalid(gk, is.Name, fieldErrorsFromCauses(causes))
}
