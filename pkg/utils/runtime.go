package utils

import (
	"harmonycloud.cn/log-collector/pkg/types"
	v1 "k8s.io/api/core/v1"
	"sync"
)

type DockerRuntime struct {
	rootfsLock  sync.RWMutex
	rootfsCache map[string]string
}
type ContainerdRuntime struct {
	rootfsLock  sync.RWMutex
	rootfsCache map[string]string
}

func GetRuntime(runtime string) RuntimeFactory {
	if runtime == "docker" {
		return &DockerRuntime{
			rootfsCache: make(map[string]string),
		}
	} else {
		return &ContainerdRuntime{
			rootfsCache: make(map[string]string),
		}
	}
}

type RuntimeFactory interface {
	FindMount(pod *v1.Pod, logModel *types.ContainerLogModel, baseDir string) ([]types.Mount, error)
	lookupRootfsCache(containerID string) (string, bool)
}
