package filebeat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/elastic/go-ucfg"
	"github.com/elastic/go-ucfg/yaml"
	goyaml "gopkg.in/yaml.v3"
	"harmonycloud.cn/log-collector/pkg/types"
	"harmonycloud.cn/log-collector/pkg/utils"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
)

const (
	FILEBEAT_EXEC_CMD  = "/usr/bin/filebeat"
	FILEBEAT_REGISTRY  = "/var/lib/filebeat/data/registry/filebeat/log.json"
	FILEBEAT_BASE_CONF = "/etc/filebeat"
	FILEBEAT_CONF_DIR  = "/etc/log-controller/global/"
	FILEBEAT_CONF_FILE = FILEBEAT_BASE_CONF + "/filebeat.yml"

	DOCKER_SYSTEM_PATH  = "/var/lib/docker/"
	KUBELET_SYSTEM_PATH = "/var/lib/kubelet/"

	ENV_FILEBEAT_OUTPUT = "FILEBEAT_OUTPUT"
)

var configOpts = []ucfg.Option{
	ucfg.PathSep("."),
	ucfg.ResolveEnv,
	ucfg.VarExp,
}
var filebeatCMD *exec.Cmd

type Filebeat struct {
	watchMap      map[string]string
	watchDone     chan bool
	watchDuration time.Duration
}

func NewFilebeat() Filebeat {
	return Filebeat{
		watchMap:      make(map[string]string),
		watchDone:     make(chan bool),
		watchDuration: 60 * time.Second,
	}
}

type Registry struct {
	Key   string        `json:"k"`
	Value RegistryState `json:"v"`
}

// RegistryState represents log offsets
type RegistryState struct {
	Source      string        `json:"source"`
	Offset      int64         `json:"offset"`
	Timestamp   time.Time     `json:"timestamp"`
	TTL         time.Duration `json:"ttl"`
	Type        string        `json:"type"`
	FileStateOS FileInode
}

// FileInode identify a unique log file
type FileInode struct {
	Inode  uint64 `json:"inode,"`
	Device uint64 `json:"device,"`
}

// Config contains all log paths
type Config struct {
	Paths []string `config:"paths"`
}

// LogConfig log configuration
type LogConfig struct {
	FilestreamID       string
	HostDir            map[string][]string
	ContainerDir       string
	File               string
	Fields             map[string]string
	Tags               []string
	RootConfigurations map[string]interface{}
	Parsers            map[string]interface{}
	Target             string
	Stdout             bool
	Format             string
	ExcludeFiles       []string
}

type Input struct {
	ConfigList []LogConfig `json:"configList"`
}

