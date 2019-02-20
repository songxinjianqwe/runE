package libcapsule

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"syscall"
)

type ProcessWrapper interface {
	// pid returns the pid for the running process.
	pid() int

	// start starts the process execution.
	start() error

	// send a SIGKILL to the process and wait for the exit.
	terminate() error

	// wait waits on the process returning the process containerState.
	wait() (*os.ProcessState, error)

	// startTime returns the process start time.
	startTime() (uint64, error)

	signal(os.Signal) error
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
func NewParentProcess(container *LinuxContainerImpl, process *Process) (ProcessWrapper, error) {
	logrus.Infof("new parent process...")
	logrus.Infof("creating pipes...")
	// Config: parent 写，child(init process)读
	childConfigPipe, parentConfigPipe, err := os.Pipe()
	logrus.Infof("create config pipe complete, parentConfigPipe: %#v, configPipe: %#v", parentConfigPipe, childConfigPipe)

	var (
		// 只对init类型的process有效
		parentExecPipe *os.File = nil
		childExecPipe  *os.File = nil
	)
	if process.Init {
		// Exec信号: child(init process)写，parent 读
		parentExecPipe, childExecPipe, err := os.Pipe()
		logrus.Infof("create exec pipe complete, parentExecPipe: %#v, childExecPipe: %#v", parentExecPipe, childExecPipe)
		if err != nil {
			return nil, err
		}
	}

	cmd, err := buildCommand(container,
		process, childConfigPipe, childExecPipe, process.Init)
	logrus.Infof("build command complete, command: %#v", cmd)
	if err != nil {
		return nil, err
	}
	if process.Init {
		return NewInitProcessWrapper(process, cmd, parentConfigPipe, parentExecPipe, container), nil
	} else {
		return NewSetnsProcessWrapper(process, cmd, parentConfigPipe), nil
	}
}

/**
构造一个command对象
*/
func buildCommand(container *LinuxContainerImpl, process *Process, childConfigPipe *os.File, childExecPipe *os.File, init bool) (*exec.Cmd, error) {
	cmd := exec.Command(ContainerInitPath, ContainerInitArgs)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: container.config.Namespaces.CloneFlags(),
	}
	cmd.Dir = container.config.Rootfs
	cmd.ExtraFiles = append(cmd.ExtraFiles, childConfigPipe)
	cmd.Env = append(cmd.Env,
		fmt.Sprintf(EnvConfigPipe+"=%d", DefaultStdFdCount+len(cmd.ExtraFiles)-1),
	)
	if init {
		cmd.ExtraFiles = append(cmd.ExtraFiles, childExecPipe)
		cmd.Env = append(cmd.Env,
			fmt.Sprintf(EnvExecPipe+"=%d", DefaultStdFdCount+len(cmd.ExtraFiles)-1),
		)
	}
	cmd.Stdin = process.Stdin
	cmd.Stdout = process.Stdout
	cmd.Stderr = process.Stderr
	return cmd, nil
}
