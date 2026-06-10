package e2b

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/schema"
	e2b "github.com/sinhang/e2b-go-sdk/e2b"
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

// ExecuteStreaming runs a shell command and streams stdout/stderr in real time.
//
// Unlike Execute, which waits for the command to finish, ExecuteStreaming
// sends output chunks as they are produced. The final chunk carries the
// exit code and sets Truncated=false.
func (b *Backend) ExecuteStreaming(ctx context.Context, req *filesystem.ExecuteRequest) (*schema.StreamReader[*filesystem.ExecuteResponse], error) {
	if req.Command == "" {
		return nil, fmt.Errorf("e2b: execute: command is required")
	}

	// Use a pipe to stream results back to the caller.
	pipeR, pipeW := schema.Pipe[*filesystem.ExecuteResponse](4)

	cmdB64 := base64.StdEncoding.EncodeToString([]byte(req.Command))
	script := fmt.Sprintf(executeStreamingPythonScript, cmdB64)

	go func() {
		defer pipeW.Close()

		exec, err := b.interpreter.Run(context.Background(), e2b.RunCodeRequest{
			Code:     script,
			Language: "python",
		})
		if err != nil {
			code := -1
			pipeW.Send(&filesystem.ExecuteResponse{
				Output:    fmt.Sprintf("e2b: execute streaming failed: %v", err),
				ExitCode:  &code,
				Truncated: true,
			}, nil)
			return
		}
		if exec.Error != nil {
			code := -1
			pipeW.Send(&filesystem.ExecuteResponse{
				Output:    fmt.Sprintf("e2b: python error: %s: %s", exec.Error.Name, exec.Error.Value),
				ExitCode:  &code,
				Truncated: true,
			}, nil)
			return
		}

		// The code interpreter returns NDJSON. Our Python script prints each
		// streaming chunk as a JSON line, plus a final "done" line.
		stdout := strings.Join(exec.Logs.Stdout, "\n")
		scanner := bufio.NewScanner(strings.NewReader(stdout))
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var chunk struct {
				Type     string `json:"type"`
				Stdout   string `json:"stdout"`
				Stderr   string `json:"stderr"`
				ExitCode *int   `json:"exitCode"`
			}
			if err := json.Unmarshal([]byte(line), &chunk); err != nil {
				continue
			}

			switch chunk.Type {
			case "chunk":
				output := chunk.Stdout
				if chunk.Stderr != "" {
					if output != "" {
						output += "\n"
					}
					output += "[stderr]: " + chunk.Stderr
				}
				pipeW.Send(&filesystem.ExecuteResponse{
					Output: output,
				}, nil)

			case "done":
				pipeW.Send(&filesystem.ExecuteResponse{
					Output:   chunk.Stdout,
					ExitCode: chunk.ExitCode,
				}, nil)
			}
		}
	}()

	return pipeR, nil
}

// executePythonScript runs a shell command and captures the full result as JSON.
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

// executeStreamingPythonScript runs a shell command and streams output line by
// line as JSON chunks, followed by a final "done" message with the exit code.
//
// Each chunk line:  {"type": "chunk", "stdout": "...", "stderr": "..."}
// Final line:       {"type": "done",  "stdout": "...", "stderr": "...", "exitCode": N}
const executeStreamingPythonScript = `
import subprocess, json, base64, sys, threading

cmd = base64.b64decode(%[1]q).decode('utf-8')

try:
    p = subprocess.Popen(
        cmd,
        shell=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
    )

    # Read stdout and stderr in parallel, collecting all output.
    stdout_lines = []
    stderr_lines = []

    def read_lines(stream, collector):
        for line in iter(stream.readline, ''):
            collector.append(line)
            # Print each line as a streaming chunk.
            chunk = {'type': 'chunk', 'stdout': '', 'stderr': ''}
            if stream is p.stdout:
                chunk['stdout'] = line.rstrip('\n')
            else:
                chunk['stderr'] = line.rstrip('\n')
            print(json.dumps(chunk), flush=True)
        stream.close()

    t1 = threading.Thread(target=read_lines, args=(p.stdout, stdout_lines))
    t2 = threading.Thread(target=read_lines, args=(p.stderr, stderr_lines))
    t1.daemon = True
    t2.daemon = True
    t1.start()
    t2.start()

    p.wait()
    t1.join(timeout=5)
    t2.join(timeout=5)

    stdout = ''.join(stdout_lines)
    stderr = ''.join(stderr_lines)

    # Send final done message with exit code.
    result = {
        'type': 'done',
        'stdout': stdout,
        'stderr': stderr,
        'exitCode': p.returncode,
    }
    print(json.dumps(result))

except Exception as e:
    result = {
        'type': 'done',
        'stdout': '',
        'stderr': str(e),
        'exitCode': -1,
    }
    print(json.dumps(result))
`

// Compile-time interface checks.
var (
	_ filesystem.Shell          = (*Backend)(nil)
	_ filesystem.StreamingShell = (*Backend)(nil)
)
