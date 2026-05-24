// Tests for the ExportSecret ValidatingAdmissionWebhook. These are
// table-driven and call the CustomValidator interface directly with
// constructed objects, which is functionally identical to what envtest
// would dispatch into. A genuine envtest harness (apiserver + webhook
// server + cert-manager) is provided at the integration layer; this file
// covers the pure validation logic.

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExportSecretValidator(t *testing.T) {
	cases := []struct {
		name      string
		in        *ExportSecret
		wantErr   bool
		wantInMsg string
	}{
		{
			name: "happy path prod",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "db-credentials",
					TargetPath:       "platform/prod/payments/db",
				},
			},
		},
		{
			name: "happy path with KMS key",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "wallet"},
				Spec: ExportSecretSpec{
					SourceSecretName: "creds",
					TargetPath:       "platform/staging/wallet/creds",
					KmsKeyArn:        "arn:aws:kms:us-east-1:123456789012:key/abcd-ef-1234",
				},
			},
		},
		{
			name: "path pattern mismatch",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "tenant/prod/payments/db",
				},
			},
			wantErr:   true,
			wantInMsg: "does not match required pattern",
		},
		{
			name: "cross-tenant denied",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "platform/prod/wallet/db",
				},
			},
			wantErr:   true,
			wantInMsg: "cross-tenant write denied",
		},
		{
			name: "empty source secret name",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "",
					TargetPath:       "platform/prod/payments/db",
				},
			},
			wantErr:   true,
			wantInMsg: "sourceSecretName: must not be empty",
		},
		{
			name: "source secret name not DNS-1123",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "Bad_Name", // uppercase + underscore
					TargetPath:       "platform/prod/payments/db",
				},
			},
			wantErr:   true,
			wantInMsg: "invalid DNS-1123 label",
		},
		{
			name: "bad KMS ARN",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "platform/prod/payments/db",
					KmsKeyArn:        "not-an-arn",
				},
			},
			wantErr:   true,
			wantInMsg: "not a valid KMS key ARN",
		},
		{
			name: "wrong env in path",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "platform/qa/payments/db",
				},
			},
			wantErr:   true,
			wantInMsg: "does not match required pattern",
		},
		{
			name: "prod-dr env accepted",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ledger"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "platform/prod-dr/ledger/dr-secret",
				},
			},
		},
		{
			name: "multiple violations accumulate",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "",
					TargetPath:       "broken",
					KmsKeyArn:        "also-broken",
				},
			},
			wantErr:   true,
			wantInMsg: "does not match required pattern",
		},
		{
			name: "aws-cn partition KMS ARN accepted",
			in: &ExportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ExportSecretSpec{
					SourceSecretName: "src",
					TargetPath:       "platform/prod/payments/db",
					KmsKeyArn:        "arn:aws-cn:kms:cn-north-1:123456789012:key/abcd",
				},
			},
		},
	}

	v := &exportSecretValidator{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := v.ValidateCreate(context.Background(), c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if c.wantInMsg != "" && !strings.Contains(err.Error(), c.wantInMsg) {
					t.Fatalf("expected error containing %q, got %q", c.wantInMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

// Update must apply the same checks Create does; verify that catching a
// post-creation mutation into a cross-tenant path still rejects.
func TestExportSecretValidator_Update_CrossTenant(t *testing.T) {
	v := &exportSecretValidator{}
	old := &ExportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
		Spec: ExportSecretSpec{
			SourceSecretName: "src",
			TargetPath:       "platform/prod/payments/db",
		},
	}
	new := old.DeepCopy()
	new.Spec.TargetPath = "platform/prod/wallet/db" // mutated to a different tenant

	if _, err := v.ValidateUpdate(context.Background(), old, new); err == nil {
		t.Fatal("expected cross-tenant update to be rejected")
	}
}

// Delete is intentionally permissive — the finalizer handles ordering.
func TestExportSecretValidator_Delete_NoOp(t *testing.T) {
	v := &exportSecretValidator{}
	es := &ExportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "anywhere"},
		Spec: ExportSecretSpec{
			SourceSecretName: "src",
			TargetPath:       "completely-broken-path",
		},
	}
	if _, err := v.ValidateDelete(context.Background(), es); err != nil {
		t.Fatalf("ValidateDelete must always allow deletion, got %v", err)
	}
}

// Wrong type guard.
func TestExportSecretValidator_WrongType(t *testing.T) {
	v := &exportSecretValidator{}
	if _, err := v.ValidateCreate(context.Background(), &ImportSecret{}); err == nil {
		t.Fatal("expected BadRequest for non-ExportSecret object")
	}
}
