package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// fakeSM implements SecretsManagerAPI with an in-memory store keyed by
// SecretId. Each operation records a call so tests can assert on which
// SDK methods fired.
type fakeSM struct {
	values map[string]string // current SecretString per SecretId
	arns   map[string]string // canonical ARN per SecretId
	calls  []string
}

func newFakeSM() *fakeSM {
	return &fakeSM{values: map[string]string{}, arns: map[string]string{}}
}

func (f *fakeSM) DescribeSecret(_ context.Context, in *secretsmanager.DescribeSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	f.calls = append(f.calls, "Describe:"+aws.ToString(in.SecretId))
	id := aws.ToString(in.SecretId)
	arn, ok := f.arns[id]
	if !ok {
		return nil, &smtypes.ResourceNotFoundException{Message: aws.String("not found")}
	}
	return &secretsmanager.DescribeSecretOutput{ARN: aws.String(arn), Name: aws.String(id)}, nil
}

func (f *fakeSM) CreateSecret(_ context.Context, in *secretsmanager.CreateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	f.calls = append(f.calls, "Create:"+aws.ToString(in.Name))
	id := aws.ToString(in.Name)
	if _, exists := f.values[id]; exists {
		return nil, errors.New("already exists")
	}
	arn := "arn:aws:secretsmanager:us-east-1:000000000000:secret:" + id
	f.values[id] = aws.ToString(in.SecretString)
	f.arns[id] = arn
	return &secretsmanager.CreateSecretOutput{ARN: aws.String(arn), VersionId: aws.String("v1")}, nil
}

func (f *fakeSM) PutSecretValue(_ context.Context, in *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	f.calls = append(f.calls, "Put:"+aws.ToString(in.SecretId))
	id := aws.ToString(in.SecretId)
	f.values[id] = aws.ToString(in.SecretString)
	return &secretsmanager.PutSecretValueOutput{ARN: aws.String(f.arns[id]), VersionId: aws.String("v2")}, nil
}

func (f *fakeSM) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.calls = append(f.calls, "Get:"+aws.ToString(in.SecretId))
	id := aws.ToString(in.SecretId)
	v, ok := f.values[id]
	if !ok {
		return nil, &smtypes.ResourceNotFoundException{Message: aws.String("not found")}
	}
	return &secretsmanager.GetSecretValueOutput{
		ARN:          aws.String(f.arns[id]),
		SecretString: aws.String(v),
		VersionId:    aws.String("v-current"),
	}, nil
}

func (f *fakeSM) TagResource(_ context.Context, in *secretsmanager.TagResourceInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.TagResourceOutput, error) {
	f.calls = append(f.calls, "Tag:"+aws.ToString(in.SecretId))
	return &secretsmanager.TagResourceOutput{}, nil
}

func TestEnsureSecretValue_Create(t *testing.T) {
	f := newFakeSM()
	w := NewSecretsManagerWrapper(f)

	res, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", []byte(`{"k":"v"}`), "", nil)
	if err != nil {
		t.Fatalf("EnsureSecretValue: %v", err)
	}
	if !res.Created {
		t.Fatalf("expected Created=true, got %+v", res)
	}
	if res.Arn == "" {
		t.Fatalf("expected non-empty ARN")
	}
}

func TestEnsureSecretValue_NoOpWhenUnchanged(t *testing.T) {
	f := newFakeSM()
	w := NewSecretsManagerWrapper(f)

	val := []byte(`{"k":"v"}`)
	if _, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", val, "", nil); err != nil {
		t.Fatal(err)
	}

	res, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", val, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Fatalf("expected Unchanged=true on second call, got %+v", res)
	}
}

func TestEnsureSecretValue_PutOnChange(t *testing.T) {
	f := newFakeSM()
	w := NewSecretsManagerWrapper(f)

	if _, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", []byte(`{"k":"v1"}`), "", nil); err != nil {
		t.Fatal(err)
	}

	res, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", []byte(`{"k":"v2"}`), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.Unchanged {
		t.Fatalf("expected a fresh Put, got %+v", res)
	}
	if res.VersionId != "v2" {
		t.Fatalf("expected versionId v2, got %q", res.VersionId)
	}
}

func TestGetSecretValue_ParsesJSON(t *testing.T) {
	f := newFakeSM()
	w := NewSecretsManagerWrapper(f)
	if _, err := w.EnsureSecretValue(context.Background(), "platform/prod/payments/db", []byte(`{"user":"YWRtaW4="}`), "", nil); err != nil {
		t.Fatal(err)
	}
	out, err := w.GetSecretValue(context.Background(), "platform/prod/payments/db")
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsJSON {
		t.Fatalf("expected IsJSON=true, got %+v", out)
	}
	if string(out.Parsed["user"]) != "admin" {
		t.Fatalf("expected parsed user=admin, got %q", string(out.Parsed["user"]))
	}
}

func TestGetSecretValue_NotFound(t *testing.T) {
	f := newFakeSM()
	w := NewSecretsManagerWrapper(f)
	_, err := w.GetSecretValue(context.Background(), "platform/prod/payments/missing")
	if err == nil {
		t.Fatalf("expected not-found error")
	}
}
