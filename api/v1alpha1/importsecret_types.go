package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ImportSecretSpec defines the desired state of ImportSecret. The operator
// reads the AWS Secrets Manager secret at SourceArn and materializes a
// Kubernetes Secret named TargetSecretName in the ImportSecret's namespace.
// The secret name portion of SourceArn (after the colon-prefixed name) must
// satisfy platform policy: `platform/<env>/<namespace>/<name>`, and the
// namespace component must equal this resource's metadata.namespace.
type ImportSecretSpec struct {
	// SourceArn is the AWS Secrets Manager ARN of the source secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^arn:aws[a-z-]*:secretsmanager:[a-z0-9-]+:[0-9]{12}:secret:.+$`
	SourceArn string `json:"sourceArn"`

	// TargetSecretName is the name of the Kubernetes Secret to create or update
	// in this namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]{0,251}[a-z0-9]$`
	TargetSecretName string `json:"targetSecretName"`

	// RefreshInterval is a Go duration string (e.g. "1h", "15m") controlling
	// how often the controller re-reads the value from Secrets Manager.
	// Defaults to "1h" when empty.
	// +optional
	// +kubebuilder:default="1h"
	RefreshInterval string `json:"refreshInterval,omitempty"`
}

// ImportSecretStatus reflects the most recently observed state of an
// ImportSecret reconcile.
type ImportSecretStatus struct {
	// Conditions follow standard Kubernetes condition semantics.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncedTime is the timestamp of the last successful materialization.
	// +optional
	LastSyncedTime *metav1.Time `json:"lastSyncedTime,omitempty"`

	// ObservedGeneration is the generation of the spec that the controller
	// last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SourceVersionId is the AWS SM VersionId observed during the last sync.
	// +optional
	SourceVersionId string `json:"sourceVersionId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=is
// +kubebuilder:printcolumn:name="SourceArn",type=string,JSONPath=`.spec.sourceArn`
// +kubebuilder:printcolumn:name="LastSynced",type=date,JSONPath=`.status.lastSyncedTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//
// ImportSecret represents an AWS-Secrets-Manager-to-Kubernetes import.
type ImportSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ImportSecretSpec   `json:"spec,omitempty"`
	Status ImportSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
//
// ImportSecretList contains a list of ImportSecret.
type ImportSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImportSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ImportSecret{}, &ImportSecretList{})
}

var _ runtime.Object = (*ImportSecret)(nil)
