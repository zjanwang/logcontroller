package adapter

import (
	"harmonycloud.cn/log-collector/pkg/types"
)

type Collector interface {
	// NewConfig parse template
	NewConfig(containerLogModels []types.ContainerLogModel, inputTemplate string, parameters types.Parameters) error
	NewConfigForHost(hostLogModels []types.HostLogModel, inputTemplate string, parameters types.Parameters) error
	DeleteConfig(parameters types.Parameters) error
	DeleteConfigForHost(parameters types.Parameters) error
	Start() error
}
