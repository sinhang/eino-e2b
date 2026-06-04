package e2b

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk/filesystem"
)

// Execute runs a shell command inside the E2B sandbox via the Python code
// interpreter (port 49999), which is the only reliable execution path on Cube
// deployments. The command is wrapped in a Python subprocess call that captures
// stdout, stderr, and exit code.
func (b *Backend) Execute(ctx context.Context, req *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("e2b: execute: command is required")
	}

	cmdB64 := base64.StdEncoding.EncodeToString([]byte(req.Command))
	script := fmt.Sprintf(executePythonScript, cmdB64)

	output, err := b.runPython(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("e2b: execute failed: %w", err)
	}

	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	}
	if json.Unmarshal([]byte(output), &out) == nil {
		exitCode := out.ExitCode
		resp := &filesystem.ExecuteResponse{
			Output:   out.Stdout,
			ExitCode: &exitCode,
		}
		if out.Stderr != "" {
			if resp.Output != "" {
				resp.Output += "\n[stderr]:\n" + out.Stderr
			} else {
				resp.Output = "[stderr]:\n" + out.Stderr
			}
		}
		return resp, nil
	}

	// Fallback: treat raw output as command output.
	exitCode := 0
	return &filesystem.ExecuteResponse{
		Output:   output,
		ExitCode: &exitCode,
	}, nil
}

const executePythonScript = `
import subprocess, json, base64, sys

cmd = base64.b64decode(%[1]q).decode('utf-8')

try:
    p = subprocess.run(
        cmd,
        shell=True,
        capture_output=True,
        text=True,
        timeout=300,
    )
    result = {
        'stdout': p.stdout,
        'stderr': p.stderr,
        'exitCode': p.returncode,
    }
except subprocess.TimeoutExpired:
    result = {
        'stdout': '',
        'stderr': 'command timed out after 300s',
        'exitCode': -1,
    }
except Exception as e:
    result = {
        'stdout': '',
        'stderr': str(e),
        'exitCode': -1,
    }

print(json.dumps(result))
`

var _ filesystem.Shell = (*Backend)(nil)
