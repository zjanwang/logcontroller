package types

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	RuleAdded     = "log-collector-rule-added"
	FinalizerName = "log.controller/finalizer"
)

/*const (
	TemplateDir = "/etc/log-controller/template"
)*/

const (
	PodName         = "k8s_pod"
	PodUID          = "k8s_pod_uid"
	PodNamespace    = "k8s_pod_namespace"
	NodeName        = "k8s_node_name"
	ContainerName   = "k8s_container_name"
	DockerContainer = "docker_container"
	ResourceType    = "k8s_resource_type"
	ResourceName    = "k8s_resource_name"
)

const (
	GlobalConfigAnno = "log.harmonycloud.cn/globalConfig"
)

const (
	ERR_ALREADY_STARTED = "already started"
)

var (
	ConfigFilePrefix = "logpipe_collect_"
	GlobalConfDir    = "/etc/log-controller/global/"
)

type Configuration struct {
	Template       string
	BaseDir        string
	GlobalConf     string
	HostName       string
	NamespacedName types.NamespacedName
}

type ContainerLogModel struct {
	Index              string
	Container          string
	LogPath            []string
	Mount              []Mount
	Fields             map[string]string
	RootConfigurations map[string]interface{}
	Tags               []string
	PodName            string
	PodUID             string
	Stdout             bool
	PodNamespace       string
	Topic              string
	ExcludeFiles       []string
	Node               string
	UID                string
}
type HostLogModel struct {
	Index              string
	LogPath            []string
	Fields             map[string]string
	RootConfigurations map[string]interface{}
	Tags               []string
	Topic              string
	ExcludeFiles       []string
	UID                string
}

type Mount struct {
	Source      string
	Destination string
	IsPVC       bool
}

// DefaultConfiguration new empty Configuration
func DefaultConfiguration() *Configuration {
	return &Configuration{}
}

type Parameters struct {
	Pod            *v1.Pod
	InitConfigPath string
	RuleName       string
	IsPodDeleted   bool
	Node           string
}
