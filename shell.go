package e2b

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk/filesystem"
	e2b "github.com/sinhang/e2b-go-sdk/e2b"
)

// Execute runs a shell command inside the E2B sandbox via a Python wrapper script.
// Uses python3 subprocess to capture stdout, stderr, and exit code reliably.
func (b *Backend) Execute(ctx context.Context, req *filesystem.ExecuteRequest) (*filesystem.ExecuteResponse, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("e2b: execute: command is required")
	}

	// Base64-encode the command to avoid shell escaping issues inside
	// the Python template (same pattern used by all other Python scripts).
	cmdB64 := base64.StdEncoding.EncodeToString([]byte(req.Command))
	script := fmt.Sprintf(executePythonScript, cmdB64)

	result, err := b.client.Commands(b.sandboxID).Run(ctx, e2b.RunCommandRequest{
		Cmd:  "python3",
		Args: []string{"-c", script},
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: execute failed: %w", err)
	}

	// Try to parse JSON output from the Python wrapper.
	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exitCode"`
	}
	if json.Unmarshal([]byte(result.Stdout), &out) == nil {
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

	// Fallback: use raw output if JSON parsing fails.
	exitCode := result.ExitCode
	resp := &filesystem.ExecuteResponse{
		Output:   result.Stdout,
		ExitCode: &exitCode,
	}
	if result.Stderr != "" {
		if resp.Output != "" {
			resp.Output += "\n[stderr]:\n" + result.Stderr
		} else {
			resp.Output = "[stderr]:\n" + result.Stderr
		}
	}

	return resp, nil
}

// executePythonScript runs an arbitrary shell command via subprocess.run and
// prints the result as a single JSON object: {"stdout": "...", "stderr": "...", "exitCode": N}.
//
// The command is base64-encoded in the template parameter to avoid any shell
// escaping issues — matching the pattern used by all other Python scripts in
// this package.
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

// Compile-time interface check for Shell.
var _ filesystem.Shell = (*Backend)(nil)
