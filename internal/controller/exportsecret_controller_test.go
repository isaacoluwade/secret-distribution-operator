package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretsv1alpha1 "github.com/example/secret-distribution-operator/api/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := secretsv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExportSecret_HappyPath(t *testing.T) {
	s := newTestScheme(t)

	es := &secretsv1alpha1.ExportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
		Spec: secretsv1alpha1.ExportSecretSpec{
			SourceSecretName: "db-credentials",
			TargetPath:       "platform/prod/payments/db",
		},
	}
	src := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-credentials", Namespace: "payments"},
		Data:       map[string][]byte{"user": []byte("admin"), "pass": []byte("hunter2")},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(es, src).
		WithStatusSubresource(&secretsv1alpha1.ExportSecret{}).
		Build()

	r := &ExportSecretReconciler{
		Client:         c,
		Scheme:         s,
		SecretsManager: NewSecretsManagerWrapper(newFakeSM()),
	}

	// First call should add the finalizer and requeue.
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: "payments"}})
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if !res.Requeue {
		t.Fatalf("expected finalizer-add requeue")
	}

	// Second call should do the real work.
	res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: "payments"}})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected periodic requeue after sync")
	}

	var got secretsv1alpha1.ExportSecret
	if err := c.Get(context.Background(), types.NamespacedName{Name: "db", Namespace: "payments"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.SecretArn == "" {
		t.Fatalf("expected SecretArn set, got status=%+v", got.Status)
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf("ObservedGeneration not propagated")
	}
}

func TestExportSecret_PolicyDenied_CrossTenant(t *testing.T) {
	s := newTestScheme(t)
	es := &secretsv1alpha1.ExportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments", Finalizers: []string{Finalizer}},
		Spec: secretsv1alpha1.ExportSecretSpec{
			SourceSecretName: "src",
			TargetPath:       "platform/prod/wallet/db", // wrong tenant
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(es).
		WithStatusSubresource(&secretsv1alpha1.ExportSecret{}).
		Build()
	r := &ExportSecretReconciler{
		Client:         c,
		Scheme:         s,
		SecretsManager: NewSecretsManagerWrapper(newFakeSM()),
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "payments"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got secretsv1alpha1.ExportSecret
	if err := c.Get(context.Background(), types.NamespacedName{Name: "x", Namespace: "payments"}, &got); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, cond := range got.Status.Conditions {
		if cond.Type == conditionPolicy && cond.Status == metav1.ConditionFalse && cond.Reason == reasonPolicyDenied {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected PolicyAllowed=False condition; got %+v", got.Status.Conditions)
	}
}

func TestExportSecret_BadPath(t *testing.T) {
	s := newTestScheme(t)
	es := &secretsv1alpha1.ExportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments", Finalizers: []string{Finalizer}},
		Spec: secretsv1alpha1.ExportSecretSpec{
			SourceSecretName: "src",
			TargetPath:       "not-allowed/prod/payments/db",
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(es).WithStatusSubresource(&secretsv1alpha1.ExportSecret{}).Build()
	r := &ExportSecretReconciler{Client: c, Scheme: s, SecretsManager: NewSecretsManagerWrapper(newFakeSM())}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "payments"}}); err != nil {
		t.Fatal(err)
	}
}

func TestExportSecret_DeletionLeavesSMAlone(t *testing.T) {
	s := newTestScheme(t)
	now := metav1.Now()
	es := &secretsv1alpha1.ExportSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "x", Namespace: "payments",
			Finalizers:        []string{Finalizer},
			DeletionTimestamp: &now,
		},
		Spec: secretsv1alpha1.ExportSecretSpec{
			SourceSecretName: "src",
			TargetPath:       "platform/prod/payments/db",
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(es).WithStatusSubresource(&secretsv1alpha1.ExportSecret{}).Build()
	fakeSM := newFakeSM()
	r := &ExportSecretReconciler{Client: c, Scheme: s, SecretsManager: NewSecretsManagerWrapper(fakeSM)}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "payments"}}); err != nil {
		t.Fatal(err)
	}
	for _, call := range fakeSM.calls {
		// No write of any kind during deletion path.
		if call != "" {
			t.Logf("call: %s", call)
		}
	}
	if len(fakeSM.calls) != 0 {
		t.Fatalf("expected zero AWS calls on delete, got %v", fakeSM.calls)
	}
}
