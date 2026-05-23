package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// SecretsManagerAPI is the minimum subset of the AWS Secrets Manager SDK
// surface that this operator depends on. Defining it as an interface (rather
// than taking a *secretsmanager.Client directly) lets unit tests substitute
// an in-memory fake without spinning up real AWS or moto.
type SecretsManagerAPI interface {
	DescribeSecret(ctx context.Context, in *secretsmanager.DescribeSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
	CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	TagResource(ctx context.Context, in *secretsmanager.TagResourceInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.TagResourceOutput, error)
}

// SecretsManagerWrapper is the operator-facing helper around the AWS SDK
// client. It implements the create-or-update flow that K8s Secret →
// Secrets Manager export needs, plus a typed read for the import direction.
type SecretsManagerWrapper struct {
	API SecretsManagerAPI
}

// NewSecretsManagerWrapper wraps the given SDK-compatible client.
func NewSecretsManagerWrapper(api SecretsManagerAPI) *SecretsManagerWrapper {
	return &SecretsManagerWrapper{API: api}
}

// EncodeSecretData marshals a Kubernetes Secret's data map into a stable
// JSON document. Stability matters because we compare the marshaled bytes
// against the existing SM value to decide whether to issue PutSecretValue.
func EncodeSecretData(data map[string][]byte) ([]byte, error) {
	// Convert []byte values to strings; SM stores text and AWS expects valid UTF-8.
	// We accept binary by base64 encoding upstream if the caller wants it; here
	// we let json.Marshal handle []byte → base64 encoding implicitly via the
	// standard library's encoding/json behavior.
	out, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshalling secret data: %w", err)
	}
	return out, nil
}

// PutResult is what EnsureSecretValue returns to the reconciler.
type PutResult struct {
	// Arn of the secret in Secrets Manager (the canonical address).
	Arn string
	// VersionId of the value just written. Empty when no write occurred
	// because the existing SM value already matched.
	VersionId string
	// Created is true if the secret was created on this call; false if it
	// already existed and we issued PutSecretValue (or made no change).
	Created bool
	// Unchanged is true if the SM value already matched and we skipped the
	// write to avoid an unnecessary version churn.
	Unchanged bool
}

// EnsureSecretValue is the operator's idempotent export primitive. It:
//   - DescribeSecret(path) to find an existing SM secret.
//   - If none: CreateSecret(path, value, kmsKey, tags) and return.
//   - If exists: GetSecretValue to compare. If equal, return Unchanged.
//     Otherwise PutSecretValue and return.
//
// The string passed in `path` is the SM SecretId — using a relative name
// (e.g., `platform/prod/payments/db-password`) lets AWS construct the ARN.
func (w *SecretsManagerWrapper) EnsureSecretValue(ctx context.Context, path string, value []byte, kmsKeyArn string, tags map[string]string) (PutResult, error) {
	desc, err := w.API.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(path),
	})

	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if !errors.As(err, &notFound) {
			return PutResult{}, fmt.Errorf("describe secret %q: %w", path, err)
		}
		// Create path.
		in := &secretsmanager.CreateSecretInput{
			Name:         aws.String(path),
			SecretString: aws.String(string(value)),
		}
		if kmsKeyArn != "" {
			in.KmsKeyId = aws.String(kmsKeyArn)
		}
		if len(tags) > 0 {
			in.Tags = mapToTags(tags)
		}
		out, err := w.API.CreateSecret(ctx, in)
		if err != nil {
			return PutResult{}, fmt.Errorf("create secret %q: %w", path, err)
		}
		return PutResult{
			Arn:       aws.ToString(out.ARN),
			VersionId: aws.ToString(out.VersionId),
			Created:   true,
		}, nil
	}

	// Secret already exists. Compare current value before writing.
	get, err := w.API.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(path),
	})
	if err != nil {
		return PutResult{}, fmt.Errorf("get secret value %q: %w", path, err)
	}
	if get.SecretString != nil && *get.SecretString == string(value) {
		// Optional tag refresh on existing secrets — best-effort.
		if len(tags) > 0 {
			_, _ = w.API.TagResource(ctx, &secretsmanager.TagResourceInput{
				SecretId: aws.String(path),
				Tags:     mapToTags(tags),
			})
		}
		return PutResult{
			Arn:       aws.ToString(desc.ARN),
			VersionId: aws.ToString(get.VersionId),
			Unchanged: true,
		}, nil
	}

	put, err := w.API.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(path),
		SecretString: aws.String(string(value)),
	})
	if err != nil {
		return PutResult{}, fmt.Errorf("put secret value %q: %w", path, err)
	}
	if len(tags) > 0 {
		_, _ = w.API.TagResource(ctx, &secretsmanager.TagResourceInput{
			SecretId: aws.String(path),
			Tags:     mapToTags(tags),
		})
	}
	return PutResult{
		Arn:       aws.ToString(desc.ARN),
		VersionId: aws.ToString(put.VersionId),
	}, nil
}

// FetchedSecret is the result of a read from Secrets Manager.
type FetchedSecret struct {
	Arn       string
	VersionId string
	Value     []byte
	// IsJSON is set true when the SecretString successfully parses as a JSON
	// object — the standard shape the export side writes. Callers can use
	// this to choose whether to unmarshal into a `map[string][]byte` data map
	// for the resulting K8s Secret or fall back to a single `value` key.
	IsJSON bool
	// Parsed is populated when IsJSON is true.
	Parsed map[string][]byte
}

// GetSecretValue reads the secret at arn (or relative name) and returns the
// raw bytes alongside the parsed JSON shape when one is present.
func (w *SecretsManagerWrapper) GetSecretValue(ctx context.Context, arn string) (FetchedSecret, error) {
	out, err := w.API.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(arn),
	})
	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return FetchedSecret{}, fmt.Errorf("secrets manager secret not found: %s", arn)
		}
		return FetchedSecret{}, fmt.Errorf("get secret value %q: %w", arn, err)
	}

	var raw []byte
	if out.SecretString != nil {
		raw = []byte(*out.SecretString)
	} else {
		raw = out.SecretBinary
	}

	fs := FetchedSecret{
		Arn:       aws.ToString(out.ARN),
		VersionId: aws.ToString(out.VersionId),
		Value:     raw,
	}

	// Try parsing as the JSON shape EnsureSecretValue writes.
	var parsed map[string][]byte
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed != nil {
		fs.IsJSON = true
		fs.Parsed = parsed
	}
	return fs, nil
}

func mapToTags(in map[string]string) []smtypes.Tag {
	tags := make([]smtypes.Tag, 0, len(in))
	for k, v := range in {
		k, v := k, v
		tags = append(tags, smtypes.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}
