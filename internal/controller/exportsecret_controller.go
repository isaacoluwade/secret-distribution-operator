package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	secretsv1alpha1 "github.com/example/secret-distribution-operator/api/v1alpha1"
	"github.com/example/secret-distribution-operator/internal/metrics"
)

// Finalizer is the single finalizer this operator owns for both CRDs. By
// design it does NOT trigger deletion of the AWS SM secret on cleanup —
// SM secrets outlive the CRD because auto-deleting platform secrets has
// caused real outages on other teams' operators.
const Finalizer = "platform.mtkp/secret-distribution-finalizer"

const (
	conditionReady        = "Ready"
	conditionPolicy       = "PolicyAllowed"
	conditionAWSReachable = "AWSReachable"

	reasonReconciled    = "Reconciled"
	reasonPolicyDenied  = "PolicyDenied"
	reasonAWSError      = "AWSError"
	reasonSourceMissing = "SourceSecretMissing"
)

// ExportSecretReconciler synchronizes a Kubernetes Secret's data into AWS
// Secrets Manager at a platform-policy-derived path. The Reconcile method
// is structured as a small set of named steps for legibility.
type ExportSecretReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	SecretsManager *SecretsManagerWrapper
}

// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=exportsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=exportsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=exportsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is the entry-point invoked by controller-runtime whenever an
// ExportSecret changes (or on requeue). Flow:
//
//  1. Fetch the ExportSecret CR. If gone, no-op.
//  2. Handle deletion via finalizer (NO SM deletion — see Finalizer doc).
//  3. Enforce platform path policy (target path shape + namespace match).
//  4. Read the source K8s Secret in this namespace.
//  5. Ensure SM has the secret at TargetPath with the current value, using
//     CreateSecret-or-PutSecretValue idempotently.
//  6. Update status (conditions, LastSyncedTime, ObservedGeneration).
//  7. Requeue after RequeueAfterExport so we re-converge regularly.
func (r *ExportSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("exportsecret", req.NamespacedName)
	start := time.Now()
	outcome := "success"
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("ExportSecret", outcome).Observe(time.Since(start).Seconds())
	}()

	var es secretsv1alpha1.ExportSecret
	if err := r.Get(ctx, req.NamespacedName, &es); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Deletion handling: the operator intentionally does not delete the
	// SM secret. We only remove the finalizer so K8s GC can proceed.
	if !es.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&es, Finalizer) {
			logger.Info("Removing finalizer; SM secret left in place by policy", "targetPath", es.Spec.TargetPath)
			controllerutil.RemoveFinalizer(&es, Finalizer)
			return ctrl.Result{}, r.Update(ctx, &es)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&es, Finalizer) {
		controllerutil.AddFinalizer(&es, Finalizer)
		if err := r.Update(ctx, &es); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 3. Policy.
	if err := PolicyCheckExportPath(es.Spec.TargetPath, es.Namespace); err != nil {
		outcome = "policy_denied"
		metrics.PolicyViolationsTotal.WithLabelValues("ExportSecret", classifyPolicyReason(err)).Inc()
		r.setPolicyDenied(&es, err)
		// Do not requeue automatically — user needs to fix spec; we'll see
		// the change as a generation bump.
		return ctrl.Result{}, r.Status().Update(ctx, &es)
	}
	setCondition(&es.Status.Conditions, conditionPolicy, metav1.ConditionTrue, reasonReconciled, "path policy satisfied", es.Generation)

	// 4. Read source K8s Secret.
	var source corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: es.Namespace, Name: es.Spec.SourceSecretName}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			outcome = "error"
			setCondition(&es.Status.Conditions, conditionReady, metav1.ConditionFalse, reasonSourceMissing,
				fmt.Sprintf("source Secret %s/%s not found", es.Namespace, es.Spec.SourceSecretName), es.Generation)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, &es)
		}
		outcome = "error"
		return ctrl.Result{}, fmt.Errorf("get source secret: %w", err)
	}

	// 5. Encode and push to AWS.
	encoded, err := EncodeSecretData(source.Data)
	if err != nil {
		outcome = "error"
		return ctrl.Result{}, err
	}

	result, err := r.SecretsManager.EnsureSecretValue(ctx, es.Spec.TargetPath, encoded, es.Spec.KmsKeyArn, es.Spec.Tags)
	if err != nil {
		outcome = "error"
		metrics.AWSAPIErrorsTotal.WithLabelValues("EnsureSecretValue", classifyAWSErr(err)).Inc()
		setCondition(&es.Status.Conditions, conditionAWSReachable, metav1.ConditionFalse, reasonAWSError, err.Error(), es.Generation)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, &es)
	}
	setCondition(&es.Status.Conditions, conditionAWSReachable, metav1.ConditionTrue, reasonReconciled, "AWS Secrets Manager reachable", es.Generation)

	// 6. Status.
	now := metav1.Now()
	es.Status.LastSyncedTime = &now
	es.Status.ObservedGeneration = es.Generation
	if result.Arn != "" {
		es.Status.SecretArn = result.Arn
	}
	if result.VersionId != "" {
		es.Status.VersionId = result.VersionId
	}
	msg := "exported to Secrets Manager"
	switch {
	case result.Created:
		msg = "created in Secrets Manager"
	case result.Unchanged:
		msg = "value unchanged; skipped PutSecretValue"
	}
	setCondition(&es.Status.Conditions, conditionReady, metav1.ConditionTrue, reasonReconciled, msg, es.Generation)

	if err := r.Status().Update(ctx, &es); err != nil {
		outcome = "error"
		return ctrl.Result{}, err
	}

	// 7. Requeue for periodic re-convergence.
	return ctrl.Result{RequeueAfter: defaultExportRequeue}, nil
}