func (f Filebeat) NewConfig(containerLogModels []types.ContainerLogModel, inputTemplate string, parameters types.Parameters) error {
	var config []LogConfig
	podNamespace := parameters.Pod.Namespace
	for _, model := range containerLogModels {
		logConfig := LogConfig{}
		logConfig.FilestreamID = model.UID
		logPathMap := make(map[string][]string)
		hostPathMap := make(map[string][]string)
		logConfig.Fields = make(map[string]string)
		logConfig.RootConfigurations = make(map[string]interface{})
		logConfig.Parsers = make(map[string]interface{})
		if model.Index != "" {
			logConfig.Fields["index"] = model.Index
		}
		resourceType, resourceName := findTargetResourceTypeName(parameters.Pod)
		logConfig.Fields[types.ResourceType] = resourceType
		logConfig.Fields[types.ResourceName] = resourceName
		logConfig.Fields[types.PodUID] = model.PodUID
		logConfig.Fields[types.ContainerName] = model.Container
		logConfig.Fields[types.DockerContainer] = model.Container
		logConfig.Fields[types.PodName] = model.PodName
		logConfig.Fields[types.NodeName] = model.Node
		logConfig.Fields[types.PodNamespace] = model.PodNamespace
		if model.Topic != "" {
			logConfig.Fields["topic"] = model.Topic
		}
		logConfig.Tags = append(logConfig.Tags, model.Tags...)
		// 建立logPathMap映射，按照目录对日志文件分组
		for _, logPath := range model.LogPath {
			dir := filepath.Dir(logPath)
			_, file := filepath.Split(logPath)
			// if file is empty
			if file == "" {
				file = "*.log"
			}
			if v, ok := logPathMap[dir]; ok {
				v = append(v, file)
				logPathMap[dir] = v
			} else {
				logPathMap[dir] = []string{file}
			}
		}
		needCleanRemove := true
		// 建立hostPathMap映射，按照宿主机目录对日志文件分组
		for _, m := range model.Mount {
			if m.Destination == "stdout" {
				dir := filepath.Dir(m.Source)
				file := filepath.Base(m.Source)
				file = file + "*"
				hostPathMap[dir] = []string{file}
				logConfig.Stdout = true
				continue
			}
			if v, ok := logPathMap[m.Destination]; ok {
				hostPathMap[m.Source] = v
				if m.IsPVC {
					needCleanRemove = false
				}
				/*				if m.Type == "hostPath" || m.Type == "pvc" {
								temp := m.Type + "-" + m.Name + "-" + m.Destination + strings.Join(v, ",")
								hash := sha256.Sum256([]byte(temp))
								hashString := base64.StdEncoding.EncodeToString(hash[:])
								logConfig.FilestreamID = hashString
								klog.Infof("template %v,hash %v", temp, hashString)
							}*/
			}
		}
		logConfig.ExcludeFiles = model.ExcludeFiles
		logConfig.HostDir = hostPathMap
		for k, v := range model.Fields {
			switch k {
			case "format":
				logConfig.Format = v
			default:
				logConfig.Fields[k] = v
			}
		}
		// 添加根配置
		for k, v := range model.RootConfigurations {
			if strings.Contains(k, "multiline") {
				ss := strings.Split(k, ".")
				if len(ss) != 2 {
					klog.Warningf("cannot parse %v configuration", k)
					continue
				}
				logConfig.Parsers[ss[1]] = v
			} else {
				logConfig.RootConfigurations[k] = v
			}
		}
		logConfig.RootConfigurations["clean_removed"] = needCleanRemove
		config = append(config, logConfig)
	}
	// 创建全局配置模版
	_, err := os.Stat(types.GlobalConfDir + podNamespace)
	if os.IsNotExist(err) {
		if err := os.Mkdir(types.GlobalConfDir+podNamespace, os.ModePerm); err != nil {
			klog.Error("failed to create dir", types.GlobalConfDir+podNamespace)
			return err
		}
		klog.Info("create dir ", types.GlobalConfDir+podNamespace)
	}
	if err := parseTemplate(parameters, config, inputTemplate); err != nil {
		return err
	}
	return nil
}

func findTargetResourceTypeName(pod *v1.Pod) (string, string) {
	var resourceType, resourceName string
	for _, o := range pod.OwnerReferences {
		switch o.Kind {
		case "ReplicaSet":
			resourceType = "Deployment"
			ownerName := o.Name
			lastIndex := strings.LastIndex(ownerName, "-")
			if lastIndex != -1 {
				ownerName = ownerName[:lastIndex]
				resourceName = ownerName
			} else {
				klog.Warningf("cannot find resourceName for pod %v", pod.Name)
			}
		default:
			resourceType = o.Kind
			resourceName = o.Name
		}
	}
	return resourceType, resourceName
}

