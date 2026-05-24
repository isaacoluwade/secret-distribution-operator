// This file wires up the ValidatingAdmissionWebhook for ExportSecret. It
// enforces, at admission time, the same platform-path policy the controller
// re-checks during Reconcile. By rejecting at admission we save the
// PolicyDenied condition round-trip — bad specs never enter etcd.
//
// No Defaulter is registered: ExportSecret has no fields that the operator
// defaults on behalf of the user. (RefreshInterval lives on ImportSecret,
// and the kubebuilder default tag on the CRD schema handles it.)

package v1alpha1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// exportSecretLog is package-scoped so test setup can swap out the sink
// if needed. controller-runtime's default discards in non-manager contexts.
var exportSecretLog = logf.Log.WithName("exportsecret-webhook")

// SetupExportSecretWebhookWithManager registers the validator with the
// manager's webhook server. Called from cmd/main.go alongside the
// reconciler setup.
func SetupExportSecretWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&ExportSecret{}).
		WithValidator(&exportSecretValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-secrets-mtkp-platform-v1alpha1-exportsecret,mutating=false,failurePolicy=fail,sideEffects=None,groups=secrets.mtkp.platform,resources=exportsecrets,verbs=create;update,versions=v1alpha1,name=vexportsecret.kb.io,admissionReviewVersions=v1

// exportSecretValidator implements webhook.CustomValidator for ExportSecret.
// The validator is stateless — the manager creates one instance per process.
type exportSecretValidator struct{}

var _ webhook.CustomValidator = &exportSecretValidator{}

// ValidateCreate runs the full policy check on a freshly submitted
// ExportSecret. Returns an aggregate error listing every violation so the
// user sees them all at once rather than fixing-and-resubmitting in a loop.
func (v *exportSecretValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	es, ok := obj.(*ExportSecret)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ExportSecret, got %T", obj))
	}
	exportSecretLog.V(1).Info("ValidateCreate", "namespace", es.Namespace, "name", es.Name)
	return nil, validateExportSecret(es)
}

// ValidateUpdate re-runs the same checks as Create. ExportSecret has no
// immutable fields per se, but every constraint we enforce on Create must
// continue to hold on Update — otherwise a user could create a valid object
// and then mutate it into a cross-tenant write.
func (v *exportSecretValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	es, ok := newObj.(*ExportSecret)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected ExportSecret, got %T", newObj))
	}
	exportSecretLog.V(1).Info("ValidateUpdate", "namespace", es.Namespace, "name", es.Name)
	return nil, validateExportSecret(es)
}

// ValidateDelete is a no-op: the controller's finalizer is responsible for
// ordering cleanup, and admission-time deletion checks would only make it
// harder to recover stuck resources.
func (v *exportSecretValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateExportSecret encapsulates every rule from the design doc:
//
//  1. spec.targetPath matches PlatformPathPattern.
//  2. The path's namespace component equals metadata.namespace.
//  3. spec.sourceSecretName is a valid DNS-1123 label.
//  4. spec.kmsKeyArn (if set) is a valid KMS ARN.
//
// Returns a StatusError so kubectl prints a clean "denied the request"
// message rather than an unstructured 500.
func validateExportSecret(es *ExportSecret) error {
	gk := schema.GroupKind{Group: GroupVersion.Group, Kind: "ExportSecret"}

	var causes []string

	// Rules 1 & 2: path shape + namespace match.
	parsed, err := ParsePlatformPath(es.Spec.TargetPath)
	if err != nil {
		// Pattern mismatch — no point trying to compare the namespace.
		causes = append(causes, fmt.Sprintf("spec.targetPath: %s", err.Error()))
	} else if parsed.Namespace != es.Namespace {
		causes = append(causes, fmt.Sprintf(
			"spec.targetPath: path namespace component %q does not match metadata.namespace %q (cross-tenant write denied)",
			parsed.Namespace, es.Namespace,
		))
	}

	// Rule 3: sourceSecretName is a DNS-1123 label. The CRD pattern is a
	// best-effort hint; this is the authoritative check.
	if es.Spec.SourceSecretName == "" {
		causes = append(causes, "spec.sourceSecretName: must not be empty")
	} else if errs := validation.IsDNS1123Label(es.Spec.SourceSecretName); len(errs) > 0 {
		causes = append(causes, fmt.Sprintf("spec.sourceSecretName: invalid DNS-1123 label: %v", errs))
	}

	// Rule 4: KMS ARN format (optional).
	if es.Spec.KmsKeyArn != "" && !KmsKeyArnPattern.MatchString(es.Spec.KmsKeyArn) {
		causes = append(causes, fmt.Sprintf(
			"spec.kmsKeyArn: %q is not a valid KMS key ARN (expected arn:aws*:kms:<region>:<acct>:key/<id>)",
			es.Spec.KmsKeyArn,
		))
	}

	if len(causes) == 0 {
		return nil
	}
	return apierrors.NewInvalid(gk, es.Name, fieldErrorsFromCauses(causes))
}
