package util

import (
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/songxinjianqwe/capsule/libcapsule"
	"github.com/songxinjianqwe/capsule/libcapsule/util/exception"
	specutil "github.com/songxinjianqwe/capsule/libcapsule/util/spec"
	"io/ioutil"
	"os"
	"sync"
)

type ContainerAction uint8

const (
	ContainerActCreate ContainerAction = iota
	ContainerActRun
)

func (action ContainerAction) String() string {
	switch action {
	case ContainerActCreate:
		return "ContainerActCreate"
	case ContainerActRun:
		return "ContainerActRun"
	default:
		return "Unknown ContainerAction"
	}
}

/**
创建或启动容器
create
or
create and start
*/
func LaunchContainer(id string, spec *specs.Spec, action ContainerAction, init bool, detach bool) (int, error) {
	logrus.Infof("launching container: %s, action: %s", id, action)
	var container libcapsule.Container
	var err error
	if init {
		container, err = CreateContainer(id, spec)
	} else {
		container, err = GetContainer(id)
	}
	if err != nil {
		return -1, err
	}
	// 将specs.Process转为libcapsule.Process
	process, err := newProcess(*spec.Process, init, detach)
	logrus.Infof("new process complete, libcapsule.Process: %#v", process)
	if err != nil {
		return -1, err
	}
	var containerErr error
	switch action {
	case ContainerActCreate:
		// 如果是create，那么不管是否是terminal，都不会删除容器
		containerErr = container.Create(process)
	case ContainerActRun:
		// c.run == c.start + c.exec [+ c.destroy]
		containerErr = container.Run(process)
	}
	if containerErr != nil {
		if err := container.Destroy(); err != nil {
			logrus.Errorf(fmt.Sprintf("container create failed, try to destroy it but failed again, cause: %s", containerErr.Error()))
			return -1, exception.NewGenericErrorWithContext(
				err,
				exception.SystemError,
				fmt.Sprintf("container create failed, try to destroy it but failed again, cause: %s", containerErr.Error()))
		}
		return -1, containerErr
	}
	// 如果是Run命令运行容器吗，并且是前台运行，那么Run结束，即为容器进程结束，则删除容器
	if action == ContainerActRun && !detach {
		if err := container.Destroy(); err != nil {
			return -1, err
		}
	}
	return 0, nil
}

/**
根据id读取一个Container
*/
func GetContainer(id string) (libcapsule.Container, error) {
	if id == "" {
		return nil, fmt.Errorf("container id cannot be empty")
	}
	factory, err := LoadFactory()
	if err != nil {
		return nil, err
	}
	return factory.Load(id)
}

/**
查询所有的id
*/
func GetContainerIds() ([]string, error) {
	var ids []string
	if _, err := os.Stat(libcapsule.RuntimeRoot); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
	}
	list, err := ioutil.ReadDir(libcapsule.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	for _, fileInfo := range list {
		ids = append(ids, fileInfo.Name())
	}
	return ids, nil
}

/**
创建容器实例
*/
func CreateContainer(id string, spec *specs.Spec) (libcapsule.Container, error) {
	logrus.Infof("creating container: %s", id)
	if id == "" {
		return nil, fmt.Errorf("container id cannot be empty")
	}
	// 1、将spec转为容器config
	config, err := specutil.CreateContainerConfig(id, spec)
	logrus.Infof("convert complete, config: %#v", config)
	if err != nil {
		return nil, err
	}
	// 2、创建容器工厂
	factory, err := LoadFactory()
	if err != nil {
		return nil, err
	}
	// 3、创建容器
	container, err := factory.Create(id, config)
	if err != nil {
		return nil, err
	}
	return container, nil
}

var (
	singletonFactory    libcapsule.Factory
	singletonFactoryErr error
	once                sync.Once
)

/*
创建容器工厂
*/
func LoadFactory() (libcapsule.Factory, error) {
	once.Do(func() {
		singletonFactory, singletonFactoryErr = libcapsule.NewFactory()
	})
	return singletonFactory, singletonFactoryErr
}

/*
将specs.Process转为libcapsule.Process
*/
func newProcess(p specs.Process, init bool, detach bool) (*libcapsule.Process, error) {
	logrus.Infof("converting specs.Process to libcapsule.Process")
	libcapsuleProcess := &libcapsule.Process{
		Args:   p.Args,
		Env:    p.Env,
		Cwd:    p.Cwd,
		Init:   init,
		Detach: detach,
	}
	return libcapsuleProcess, nil
}
