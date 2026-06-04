package e2b

import (
	"context"
	"fmt"

	e2b "github.com/sinhang/e2b-go-sdk/e2b"
	"github.com/cloudwego/eino/adk/filesystem"
)

// Execute runs a shell command inside the E2B sandbox.
func (b *Backend) Execute(ctx context.Context, req *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("e2b: execute: command is required")
	}

	result, err := b.client.Commands(b.sandboxID).Run(ctx, e2b.RunCommandRequest{
		Cmd:  "/bin/sh",
		Args: []string{"-c", req.Command},
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: execute failed: %w", err)
	}

	exitCode := result.ExitCode
	resp := &filesystem.ExecuteResponse{
		Output:   result.Stdout,
		ExitCode: &exitCode,
	}

	// Include stderr in output if present
	if result.Stderr != "" {
		if resp.Output != "" {
			resp.Output += "\n[stderr]:\n" + result.Stderr
		} else {
			resp.Output = "[stderr]:\n" + result.Stderr
		}
	}

	return resp, nil
}

// Compile-time interface check for Shell.
var _ filesystem.Shell = (*Backend)(nil)
