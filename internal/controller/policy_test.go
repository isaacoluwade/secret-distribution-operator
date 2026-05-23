package controller

import (
	"strings"
	"testing"
)

func TestPolicyCheckExportPath(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		namespace string
		wantErr   string
	}{
		{
			name:      "happy path prod",
			path:      "platform/prod/payments/db-password",
			namespace: "payments",
		},
		{
			name:      "happy path dev",
			path:      "platform/dev/wallet/api-token",
			namespace: "wallet",
		},
		{
			name:      "happy path prod-dr",
			path:      "platform/prod-dr/ledger/k1",
			namespace: "ledger",
		},
		{
			name:      "wrong env",
			path:      "platform/test/payments/db-password",
			namespace: "payments",
			wantErr:   "does not match required pattern",
		},
		{
			name:      "no platform prefix",
			path:      "tenant/prod/payments/db-password",
			namespace: "payments",
			wantErr:   "does not match required pattern",
		},
		{
			name:      "namespace mismatch (cross-tenant)",
			path:      "platform/prod/wallet/db-password",
			namespace: "payments",
			wantErr:   "cross-tenant",
		},
		{
			name:      "uppercase rejected",
			path:      "platform/prod/Payments/db-password",
			namespace: "Payments",
			wantErr:   "does not match required pattern",
		},
		{
			name:      "too few components",
			path:      "platform/prod/payments",
			namespace: "payments",
			wantErr:   "does not match required pattern",
		},
		{
			name:      "too many components",
			path:      "platform/prod/payments/db/password",
			namespace: "payments",
			wantErr:   "does not match required pattern",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := PolicyCheckExportPath(c.path, c.namespace)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestPolicyCheckImportArn(t *testing.T) {
	cases := []struct {
		name      string
		arn       string
		namespace string
		wantErr   string
	}{
		{
			name:      "happy path",
			arn:       "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/payments/db-password",
			namespace: "payments",
		},
		{
			name:      "namespace mismatch",
			arn:       "arn:aws:secretsmanager:us-east-1:123456789012:secret:platform/prod/wallet/db-password",
			namespace: "payments",
			wantErr:   "cross-tenant",
		},
		{
			name:      "not an SM arn",
			arn:       "arn:aws:s3:::my-bucket",
			namespace: "payments",
			wantErr:   "does not appear to be a Secrets Manager ARN",
		},
		{
			name:      "secret name not platform path",
			arn:       "arn:aws:secretsmanager:us-east-1:123456789012:secret:tenant/payments/db",
			namespace: "payments",
			wantErr:   "does not match required pattern",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := PolicyCheckImportArn(c.arn, c.namespace)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestSecretNameFromArn(t *testing.T) {
	got, err := SecretNameFromArn("arn:aws:secretsmanager:eu-west-1:000000000000:secret:platform/dev/x/y")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "platform/dev/x/y" {
		t.Fatalf("got %q", got)
	}
}