func (f Filebeat) NewConfigForHost(hostLogModels []types.HostLogModel, inputTemplate string, parameters types.Parameters) error {
	var config []LogConfig
	for _, model := range hostLogModels {
		logConfig := LogConfig{}
		logConfig.FilestreamID = model.UID
		logPathMap := make(map[string][]string)
		logConfig.Fields = make(map[string]string)
		logConfig.Parsers = make(map[string]interface{})
		logConfig.RootConfigurations = make(map[string]interface{})
		if model.Index != "" {
			logConfig.Fields["index"] = model.Index
		}
		logConfig.Fields[types.NodeName] = parameters.Node
		if model.Topic != "" {
			logConfig.Fields["topic"] = model.Topic
		}
		logConfig.Tags = append(logConfig.Tags, model.Tags...)
		for _, logPath := range model.LogPath {
			dir := filepath.Dir(logPath)
			_, file := filepath.Split(logPath)
			// if file is empty
			if file == "" {
				file = "*.log"
			}
			if v, ok := logPathMap[dir]; ok {
				v = append(v, file)
				logPathMap[dir] = v
			} else {
				logPathMap[dir] = []string{file}
			}
		}
		logConfig.ExcludeFiles = model.ExcludeFiles
		logConfig.HostDir = logPathMap
		for k, v := range model.Fields {
			switch k {
			case "format":
				logConfig.Format = v
			default:
				logConfig.Fields[k] = v
			}
		}
		for k, v := range model.RootConfigurations {
			if strings.Contains(k, "multiline") {
				ss := strings.Split(k, ".")
				if len(ss) != 2 {
					klog.Warningf("cannot parse %v configuration", k)
					continue
				}
				logConfig.Parsers[ss[1]] = v
			} else {
				logConfig.RootConfigurations[k] = v
			}
		}
		config = append(config, logConfig)
	}
	_, err := os.Stat(types.GlobalConfDir + parameters.Node)
	if os.IsNotExist(err) {
		if err := os.Mkdir(types.GlobalConfDir+parameters.Node, os.ModePerm); err != nil {
			klog.Error("failed to create dir", types.GlobalConfDir+parameters.Node)
			return err
		}
		klog.Info("create dir ", types.GlobalConfDir+parameters.Node)
	}
	if err := parseTemplate(parameters, config, inputTemplate); err != nil {
		return err
	}
	return nil
}

func (f Filebeat) DeleteConfig(parameters types.Parameters) error {
	// return f.watchContainerAfterDelete(parameters)
	_, err := os.Stat(types.GlobalConfDir + parameters.Pod.Namespace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		klog.Error(err, " failed to read global config dir")
		return err
	}
	files, err := os.ReadDir(types.GlobalConfDir + parameters.Pod.Namespace)
	if err != nil {
		klog.Error(err, " failed to read global config dir")
		return err
	}
	containsConfigName := string(parameters.Pod.UID)
	if parameters.RuleName != "" {
		containsConfigName = string(parameters.Pod.UID) + "-by-" + parameters.RuleName + ".yml"
	}
	for _, file := range files {
		if strings.Contains(file.Name(), containsConfigName) {
			err := os.Remove(types.GlobalConfDir + parameters.Pod.Namespace + "/" + file.Name())
			if err != nil {
				klog.Error(err, fmt.Sprintf("remove file %s failed", types.GlobalConfDir+parameters.Pod.Namespace+"/"+file.Name()))
			}
			klog.Infof("remove file %s", types.GlobalConfDir+parameters.Pod.Namespace+"/"+file.Name())
		}
	}
	return nil
}

func (f Filebeat) DeleteConfigForHost(parameters types.Parameters) error {
	_, err := os.Stat(types.GlobalConfDir + parameters.Node)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		klog.Error(err, " failed to read global config dir")
		return err
	}
	files, err := os.ReadDir(types.GlobalConfDir + parameters.Node)
	if err != nil {
		klog.Error(err, " failed to read global config dir")
		return err
	}
	containsConfigName := parameters.Node + "-by-" + parameters.RuleName + ".yml"
	for _, file := range files {
		if strings.Contains(file.Name(), containsConfigName) {
			err := os.Remove(types.GlobalConfDir + parameters.Node + "/" + file.Name())
			if err != nil {
				klog.Error(err, fmt.Sprintf("remove file %s failed", types.GlobalConfDir+parameters.Node+"/"+file.Name()))
			}
			klog.Infof("remove file %s", types.GlobalConfDir+parameters.Node+"/"+file.Name())
		}
	}
	return nil
}

