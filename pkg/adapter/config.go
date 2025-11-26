package adapter

import (
	"harmonycloud.cn/log-collector/pkg/adapter/filebeat"
)

var (
	FilebeatCollector = filebeat.NewFilebeat()
)

func GetCollector(collector string) Collector {
	if collector == "filebeat" {
		return FilebeatCollector
	}
	return FilebeatCollector
}
