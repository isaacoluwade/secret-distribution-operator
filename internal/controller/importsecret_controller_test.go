package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretsv1alpha1 "github.com/example/secret-distribution-operator/api/v1alpha1"
)

func TestImportSecret_HappyPath(t *testing.T) {
	s := newTestScheme(t)
	is := &secretsv1alpha1.ImportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
		Spec: secretsv1alpha1.ImportSecretSpec{
			SourceArn:        "arn:aws:secretsmanager:us-east-1:000000000000:secret:platform/prod/payments/db",
			TargetSecretName: "db-credentials",
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(is).WithStatusSubresource(&secretsv1alpha1.ImportSecret{}).Build()

	// Seed AWS with a JSON-shaped value first.
	smFake := newFakeSM()
	w := NewSecretsManagerWrapper(smFake)
	if _, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", []byte(`{"user":"YWRtaW4="}`), "", nil); err != nil {
		t.Fatal(err)
	}

	r := &ImportSecretReconciler{Client: c, Scheme: s, SecretsManager: w}

	// First call adds finalizer.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: "payments"}}); err != nil {
		t.Fatal(err)
	}
	// Second call materializes the K8s Secret.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: "payments"}}); err != nil {
		t.Fatal(err)
	}

	var got corev1.Secret
	if err := c.Get(context.Background(), types.NamespacedName{Name: "db-credentials", Namespace: "payments"}, &got); err != nil {
		t.Fatalf("expected K8s Secret to exist: %v", err)
	}
	if string(got.Data["user"]) != "admin" {
		t.Fatalf("data[user]=%q", string(got.Data["user"]))
	}
	if got.Labels["platform.mtkp/owned-by"] != "ImportSecret" {
		t.Fatalf("missing owned-by label: %v", got.Labels)
	}
}

func TestImportSecret_PolicyDenied_CrossTenant(t *testing.T) {
	s := newTestScheme(t)
	is := &secretsv1alpha1.ImportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments", Finalizers: []string{Finalizer}},
		Spec: secretsv1alpha1.ImportSecretSpec{
			SourceArn:        "arn:aws:secretsmanager:us-east-1:000000000000:secret:platform/prod/wallet/db",
			TargetSecretName: "x",
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(is).WithStatusSubresource(&secretsv1alpha1.ImportSecret{}).Build()
	r := &ImportSecretReconciler{Client: c, Scheme: s, SecretsManager: NewSecretsManagerWrapper(newFakeSM())}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "payments"}}); err != nil {
		t.Fatal(err)
	}

	// K8s secret must NOT have been created.
	var bad corev1.Secret
	err := c.Get(context.Background(), types.NamespacedName{Name: "x", Namespace: "payments"}, &bad)
	if err == nil {
		t.Fatalf("expected target Secret to be absent on policy deny")
	}
}
