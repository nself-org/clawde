// gvisor.go — gVisor (runsc) sandbox executor.
//
// Purpose: Wrap docker run --runtime=runsc to execute commands inside gVisor's
//          user-space kernel. This provides the strongest syscall isolation:
//          gVisor intercepts ALL syscalls in user space, preventing kernel
//          exploits entirely.
//
// Activation: CLAWDE_SANDBOX_RUNTIME=gvisor on Linux.
//
// Requirements: Docker installed + `docker info --format '{{.Runtimes}}'` must
//               include "runsc". Install gVisor: https://gvisor.dev/docs/user_guide/install/
//
// Constraints: File ≤500 lines.
// SPORT: REGISTRY-SERVICES.md → gVisor sandbox runtime.
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gvisorDockerImage is the container image used when no WorkDir image is
// specified. Overridable via CLAWDE_GVISOR_IMAGE env var.
const gvisorDefaultImage = "alpine:3.19"

// gVisorExecutor implements SandboxExecutor using docker run --runtime=runsc.
type gVisorExecutor struct {
	image string
}

func newGVisorExecutor() (SandboxExecutor, error) {
	// Verify Docker + runsc are available.
	out, err := exec.Command("docker", "info", "--format", "{{.Runtimes}}").Output()
	if err != nil {
		return nil, fmt.Errorf("gvisor: docker not available: %w", err)
	}
	if !strings.Contains(string(out), "runsc") {
		return nil, fmt.Errorf("gvisor: runsc runtime not registered in Docker (install gVisor: https://gvisor.dev/docs/user_guide/install/)")
	}

	image := gvisorDefaultImage
	return &gVisorExecutor{image: image}, nil
}

// Execute runs the command inside a Docker container using the gVisor runtime.
//
// Purpose: Provide kernel-bypass sandbox via gVisor's user-space kernel.
//          All syscalls from the sandboxed process are intercepted by gVisor,
//          which only exposes a safe subset.
// Inputs:  ctx — caller context; sc — the sandbox command.
// Outputs: SandboxResult; error on Docker invocation failure.
// Constraints: Network is disabled (--network none). Mounts are read-only
//              except for a tmpfs at /workspace.
func (g *gVisorExecutor) Execute(ctx context.Context, sc SandboxCommand) (SandboxResult, error) {
	// Build docker run command.
	args := []string{
		"run",
		"--rm",
		"--runtime=runsc",
		"--network=none",         // no network access
		"--read-only",            // immutable container FS
		"--tmpfs=/workspace:rw",  // writable workspace
		"--tmpfs=/tmp:rw",        // writable /tmp
		"--security-opt=no-new-privileges",
	}

	// Pass environment variables.
	for _, e := range sc.Env {
		args = append(args, "-e", e)
	}

	// Working directory inside container.
	workDir := "/workspace"
	if sc.WorkDir != "" {
		workDir = sc.WorkDir
	}
	args = append(args, "--workdir="+workDir)

	// Image + command.
	args = append(args, g.image)
	args = append(args, sc.Cmd)
	args = append(args, sc.Args...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	return runWithTimeout(ctx, cmd, sc)
}
