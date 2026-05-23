package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ExportSecretSpec defines the desired state of ExportSecret.
// The operator reads the Kubernetes Secret named by SourceSecretName from
// the same namespace as this ExportSecret, and writes its data into AWS
// Secrets Manager at TargetPath. TargetPath must satisfy the platform
// policy `platform/<env>/<namespace>/<secret-name>` (see internal/controller/policy.go).
type ExportSecretSpec struct {
	// SourceSecretName is the name of the Kubernetes Secret in this namespace
	// whose data will be exported to AWS Secrets Manager.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]{0,251}[a-z0-9]$`
	SourceSecretName string `json:"sourceSecretName"`

	// TargetPath is the AWS Secrets Manager path. Must match
	// `platform/(dev|staging|prod|prod-dr)/<namespace>/<secret-name>`, and the
	// `<namespace>` component must equal this resource's metadata.namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^platform/(dev|staging|prod|prod-dr)/[a-z0-9-]+/[a-z0-9-]+$`
	TargetPath string `json:"targetPath"`

	// KmsKeyArn is an optional KMS key to use for encryption when creating
	// the secret in Secrets Manager. If empty, the AWS-managed key is used.
	// +optional
	KmsKeyArn string `json:"kmsKeyArn,omitempty"`

	// Tags are applied to the secret in Secrets Manager on creation.
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
}

// ExportSecretStatus reflects the most recently observed state of an
// ExportSecret reconcile.
type ExportSecretStatus struct {
	// Conditions follow standard Kubernetes condition semantics.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncedTime is the timestamp of the last successful PutSecretValue.
	// +optional
	LastSyncedTime *metav1.Time `json:"lastSyncedTime,omitempty"`

	// ObservedGeneration is the generation of the spec that the controller
	// last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SecretArn is the AWS Secrets Manager ARN of the exported secret once it
	// has been created.
	// +optional
	SecretArn string `json:"secretArn,omitempty"`

	// VersionId is the SM VersionId from the most recent PutSecretValue.
	// +optional
	VersionId string `json:"versionId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=es
// +kubebuilder:printcolumn:name="TargetPath",type=string,JSONPath=`.spec.targetPath`
// +kubebuilder:printcolumn:name="LastSynced",type=date,JSONPath=`.status.lastSyncedTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//
// ExportSecret represents a Kubernetes-to-AWS-Secrets-Manager export.
type ExportSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExportSecretSpec   `json:"spec,omitempty"`
	Status ExportSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
//
// ExportSecretList contains a list of ExportSecret.
type ExportSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExportSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExportSecret{}, &ExportSecretList{})
}

// DeepCopyObject convenience accessor (real impl lives in zz_generated_deepcopy.go).
var _ runtime.Object = (*ExportSecret)(nil)