const defaultExportRequeue = 10 * time.Minute

// setPolicyDenied is a small helper that flips the Ready and PolicyAllowed
// conditions to False with a PolicyDenied reason and a copy of the
// underlying error in the message.
func (r *ExportSecretReconciler) setPolicyDenied(es *secretsv1alpha1.ExportSecret, err error) {
	setCondition(&es.Status.Conditions, conditionPolicy, metav1.ConditionFalse, reasonPolicyDenied, err.Error(), es.Generation)
	setCondition(&es.Status.Conditions, conditionReady, metav1.ConditionFalse, reasonPolicyDenied, "policy denied; see PolicyAllowed condition", es.Generation)
	es.Status.ObservedGeneration = es.Generation
}

// SetupWithManager registers the controller with the manager.
func (r *ExportSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1alpha1.ExportSecret{}).
		Complete(r)
}

// classifyPolicyReason maps the free-text policy error into a stable label
// value for the policy_violations_total metric. The classification is
// substring-based to avoid coupling metrics to wording changes.
func classifyPolicyReason(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, nil):
		return "none"
	case containsAny(msg, "cross-tenant", "namespace component"):
		return "namespace_mismatch"
	case containsAny(msg, "does not match required pattern", "does not appear to be a Secrets Manager ARN"):
		return "bad_path"
	default:
		return "other"
	}
}

// classifyAWSErr produces a short class label for the AWS-error metric. The
// AWS SDK v2 returns typed errors; here we keep things simple and recognize
// the strings that the wrapper surfaces. Production code might unwrap with
// errors.As against the smithy ResponseError type.
func classifyAWSErr(err error) string {
	if err == nil {
		return "none"
	}
	msg := err.Error()
	switch {
	case containsAny(msg, "ResourceNotFound", "not found"):
		return "not_found"
	case containsAny(msg, "AccessDenied", "access denied"):
		return "access_denied"
	case containsAny(msg, "Throttling", "throttle"):
		return "throttled"
	default:
		return "other"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// setCondition is a thin wrapper around meta.SetStatusCondition that
// stamps ObservedGeneration and forces a non-nil LastTransitionTime.
func setCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string, gen int64) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: gen,
	})
}