func (f Filebeat) watchContainerAfterDelete(parameters types.Parameters) error {
	if f.watchMap == nil {
		klog.Info("init filebeat watch map")
		f.watchMap = make(map[string]string)
	}
	containsConfigName := string(parameters.Pod.UID)
	if parameters.RuleName != "" {
		containsConfigName = string(parameters.Pod.UID) + "-by-" + parameters.RuleName + ".yml"
	}
	files, err := os.ReadDir(types.GlobalConfDir + parameters.Pod.Namespace)
	if err != nil {
		klog.Error(err, " failed to read global config dir")
		return err
	}
	for _, file := range files {
		if strings.Contains(file.Name(), containsConfigName) {
			f.watchMap[types.GlobalConfDir+parameters.Pod.Namespace+"/"+file.Name()] = "true"
			klog.Infof("add watcher %s", types.GlobalConfDir+parameters.Pod.Namespace+file.Name())
		}
	}
	return nil
}

func (f Filebeat) Start() error {
	if filebeatCMD != nil {
		pid := filebeatCMD.Process.Pid
		klog.Info(fmt.Sprintf("filebeat started, pid: %v", pid))
		return fmt.Errorf(types.ERR_ALREADY_STARTED)
	}
	klog.Info("starting filebeat")
	filebeatCMD = exec.Command(FILEBEAT_EXEC_CMD, "-c", FILEBEAT_CONF_FILE)
	filebeatCMD.Stderr = os.Stderr
	filebeatCMD.Stdout = os.Stdout
	err := filebeatCMD.Start()
	if err != nil {
		klog.Errorf("filebeat start fail: %v", err)
	}
	go func() {
		klog.Infof("filebeat started: %v", filebeatCMD.Process.Pid)
		err := filebeatCMD.Wait()
		if err != nil {
			klog.Errorf("filebeat exited: %v", err)
			if exitError, ok := err.(*exec.ExitError); ok {
				processState := exitError.ProcessState
				klog.Errorf("filebeat exited pid: %v", processState.Pid())
			}
		}
		// try to restart filebeat
		klog.Warningf("filebeat exited and try to restart")
		filebeatCMD = nil
		f.Start()
	}()
	// TODO watch offset when delete
	// go f.watch()
	return nil
}

func (f Filebeat) watch() {
	klog.Info("start filebeat watcher")
	for {
		select {
		case <-f.watchDone:
			klog.Infof("filebeat watcher stop")
			return
		case <-time.After(f.watchDuration):
			klog.Info("debug scan")
			err := f.scan()
			if err != nil {
				klog.Errorf("filebeat watcher scan error: %v", err)
			}
		}
	}
}

func (f Filebeat) scan() error {
	states, err := f.getRegsitryState()
	if err != nil {
		return err
	}
	for _, watchFile := range f.watchMap {
		config, err := f.loadConfig(watchFile)
		if err != nil || config == nil {
			continue
		}
		paths := make(map[string]string, 0)
		for _, path := range config.Paths {
			if _, ok := paths[path]; !ok {
				paths[path] = watchFile
			}
		}
		if _, err := os.Stat(watchFile); err != nil && os.IsNotExist(err) {
			klog.Infof("log config %s has been removed and ignore", watchFile)
			delete(f.watchMap, watchFile)
		} else if f.canRemoveConf(watchFile, states, paths, config) {
			klog.Infof("try to remove log config %s", watchFile)
			if err := os.Remove(watchFile); err != nil {
				klog.Errorf("remove log config %s fail: %v", watchFile, err)
			} else {
				delete(f.watchMap, watchFile)
			}
		}
	}
	return nil
}
func (f Filebeat) isAutoMountPath(path string) bool {
	dockerVolumePattern := fmt.Sprintf("^%s.*$", filepath.Join("/host", DOCKER_SYSTEM_PATH))
	if ok, _ := regexp.MatchString(dockerVolumePattern, path); ok {
		return true
	}

	kubeletVolumePattern := fmt.Sprintf("^%s.*$", filepath.Join("/host", KUBELET_SYSTEM_PATH))
	ok, _ := regexp.MatchString(kubeletVolumePattern, path)
	return ok
}

