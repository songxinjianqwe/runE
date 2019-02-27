package libcapsule

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"syscall"
)

type ParentProcess interface {
	// pid returns the pid for the running process.
	pid() int

	// create starts the process execution.
	start() error

	// send a SIGKILL to the process and wait for the exit.
	terminate() error

	// wait waits on the process returning the process state.
	wait() error

	// startTime returns the process create time.
	startTime() (uint64, error)

	// send signal to the process
	signal(os.Signal) error

	// detach returns the process is detach
	detach() bool
}

/**
一个进程默认有三个文件描述符，stdin、stdout、stderr
外带的文件描述符在这三个fd之后
*/
const DefaultStdFdCount = 3

/**
创建一个ProcessWrapper实例，用于启动容器进程
有可能是InitProcessWrapper，也有可能是SetnsProcessWrapper
*/
func NewParentProcess(container *LinuxContainer, process *Process) (ParentProcess, error) {
	logrus.Infof("new parent process...")
	logrus.Infof("creating pipes...")
	// Config: parent 写，child(init process)读
	childConfigPipe, parentConfigPipe, err := os.Pipe()
	logrus.Infof("create config pipe complete, parentConfigPipe: %#v, configPipe: %#v", parentConfigPipe, childConfigPipe)

	cmd, err := buildCommand(container,
		process, childConfigPipe)
	logrus.Infof("build command complete, command: %#v", cmd)
	if err != nil {
		return nil, err
	}
	if process.Init {
		return NewParentInitProcess(process, cmd, parentConfigPipe, container), nil
	} else {
		return NewParentSetnsProcess(process, cmd, parentConfigPipe), nil
	}
}

/**
构造一个command对象
*/
func buildCommand(container *LinuxContainer, process *Process, childConfigPipe *os.File) (*exec.Cmd, error) {
	cmd := exec.Command(ContainerInitCmd, ContainerInitArgs)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: container.config.Namespaces.CloneFlags(),
	}
	cmd.Dir = container.config.Rootfs
	cmd.ExtraFiles = append(cmd.ExtraFiles, childConfigPipe)
	cmd.Env = append(cmd.Env,
		fmt.Sprintf(EnvConfigPipe+"=%d", DefaultStdFdCount+len(cmd.ExtraFiles)-1),
	)
	cmd.Stdin = process.Stdin
	cmd.Stdout = process.Stdout
	cmd.Stderr = process.Stderr
	return cmd, nil
}