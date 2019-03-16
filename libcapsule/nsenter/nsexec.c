#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <fcntl.h>
#include <errno.h>
#include <unistd.h>
#include <sched.h>
#include <setjmp.h>


const char* LOG_PREFIX 	   			= "[EXEC]";
const char* ENV_CONFIG_PIPE      	= "_LIBCAPSULE_CONFIG_PIPE";
const char* ENV_INITIALIZER_TYPE 	= "_LIBCAPSULE_INITIALIZER_TYPE";
const char* NS_DELIMETER 			= ",";
const char* CMD_DELIMETER 			= " ";
const char*	EXEC_INITIALIZER  		= "exec";
const int ERROR 					= 1;
const int OK						= 0;

#define JUMP_PARENT 0x00
#define JUMP_CHILD  0xA0

int enter_namespaces(int config_pipe_fd);
int child_func(void *arg) __attribute__ ((noinline));

// 1.某个进程创建后其pid namespace就固定了，使用setns和unshare改变后，其本身的pid namespace不会改变，只有fork出的子进程的pid namespace改变(改变的是每个进程的nsproxy->pid_namespace_for_children)
// 因为PID对用户态的函数而言是一个固定值,不存在更换PID Namespace的问题,它意味着更换PID,会出问题.
// 2.用setns进入mnt namespace应该放在其他namespace之后，否则可能出现无法打开/proc/pid/ns/…的错误

char child_stack[4096] __attribute__ ((aligned(16)));

// 直接return是没法进入go runtime的,long jump可以回到nsexec的堆栈.
// 函数longjmp()使程序在最近一次调用setjmp()处重新执行。
int child_func(void *arg) {
    printf("%s child started, just goto Go Runtime\n", LOG_PREFIX);
    jmp_buf* env  = (jmp_buf*)arg;
   	longjmp(*env, JUMP_CHILD);
}

void nsexec() {
    // init和exec都会进入此段代码
	const char* type = getenv(ENV_INITIALIZER_TYPE);
	if (!type || strcmp(type, EXEC_INITIALIZER) != 0) {
		return;
	}
    jmp_buf env;
    switch(setjmp(env)) {
        case JUMP_PARENT:
            printf("%s start to read namespaces\n", LOG_PREFIX);
            const char* config_pipe_env = getenv(ENV_CONFIG_PIPE);
            printf("%s read config pipe env: %s\n", LOG_PREFIX, config_pipe_env);
            int config_pipe_fd = atoi(config_pipe_env);
            printf("%s config pipe fd: %d\n", LOG_PREFIX, config_pipe_fd);
            if (config_pipe_fd <= 0) {
                printf("%s converting config pipe to int failed\n", LOG_PREFIX);
                exit(ERROR);
            }
            // 先加入已有的
            int status = join_namespaces(config_pipe_fd);
            if (status < 0) {
                exit(status);
            }
            // 再创建新的

            // 最后让child进入go runtime,因为自己setns后无法进入新的PID NS,只有child才能.
            status = clone_child(config_pipe_fd, env);
            printf("%s exec process exited\n", LOG_PREFIX);
            exit(status);
        case JUMP_CHILD:
            printf("%s JUMP_CHILD succeeded\n", LOG_PREFIX);
            return;
    }
}

int clone_child(int config_pipe_fd, jmp_buf* env) {
    int child_pid = clone(child_func, child_stack, CLONE_PARENT, env);
    if (child_pid < 0) {
        printf("%s clone child failed, child pid is %d\n", LOG_PREFIX, child_pid);
        return ERROR;
    }
    printf("%s clone child succeeded, child pid is %d\n", LOG_PREFIX, child_pid);
    int status = writeInt(config_pipe_fd, child_pid);
    if (status < 0) {
        printf("%s write child pid to pipe failed, cause: %s\n", LOG_PREFIX, strerror(errno));
    } else {
        printf("%s write child pid to pipe succeeded\n", LOG_PREFIX);
    }
    return status;
}

int join_namespaces(int config_pipe_fd) {
	// 读出namespaces的长度
	int nsLen = readInt(config_pipe_fd);
	printf("%s read namespace len: %d\n", LOG_PREFIX, nsLen);

	// 再读出namespaces
	char namespaces[nsLen];
	if (read(config_pipe_fd, namespaces, nsLen) < 0) {
		printf("%s read namespaces failed\n", LOG_PREFIX);
		return ERROR;
	}
	namespaces[nsLen] = '\0';
	printf("%s read namespaces: %s\n", LOG_PREFIX, namespaces);
	char* ns = strtok(namespaces, NS_DELIMETER);
	while(ns) {
        printf("%s current namespace_path is %s\n", LOG_PREFIX, ns);
        int result = nsenter(ns);
        printf("\n");
        if (result < 0) {
			return ERROR;
        }
		ns = strtok(NULL, NS_DELIMETER);
	}
	printf("%s enter namespaces succeeded\n", LOG_PREFIX);
	return OK;
}

int readInt(int config_pipe_fd) {
    char intBuffer[4];
	if (read(config_pipe_fd, intBuffer, 4) < 0) {
		printf("%s read namespace length failed\n", LOG_PREFIX);
		return ERROR;
	}
	return (intBuffer[0] << 24) + (intBuffer[1] << 16) + (intBuffer[2] << 8) + intBuffer[3];
}

int writeInt(int config_pipe_fd, int data) {
    char intBuffer[4];
    intBuffer[0] = data >> 24;
    intBuffer[1] = data >> 16;
    intBuffer[2] = data >> 8;
    intBuffer[3] = data;
    if(write(config_pipe_fd, intBuffer, 4) < 0) {
        printf("%s read namespaces failed\n", LOG_PREFIX);
        return ERROR;
    }
    return OK;
}

int nsenter(char* namespace_path) {
    printf("%s entering namespace_path %s ...\n", LOG_PREFIX, namespace_path);
    int fd = open(namespace_path, O_RDONLY);
    if (fd < 0) {
        printf("%s open %s failed, cause: %s\n", LOG_PREFIX, namespace_path, strerror(errno));
        return -1;
    }
    // int setns(int fd, int nstype)
    // 参数fd表示我们要加入的namespace的文件描述符,它是一个指向/proc/[pid]/ns目录的文件描述符，可以通过直接打开该目录下的链接或者打开一个挂载了该目录下链接的文件得到。
    // 参数nstype让调用者可以去检查fd指向的namespace类型是否符合我们实际的要求。如果填0表示不检查。
    if (setns(fd, 0) < 0) {
        close(fd);
        // Linux中系统调用的错误都存储于 errno中，errno由操作系统维护，存储就近发生的错误，即下一次的错误码会覆盖掉上一次的错误。
        // 字符串显示错误信息 / strerror
        printf("%s enter namespace %s failed, cause: %s\n", LOG_PREFIX, namespace_path, strerror(errno));
        return -1;
    } else {
        close(fd);
        printf("%s enter namespace %s succeeded\n", LOG_PREFIX, namespace_path);
    	return 0;
    }
}
