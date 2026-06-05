package internal

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"
	"k8s.io/utils/exec"
)

// straceMountExec is CANARY INSTRUMENTATION — DO NOT MERGE.
//
// It wraps an exec.Interface so the fs node's `mount` command runs under strace,
// writing a per-mount trace to a file. It exists only to validate the NFS mount
// end to end during canary testing — the mount(2) source/options and the
// connect() targets the kernel actually dials. Requires `strace` in the runtime
// image. Non-mount commands pass through unchanged.
type straceMountExec struct {
	exec.Interface
}

func (e straceMountExec) Command(cmd string, args ...string) exec.Cmd {
	if cmd == "mount" {
		return e.Interface.Command("strace", straceMountArgs(args)...)
	}

	return e.Interface.Command(cmd, args...)
}

func (e straceMountExec) CommandContext(ctx context.Context, cmd string, args ...string) exec.Cmd {
	if cmd == "mount" {
		return e.Interface.CommandContext(ctx, "strace", straceMountArgs(args)...)
	}

	return e.Interface.CommandContext(ctx, cmd, args...)
}

func straceMountArgs(mountArgs []string) []string {
	logPath := fmt.Sprintf("/tmp/crusoe-mount-strace-%d.log", time.Now().UnixNano())
	klog.Warningf("CANARY: tracing mount under strace -> %s", logPath)

	// -f follows mount.nfs forks; -tt timestamps; -s 512 keeps long mount-option
	// strings intact; connect/sendto/sendmsg show the IPs the kernel dials.
	straceArgs := []string{
		"-f", "-tt", "-s", "512",
		"-e", "trace=mount,connect,sendto,sendmsg",
		"-o", logPath, "mount",
	}

	return append(straceArgs, mountArgs...)
}
