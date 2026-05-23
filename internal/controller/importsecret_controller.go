package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// ImportSecretReconciler reads an AWS Secrets Manager secret by ARN and
// materializes it as a Kubernetes Secret in the same namespace as the
// ImportSecret. The arn's path must satisfy platform policy AND its
// namespace component must equal the ImportSecret's namespace.
type ImportSecretReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	SecretsManager *SecretsManagerWrapper
}

// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=importsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=importsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.mtkp.platform,resources=importsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile orchestrates the AWS → K8s sync flow. See ExportSecretReconciler
// for the parallel write-side flow.
func (r *ImportSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("importsecret", req.NamespacedName)
	start := time.Now()
	outcome := "success"
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("ImportSecret", outcome).Observe(time.Since(start).Seconds())
	}()

	var is secretsv1alpha1.ImportSecret
	if err := r.Get(ctx, req.NamespacedName, &is); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !is.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&is, Finalizer) {
			// Delete the materialized K8s Secret if we created one; we use
			// the operator-owned label as the safety check.
			target := &corev1.Secret{}
			err := r.Get(ctx, types.NamespacedName{Namespace: is.Namespace, Name: is.Spec.TargetSecretName}, target)
			if err == nil && target.Labels["platform.mtkp/owned-by"] == "ImportSecret" {
				if err := r.Delete(ctx, target); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("delete target secret: %w", err)
				}
			}
			logger.Info("Removing finalizer; SM secret left in place by policy", "sourceArn", is.Spec.SourceArn)
			controllerutil.RemoveFinalizer(&is, Finalizer)
			return ctrl.Result{}, r.Update(ctx, &is)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&is, Finalizer) {
		controllerutil.AddFinalizer(&is, Finalizer)
		if err := r.Update(ctx, &is); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Policy.
	if err := PolicyCheckImportArn(is.Spec.SourceArn, is.Namespace); err != nil {
		outcome = "policy_denied"
		metrics.PolicyViolationsTotal.WithLabelValues("ImportSecret", classifyPolicyReason(err)).Inc()
		setCondition(&is.Status.Conditions, conditionPolicy, metav1.ConditionFalse, reasonPolicyDenied, err.Error(), is.Generation)
		setCondition(&is.Status.Conditions, conditionReady, metav1.ConditionFalse, reasonPolicyDenied, "policy denied; see PolicyAllowed condition", is.Generation)
		is.Status.ObservedGeneration = is.Generation
		return ctrl.Result{}, r.Status().Update(ctx, &is)
	}
	setCondition(&is.Status.Conditions, conditionPolicy, metav1.ConditionTrue, reasonReconciled, "arn policy satisfied", is.Generation)

	// Read from AWS.
	fetched, err := r.SecretsManager.GetSecretValue(ctx, is.Spec.SourceArn)
	if err != nil {
		outcome = "error"
		metrics.AWSAPIErrorsTotal.WithLabelValues("GetSecretValue", classifyAWSErr(err)).Inc()
		setCondition(&is.Status.Conditions, conditionAWSReachable, metav1.ConditionFalse, reasonAWSError, err.Error(), is.Generation)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, &is)
	}
	setCondition(&is.Status.Conditions, conditionAWSReachable, metav1.ConditionTrue, reasonReconciled, "AWS Secrets Manager reachable", is.Generation)

	// Materialize K8s Secret.
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      is.Spec.TargetSecretName,
			Namespace: is.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, target, func() error {
		if target.Labels == nil {
			target.Labels = map[string]string{}
		}
		target.Labels["platform.mtkp/owned-by"] = "ImportSecret"
		target.Labels["platform.mtkp/import-secret"] = is.Name
		if fetched.IsJSON {
			target.Data = fetched.Parsed
		} else {
			target.Data = map[string][]byte{"value": fetched.Value}
		}
		return controllerutil.SetControllerReference(&is, target, r.Scheme)
	}); err != nil {
		outcome = "error"
		return ctrl.Result{}, fmt.Errorf("create-or-update target secret: %w", err)
	}

	now := metav1.Now()
	is.Status.LastSyncedTime = &now
	is.Status.ObservedGeneration = is.Generation
	is.Status.SourceVersionId = fetched.VersionId
	setCondition(&is.Status.Conditions, conditionReady, metav1.ConditionTrue, reasonReconciled, "imported from Secrets Manager", is.Generation)
	if err := r.Status().Update(ctx, &is); err != nil {
		outcome = "error"
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: parseRefreshInterval(is.Spec.RefreshInterval)}, nil
}

// parseRefreshInterval converts the CRD's RefreshInterval string into a
// duration, defaulting to 1h if empty or unparseable. We intentionally
// keep the default permissive rather than failing reconcile on bad input —
// the OpenAPI schema can be tightened later with a regex.
func parseRefreshInterval(s string) time.Duration {
	if s == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return time.Hour
	}
	return d
}

// SetupWithManager registers the controller with the manager.
func (r *ImportSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1alpha1.ImportSecret{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
