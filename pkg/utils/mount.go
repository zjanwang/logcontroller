package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/containerd/containerd"
	"google.golang.org/grpc"
	"harmonycloud.cn/log-collector/pkg/types"
	v1 "k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"
	"path/filepath"
	"strings"
	"time"
)

var (
	Conn                 *grpc.ClientConn
	ContainerdClient     runtimeapi.RuntimeServiceClient
	containerdUnixSocket = "/var/run/containerd/containerd.sock"
)

// ContainerEnv is container env
type ContainerEnv struct {
	Key   string
	Value string
}

// containerInfo is extra info returned by containerd grpc api
// it's NOT part of cri-api, so we keep this struct being internal visibility.
// If we don't care sth details, we will keep it being interface type.
type containerInfo struct {
	Sandboxid      string                    `json:"sandboxID"`
	Pid            int                       `json:"pid"`
	Removing       bool                      `json:"removing"`
	Snapshotkey    string                    `json:"snapshotKey"`
	Snapshotter    string                    `json:"snapshotter"`
	Runtimetype    string                    `json:"runtimeType"`
	Runtimeoptions interface{}               `json:"runtimeOptions"`
	Config         *ContainerInfoConfig      `json:"config"`
	Runtimespec    *ContainerInfoRuntimeSpec `json:"runtimeSpec"`
}

// ContainerInfoRuntimeSpec ...
type ContainerInfoRuntimeSpec struct {
	Ociversion  string                       `json:"ociVersion"`
	Process     interface{}                  `json:"process"`
	Root        interface{}                  `json:"root"`
	Mounts      []*ContainerInfoRuntimeMount `json:"mounts"`
	Annotations interface{}                  `json:"annotations"`
	Linux       interface{}                  `json:"linux,omitempty"`
}

// ContainerInfoRuntimeMount ...
type ContainerInfoRuntimeMount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options"`
}

// ContainerInfoConfig ...
type ContainerInfoConfig struct {
	Metadata    *ContainerInfoMetadata `json:"metadata"`
	Image       interface{}            `json:"image"`
	Envs        []*ContainerInfoEnv    `json:"envs"`
	Mounts      []*ContainerInfoMount  `json:"mounts"`
	Labels      interface{}            `json:"labels"`
	Annotations interface{}            `json:"annotations"`
	LogPath     string                 `json:"log_path"`
	Linux       interface{}            `json:"linux,omitempty"`
}

// ContainerInfoMetadata ...
type ContainerInfoMetadata struct {
	Name string `json:"name"`
}

// ContainerEnv ...
type ContainerInfoEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ContainerInfoMount ...
type ContainerInfoMount struct {
	ContainerPath  string `json:"container_path"`
	HostPath       string `json:"host_path"`
	Readonly       bool   `json:"readonly,omitempty"`
	SelinuxRelabel bool   `json:"selinux_relabel,omitempty"`
}

