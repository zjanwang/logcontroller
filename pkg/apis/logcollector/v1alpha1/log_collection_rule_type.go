package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:resource:shortName=lcr
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LogCollectionRule represents the rule to collect log
type LogCollectionRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec LogCollectionRuleSpec `json:"spec,omitempty"`
}

// LogCollectionRuleSpec represents the expectation of LogCollectionRule
type LogCollectionRuleSpec struct {
	Collectors []Collector `json:"collectors"`
}

type Collector struct {
	Enable        bool              `json:"enable"`
	MatchLabels   map[string]string `json:"matchLabels"`
	ContainerRule []ContainerRule   `json:"containerRule"`
}

type ContainerRule struct {
	Containers []string         `json:"containers,omitempty"`
	Rules      []CollectionRule `json:"rules"`
}

type CollectionRule struct {
	Stdout             bool              `json:"stdout,omitempty"`
	Path               []string          `json:"path,omitempty"`
	Fields             map[string]string `json:"fields,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	ExcludeFiles       []string          `json:"excludeFiles,omitempty"`
	RootConfigurations map[string]string `json:"rootConfigurations,omitempty"`
	Kafka              Kafka             `json:"kafka,omitempty"`
	Elasticsearch      Elasticsearch     `json:"elasticsearch,omitempty"`
	UID                string            `json:"uid,omitempty"`
}

type Kafka struct {
	Topic string `json:"topic,omitempty"`
}
type Elasticsearch struct {
	Index string `json:"index,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// LogCollectionRuleList is a collection of LogCollectionRule
type LogCollectionRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []LogCollectionRule `json:"items"`
}