func (f Filebeat) canRemoveConf(watchFile string, registry map[string]RegistryState, configPaths map[string]string, config *Config) bool {
	for _, path := range config.Paths {
		autoMount := f.isAutoMountPath(filepath.Dir(path))
		logFiles, _ := filepath.Glob(path)
		for _, logFile := range logFiles {
			info, err := os.Stat(logFile)
			if err != nil {
				klog.Error(err)
				continue
			}
			if _, ok := registry[logFile]; !ok {
				klog.Warningf("%s->%s registry not exist", watchFile, logFile)
				continue
			}
			if registry[logFile].Offset < info.Size() {
				if autoMount { // ephemeral logs
					klog.Infof("%s->%s does not finish to read", watchFile, logFile)
					return false
				} else if _, ok := configPaths[path]; !ok { // host path bind
					klog.Infof("%s->%s does not finish to read and not exist in other config",
						watchFile, logFile)
					return false
				}
			}
		}
	}
	return true
}

func (f Filebeat) loadConfig(file string) (*Config, error) {
	c, err := yaml.NewConfigWithFile(file, configOpts...)
	if err != nil {
		klog.Errorf("read %v log config error: %v", file, err)
		return nil, err
	}
	var config Config
	if err := c.Unpack(&config); err != nil {
		klog.Errorf("parse %v log config error: %v", file, err)
		return nil, err
	}
	return &config, nil
}

func (f Filebeat) getRegsitryState() (map[string]RegistryState, error) {
	file, err := os.Open(FILEBEAT_REGISTRY)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	states := make([]RegistryState, 0)
	err = decoder.Decode(&states)
	if err != nil {
		return nil, err
	}

	statesMap := make(map[string]RegistryState, 0)
	for _, state := range states {
		if _, ok := statesMap[state.Source]; !ok {
			statesMap[state.Source] = state
		}
	}
	return statesMap, nil
}

func parseTemplate(parameters types.Parameters, config []LogConfig, inputTemplate string) error {
	tmpl := template.Must(template.New("config").Parse(inputTemplate))
	var buf bytes.Buffer
	context := map[string]interface{}{
		"configList": config,
	}
	err := tmpl.Execute(&buf, context)
	if err != nil {
		klog.Error(err, " failed to render config")
		return err
	}
	// merge duplicated keys(mostly parsers)
	var root goyaml.Node
	if err := goyaml.Unmarshal([]byte(buf.String()), &root); err != nil {
		klog.Error(err, " failed to parse tpl")
		return err
	}
	mergedRoot, err := utils.MergeDuplicateKeys(&root)
	if err != nil {
		log.Fatalf("merge duplicate keys error: %v", err)
	}
	mergedData, err := goyaml.Marshal(mergedRoot)
	if err != nil {
		log.Fatalf("marshal merged root failed: %v", err)
	}

	if parameters.Pod != nil {
		if err = os.WriteFile(fmt.Sprintf("%s/%s/%s-by-%s.yml", types.GlobalConfDir, parameters.Pod.Namespace, parameters.Pod.GetUID(), parameters.RuleName), mergedData, os.FileMode(0644)); err != nil {
			klog.Error(err, " failed to write template config")
			return err
		}
	} else {
		if err = os.WriteFile(fmt.Sprintf("%s/%s/%s-by-%s.yml", types.GlobalConfDir, parameters.Node, parameters.Node, parameters.RuleName), mergedData, os.FileMode(0644)); err != nil {
			klog.Error(err, " failed to write template config")
			return err
		}
	}

	return nil
}
