package annotation_watch

import (
	"gopkg.in/yaml.v3"
	"harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1"
	"k8s.io/klog/v2"
	"strings"
)

const (
	LogConfigAnnotation = "devops.harmonycloud.cn/logconfig"
	LogSuffix           = "-logrule"
)

type LogConfig struct {
	Stdout       bool        `yaml:"stdout"`
	Path         string      `yaml:"path"`
	Tags         []string    `yaml:"tags"`
	Kafka        KafkaConfig `yaml:"kafka"`
	ExcludeFiles []string    `yaml:"excludeFiles"`
}

type KafkaConfig struct {
	Topic string `yaml:"topic"`
}

func HasLogAnnotation(anno map[string]string) (string, bool) {
	if v, ok := anno[LogConfigAnnotation]; ok {
		return v, true
	}
	return "", false
}

func ParseAnnotationToRule(value, name, namespace string, labels map[string]string) (*v1alpha1.LogCollectionRule, error) {
	config := make(map[string][]LogConfig)
	err := yaml.Unmarshal([]byte(value), &config)
	if err != nil {
		return nil, err
	}
	rule := &v1alpha1.LogCollectionRule{}
	rule.Name = name
	rule.Namespace = namespace
	collector := v1alpha1.Collector{}
	collector.MatchLabels = labels
	collector.Enable = true
	for container, configs := range config {
		containerRule := v1alpha1.ContainerRule{}
		containerRule.Containers = append(containerRule.Containers, container)
		for _, c := range configs {
			r := v1alpha1.CollectionRule{}
			r.Path = append(r.Path, c.Path)
			r.Stdout = c.Stdout
			r.Kafka.Topic = c.Kafka.Topic
			for _, ex := range c.ExcludeFiles {
				r.ExcludeFiles = append(r.ExcludeFiles, ex)
			}
			targetTag := parseTags(c.Tags)
			r.Fields = targetTag
			containerRule.Rules = append(containerRule.Rules, r)
		}
		collector.ContainerRule = append(collector.ContainerRule, containerRule)
	}
	rule.Spec.Collectors = append(rule.Spec.Collectors, collector)
	return rule, nil
}

func parseTags(tags []string) map[string]string {
	target := make(map[string]string)
	for _, tag := range tags {
		part := strings.SplitN(tag, "=", 2)
		if len(part) != 2 {
			klog.Errorf("can not parse tag %v", tag)
			continue
		}
		target[part[0]] = part[1]
	}
	return target
}
