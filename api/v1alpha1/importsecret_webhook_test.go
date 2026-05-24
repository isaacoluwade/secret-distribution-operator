// Tests for the ImportSecret ValidatingAdmissionWebhook. Same shape as
// the ExportSecret tests — direct CustomValidator calls, table-driven.

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestImportSecretValidator(t *testing.T) {
	cases := []struct {
		name      string
		in        *ImportSecret
		wantErr   bool
		wantInMsg string
	}{
		{
			name: "happy path",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "db-credentials",
					RefreshInterval:  "30m",
				},
			},
		},
		{
			name: "happy path default refresh",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "wallet"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:eu-west-1:000000000000:secret:platform/dev/wallet/api-token",
					TargetSecretName: "api-token",
					// RefreshInterval empty → CRD default applies; webhook
					// should not reject.
				},
			},
		},
		{
			name: "not an SM arn",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:s3:::my-bucket",
					TargetSecretName: "x",
				},
			},
			wantErr:   true,
			wantInMsg: "not a valid Secrets Manager ARN",
		},
		{
			name: "arn missing region",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager::123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
				},
			},
			wantErr:   true,
			wantInMsg: "not a valid Secrets Manager ARN",
		},
		{
			name: "secret name not platform path",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:tenant/payments/db",
					TargetSecretName: "x",
				},
			},
			wantErr:   true,
			wantInMsg: "does not match required pattern",
		},
		{
			name: "cross-tenant read",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/wallet/db",
					TargetSecretName: "x",
				},
			},
			wantErr:   true,
			wantInMsg: "cross-tenant read denied",
		},
		{
			name: "empty target secret name",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "",
				},
			},
			wantErr:   true,
			wantInMsg: "targetSecretName: must not be empty",
		},
		{
			name: "target secret name not DNS-1123",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "Bad_Name",
				},
			},
			wantErr:   true,
			wantInMsg: "invalid DNS-1123 label",
		},
		{
			name: "refresh interval unparseable",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
					RefreshInterval:  "soon",
				},
			},
			wantErr:   true,
			wantInMsg: "not a valid Go duration",
		},
		{
			name: "refresh interval too short",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
					RefreshInterval:  "30s",
				},
			},
			wantErr:   true,
			wantInMsg: "below the minimum",
		},
		{
			name: "refresh interval too long",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
					RefreshInterval:  "72h",
				},
			},
			wantErr:   true,
			wantInMsg: "exceeds the maximum",
		},
		{
			name: "refresh interval at minimum",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
					RefreshInterval:  "1m",
				},
			},
		},
		{
			name: "refresh interval at maximum",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
					RefreshInterval:  "24h",
				},
			},
		},
		{
			name: "aws-us-gov partition accepted",
			in: &ImportSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "payments"},
				Spec: ImportSecretSpec{
					SourceArn:        "arn:aws-us-gov:secretsmanager:us-gov-west-1:123456789012:secret:platform/prod/payments/db",
					TargetSecretName: "x",
				},
			},
		},
	}

	v := &importSecretValidator{}
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

func TestImportSecretValidator_Update_CrossTenant(t *testing.T) {
	v := &importSecretValidator{}
	old := &ImportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "payments"},
		Spec: ImportSecretSpec{
			SourceArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db",
			TargetSecretName: "db",
		},
	}
	new := old.DeepCopy()
	new.Spec.SourceArn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/wallet/db"

	if _, err := v.ValidateUpdate(context.Background(), old, new); err == nil {
		t.Fatal("expected cross-tenant update to be rejected")
	}
}

func TestImportSecretValidator_Delete_NoOp(t *testing.T) {
	v := &importSecretValidator{}
	is := &ImportSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "anywhere"},
		Spec: ImportSecretSpec{
			SourceArn:        "totally-not-an-arn",
			TargetSecretName: "Bad_Name",
		},
	}
	if _, err := v.ValidateDelete(context.Background(), is); err != nil {
		t.Fatalf("ValidateDelete must always allow deletion, got %v", err)
	}
}

func TestImportSecretValidator_WrongType(t *testing.T) {
	v := &importSecretValidator{}
	if _, err := v.ValidateCreate(context.Background(), &ExportSecret{}); err == nil {
		t.Fatal("expected BadRequest for non-ImportSecret object")
	}
}
