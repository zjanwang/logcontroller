package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:scope=Cluster,shortName=hlcr
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HostLogCollectionRule represents the rule to collect log
type HostLogCollectionRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec HostLogCollectionRuleSpec `json:"spec,omitempty"`
}

// LogCollectionRuleSpec represents the expectation of LogCollectionRule
type HostLogCollectionRuleSpec struct {
	Collectors   []HostCollector   `json:"collectors"`
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

type HostCollector struct {
	HostRules []HostCollectionRule `json:"rules"`
}

type HostCollectionRule struct {
	Path               []string          `json:"path,omitempty"`
	Fields             map[string]string `json:"fields,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	RootConfigurations map[string]string `json:"rootConfigurations,omitempty"`
	ExcludeFiles       []string          `json:"excludeFiles,omitempty"`
	Kafka              Kafka             `json:"kafka,omitempty"`
	Elasticsearch      Elasticsearch     `json:"elasticsearch,omitempty"`
	UID                string            `json:"uid,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HostLogCollectionRuleList is a collection of HostLogCollectionRule
type HostLogCollectionRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []HostLogCollectionRule `json:"items"`
}