func (r *DockerRuntime) FindMount(pod *v1.Pod, logModel *types.ContainerLogModel, baseDir string) ([]types.Mount, error) {
	var mountPoint []types.Mount
	var containerId string
	for _, initStatus := range pod.Status.InitContainerStatuses {
		if initStatus.Name == logModel.Container {
			containerId = initStatus.ContainerID
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == logModel.Container {
			containerId = status.ContainerID
		}
	}
	if containerId == "" {
		return nil, fmt.Errorf("containerID %v not found in pod", logModel.Container)
	}

	id := GetContainerIDByPod(containerId)
	container, err := GetDockerContainerByID(id)
	if err != nil {
		return mountPoint, err
	}
	if container == nil {
		return mountPoint, nil
	}
	var (
		containerEnvs []*ContainerEnv
	)
	for _, env := range container.Config.Env {
		kv := strings.Split(env, "=")
		containerEnvs = append(containerEnvs, &ContainerEnv{
			Key:   kv[0],
			Value: kv[1],
		})
	}
	for k, v := range logModel.Fields {
		logModel.Fields[k] = ParseEnv(containerEnvs, v)
	}
	klog.Infof("mount info for container %v:%v", logModel.Container, container.Mounts)
	for i, path := range logModel.LogPath {
		if path == "stdout" {
			mount := types.Mount{}
			mount.Source = baseDir + container.LogPath
			mount.Destination = "stdout"
			mountPoint = append(mountPoint, mount)
			continue
		}
		logModel.LogPath[i] = ParsePath(containerEnvs, path) //是否使用path = 更有效，可避免对父目录使用ParsePath
		pathDir := filepath.Dir(path)
		parsedPath := ParsePath(containerEnvs, pathDir)
		findDir := false
		mountDestination := parsedPath
		dir := parsedPath
		for {
			for _, mount := range container.Mounts {
				relPath, err := filepath.Rel(mount.Destination, mountDestination)
				if err != nil {
					klog.Error(err)
					continue
				}
				if relPath == "" {
					continue
				}
				if filepath.Clean(mount.Destination) == dir {
					findDir = true
					isMountExists := false
					mountSource := filepath.Join(baseDir, mount.Source, relPath)
					if relPath == "." {
						mountSource = filepath.Join(baseDir, mount.Source)
					}
					for _, m := range mountPoint {
						if m.Destination == mountDestination && m.Source == mountSource {
							isMountExists = true
						}
					}
					if !isMountExists {
						mountPoint = append(mountPoint, types.Mount{Source: mountSource, Destination: mountDestination})
					}
					break
				}
			}
			if findDir {
				break
			}
			dir = filepath.Dir(dir)
			if dir == "/" || dir == "." {
				upperdir := container.GraphDriver.Data["UpperDir"]
				if upperdir != "" {
					mountPoint = append(mountPoint, types.Mount{Source: baseDir + upperdir + mountDestination, Destination: mountDestination})
				}
				break
			}
		}
	}
	return mountPoint, nil
}

func (r *DockerRuntime) lookupRootfsCache(containerID string) (string, bool) {
	r.rootfsLock.RLock()
	defer r.rootfsLock.RUnlock()
	dir, ok := r.rootfsCache[containerID]
	return dir, ok
}

func (r *ContainerdRuntime) FindMount(pod *v1.Pod, logModel *types.ContainerLogModel, baseDir string) ([]types.Mount, error) {
	var mountPoint []types.Mount
	var err error
	//  建立与pod的连接
	if Conn == nil || Conn.GetState().String() != "READY" {
		ContainerdClient, Conn, err = NewClient(containerdUnixSocket, 10*time.Second)
		if err != nil {
			if strings.Contains(err.Error(), "context deadline exceeded") {
				klog.Error(err)
				klog.Fatalf("cannot connect to containerd,try to restart...")
			}
			return nil, err
		}
	}
	// 查找指定container
	var containerId string
	for _, initStatus := range pod.Status.InitContainerStatuses {
		if initStatus.Name == logModel.Container {
			containerId = initStatus.ContainerID
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == logModel.Container {
			containerId = status.ContainerID
		}
	}
	if containerId == "" {
		return nil, fmt.Errorf("container %v not found in pod", logModel.Container)
	}
	id := GetContainerIDByPod(containerId)
	// 通过请求获取container对象
	req := &runtimeapi.ContainerStatusRequest{
		ContainerId: id,
		Verbose:     true, // NOQA
	}
	cs, err := ContainerdClient.ContainerStatus(context.TODO(), req)
	if err != nil {
		return nil, err
	}

	// NOTE: unmarshal the extra info to get the container envs and mounts data.
	// Mounts should include both image volume and container mount.
	extraContainerInfo := new(containerInfo)
	err = json.Unmarshal([]byte(cs.Info["info"]), extraContainerInfo)
	if err != nil {
		return nil, err
	}
	var (
		containerEnvs []*ContainerEnv
	)
	for _, ce := range extraContainerInfo.Config.Envs {
		containerEnvs = append(containerEnvs, &ContainerEnv{
			Key:   ce.Key,
			Value: ce.Value,
		})
	}
	for k, v := range logModel.Fields {
		logModel.Fields[k] = ParseEnv(containerEnvs, v)
	}
	mounts := cs.Status.Mounts
	klog.Infof("mount info for container %v:%v", logModel.Container, mounts)
	for i, path := range logModel.LogPath {
		if path == "stdout" {
			mount := types.Mount{}
			stdoutDir := filepath.Dir(cs.Status.LogPath)
			mount.Source = baseDir + stdoutDir + "/*.log"
			mount.Destination = "stdout"
			mountPoint = append(mountPoint, mount)
			continue
		}
		logModel.LogPath[i] = ParsePath(containerEnvs, path)
		pathDir := filepath.Dir(path)
		parsedPath := ParsePath(containerEnvs, pathDir)
		findDir := false
		mountDestination := parsedPath
		dir := parsedPath
		/*				findDir := false
						mountDestination := filepath.Dir(path)
						dir := filepath.Dir(path)*/
		for {
			for _, mount := range mounts {
				relPath, err := filepath.Rel(mount.ContainerPath, mountDestination)
				if err != nil {
					klog.Error(err)
					continue
				}
				if relPath == "" {
					continue
				}
				if filepath.Clean(mount.ContainerPath) == dir {
					findDir = true
					isMountExists := false
					mountSource := filepath.Join(baseDir, mount.HostPath, relPath)
					if relPath == "." {
						mountSource = filepath.Join(baseDir, mount.HostPath)
					}
					for _, m := range mountPoint {
						if m.Destination == mountDestination && m.Source == mountSource {
							isMountExists = true
						}
					}
					if !isMountExists {
						mountPoint = append(mountPoint, types.Mount{Source: mountSource, Destination: mountDestination})
					}
					break
				}
			}
			if findDir {
				break
			}
			dir = filepath.Dir(dir)
			if dir == "/" || dir == "." {
				upperDir := r.getContainerUpperDir(extraContainerInfo.Snapshotkey, extraContainerInfo.Snapshotter)
				if upperDir != "" {
					mountPoint = append(mountPoint, types.Mount{Source: baseDir + upperDir + mountDestination, Destination: mountDestination})
				}
				break
			}
		}
	}
	return mountPoint, nil
}

func (r *ContainerdRuntime) lookupRootfsCache(containerID string) (string, bool) {
	r.rootfsLock.RLock()
	defer r.rootfsLock.RUnlock()
	dir, ok := r.rootfsCache[containerID]
	return dir, ok
}

func (r *ContainerdRuntime) getContainerUpperDir(containerid, snapshotter string) string {
	containerdClient, err := containerd.New(containerdUnixSocket, containerd.WithDefaultNamespace("k8s.io"))
	if err == nil {
		_, err = containerdClient.Version(context.Background())
	}
	if err != nil {
		klog.Warning(context.Background(), "CONTAINERD_CLIENT_ALARM", "Connect containerd failed", err)
		containerdClient = nil
	}
	if dir, ok := r.lookupRootfsCache(containerid); ok {
		return dir
	}
	si := containerdClient.SnapshotService(snapshotter)
	mounts, err := si.Mounts(context.Background(), containerid)
	if err != nil {
		klog.Warning(context.Background(), "CONTAINERD_CLIENT_ALARM", "cannot get snapshot info, containerID", containerid, "errInfo", err)
		return ""
	}
	for _, m := range mounts {
		if len(m.Options) != 0 {
			for _, i := range m.Options {
				s := strings.Split(i, "=")
				if s[0] == "upperdir" {
					r.rootfsLock.Lock()
					r.rootfsCache[containerid] = s[1]
					r.rootfsLock.Unlock()
					return s[1]
				}
				continue
			}
		}
	}
	return ""
}
