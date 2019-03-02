package libcapsule

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/songxinjianqwe/capsule/libcapsule/cgroups"
	"github.com/songxinjianqwe/capsule/libcapsule/configs"
	"github.com/songxinjianqwe/capsule/libcapsule/util/exception"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	// 容器状态文件的文件名
	// 存放在 $RuntimeRoot/$containerId/下
	StateFilename       = "state.json"
	NotExecFlagFilename = "not_exec.flag"
	// 重新执行本应用的command，相当于 重新执行./capsule
	ContainerInitCmd = "/proc/self/exe"
	// 运行容器init进程的命令
	ContainerInitArgs = "init"
	// 运行时文件的存放目录
	RuntimeRoot = "/run/capsule"
	// 容器配置文件，存放在运行capsule的cwd下
	ContainerConfigFilename = "config.json"
)

func NewFactory() (Factory, error) {
	logrus.Infof("new container factory ...")
	logrus.Infof("mkdir RuntimeRoot if not exists: %s", RuntimeRoot)
	if err := os.MkdirAll(RuntimeRoot, 0700); err != nil {
		return nil, exception.NewGenericError(err, exception.SystemError)
	}
	factory := &LinuxContainerFactory{}
	return factory, nil
}

type LinuxContainerFactory struct {
}

func (factory *LinuxContainerFactory) Create(id string, config *configs.ContainerConfig) (Container, error) {
	logrus.Infof("container factory creating container: %s", id)
	containerRoot := filepath.Join(RuntimeRoot, id)
	// 如果该目录已经存在(err == nil)，则报错；如果有其他错误(忽略目录不存在的错，我们希望目录不存在)，则报错
	if _, err := os.Stat(containerRoot); err == nil {
		return nil, exception.NewGenericError(fmt.Errorf("container with id exists: %v", id), exception.SystemError)
	} else if !os.IsNotExist(err) {
		return nil, exception.NewGenericError(err, exception.SystemError)
	}
	logrus.Infof("mkdir containerRoot: %s", containerRoot)
	if err := os.MkdirAll(containerRoot, 0711); err != nil {
		return nil, exception.NewGenericError(err, exception.SystemError)
	}
	container := &LinuxContainer{
		id:            id,
		root:          containerRoot,
		config:        *config,
		cgroupManager: cgroups.NewCroupManager(id, make(map[string]string)),
	}
	container.statusBehavior = &StoppedStatusBehavior{c: container}
	logrus.Infof("create container complete, container: %#v", container)
	return container, nil
}

func (factory *LinuxContainerFactory) Load(id string) (Container, error) {
	containerRoot := filepath.Join(RuntimeRoot, id)
	state, err := factory.loadContainerState(containerRoot, id)
	if err != nil {
		return nil, err
	}
	container := &LinuxContainer{
		id:            id,
		createdTime:   state.Created,
		root:          containerRoot,
		config:        state.Config,
		cgroupManager: cgroups.NewCroupManager(id, state.CgroupPaths),
	}
	container.initProcess = NewParentNoChildProcess(state.InitProcessPid, state.InitProcessStartTime, container)
	detectedStatus, err := container.detectContainerStatus()
	if err != nil {
		return nil, err
	}
	// 目前的状态
	container.statusBehavior, err = NewContainerStatusBehavior(detectedStatus, container)
	if err != nil {
		return nil, err
	}
	return container, nil
}

func (factory *LinuxContainerFactory) StartInitialization() error {
	defer func() {
		if e := recover(); e != nil {
			logrus.Errorf("panic from initialization: %v, %v", e, string(debug.Stack()))
		}
	}()
	configPipeEnv := os.Getenv(EnvConfigPipe)
	initPipeFd, err := strconv.Atoi(configPipeEnv)
	logrus.WithField("init", true).Infof("got config pipe env: %d", initPipeFd)
	if err != nil {
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "converting EnvConfigPipe to int")
	}
	initializerType := InitializerType(os.Getenv(EnvInitializerType))
	logrus.WithField("init", true).Infof("got initializer type: %s", initializerType)

	// 读取config
	configPipe := os.NewFile(uintptr(initPipeFd), "configPipe")
	logrus.WithField("init", true).Infof("open child pipe: %#v", configPipe)
	logrus.WithField("init", true).Infof("starting to read init config from child pipe")
	bytes, err := ioutil.ReadAll(configPipe)
	if err != nil {
		logrus.WithField("init", true).Errorf("read init config failed: %s", err.Error())
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "reading init config from configPipe")
	}
	// child 读完就关
	if err = configPipe.Close(); err != nil {
		logrus.Errorf("closing parent pipe failed: %s", err.Error())
	}
	logrus.Infof("read init config complete, unmarshal bytes")
	initConfig := &InitConfig{}
	if err = json.Unmarshal(bytes, initConfig); err != nil {
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "unmarshal init config")
	}
	logrus.WithField("init", true).Infof("read init config from child pipe: %#v", initConfig)

	// 环境变量设置
	if err := populateProcessEnvironment(initConfig.ProcessConfig.Env); err != nil {
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "populating environment variables")
	}

	// 创建Initializer
	initializer, err := NewInitializer(initializerType, initConfig, configPipe)
	if err != nil {
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "creating initializer")
	}
	logrus.WithField("init", true).Infof("created initializer:%#v", initializer)

	// 正式开始初始化
	if err := initializer.Init(); err != nil {
		return exception.NewGenericErrorWithContext(err, exception.SystemError, "executing init command")
	}
	return nil
}

// populateProcessEnvironment loads the provided environment variables into the
// current processes's environment.
func populateProcessEnvironment(env []string) error {
	for _, pair := range env {
		p := strings.SplitN(pair, "=", 2)
		if len(p) < 2 {
			return fmt.Errorf("invalid environment '%v'", pair)
		}
		logrus.WithField("init", true).Infof("set env: key:%s, value:%s", p[0], p[1])
		if err := os.Setenv(p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

func (factory *LinuxContainerFactory) loadContainerState(containerRoot, id string) (*StateStorage, error) {
	stateFilePath := filepath.Join(containerRoot, StateFilename)
	f, err := os.Open(stateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exception.NewGenericError(fmt.Errorf("container %s does not exist", id), exception.ContainerNotExists)
		}
		return nil, exception.NewGenericError(err, exception.SystemError)
	}
	defer f.Close()
	var state *StateStorage
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		return nil, exception.NewGenericError(err, exception.SystemError)
	}
	return state, nil
}
