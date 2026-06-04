package e2b

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk/filesystem"
	e2b "github.com/sinhang/e2b-go-sdk/e2b"
)

// runPython executes a Python script inside the sandbox via the code interpreter
// (port 49999) and returns the stdout output.
//
// This is the only supported execution path on Cube deployments — envd (port
// 49983) and /process/start are not available.
func (b *Backend) runPython(ctx context.Context, script string) (string, error) {
	exec, err := b.interpreter.RunSimple(ctx, script)
	if err != nil {
		return "", fmt.Errorf("e2b: python execution failed: %w", err)
	}
	if exec.Error != nil {
		stderr := strings.Join(exec.Logs.Stderr, "\n")
		log.Printf("e2b: python error [%s]: %s (traceback=%s stderr=%q)",
			exec.Error.Name, exec.Error.Value, exec.Error.Traceback, stderr)
		return "", fmt.Errorf("e2b: python error: %s: %s", exec.Error.Name, exec.Error.Value)
	}

	stdout := strings.Join(exec.Logs.Stdout, "\n")
	stderr := strings.Join(exec.Logs.Stderr, "\n")

	// If the script produced no stdout and no stderr, the code interpreter
	// may have silently failed (e.g. non-NDJSON response silently discarded).
	// Log a warning and include stderr in the error if available.
	if stdout == "" && stderr == "" && len(script) > 0 {
		log.Printf("e2b: runPython: code interpreter returned empty response (stdout=0 stderr=0 results=%d)",
			len(exec.Results))
	}

	if stderr != "" {
		log.Printf("e2b: runPython stderr: %s", stderr)
	}

	return stdout, nil
}

// LsInfo lists files and directories at the given path.
func (b *Backend) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path := filepath.Clean(req.Path)
	script := fmt.Sprintf(lsInfoPythonScript, path)

	output, err := b.runPython(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("e2b: ls failed: %w", err)
	}

	var files []filesystem.FileInfo
	output = strings.TrimSpace(output)
	if output == "" {
		return files, nil
	}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		var fi filesystem.FileInfo
		if err := json.Unmarshal([]byte(line), &fi); err != nil {
			continue
		}
		files = append(files, fi)
	}
	return files, nil
}

// Read reads a file from the sandbox, optionally with line offset and limit.
//
// Offset is 1-indexed (1 means the first line). Values <= 0 default to 1.
// Limit <= 0 defaults to 2000 lines.
func (b *Backend) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	path := filepath.Clean(req.FilePath)

	resp, err := b.client.DownloadFile(ctx, e2b.DownloadFileRequest{Path: path})
	if err != nil {
		return nil, fmt.Errorf("e2b: read failed for %s: %w", path, err)
	}

	offset := req.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 2000
	}

	content := applyLineOffsetLimit(resp.Content, offset, limit)
	return &filesystem.FileContent{Content: content}, nil
}

// Write creates or overwrites a file in the sandbox.
// Parent directories are created automatically by the E2B API.
func (b *Backend) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	path := filepath.Clean(req.FilePath)

	_, err := b.client.UploadFile(ctx, e2b.UploadFileRequest{
		Path:    path,
		Content: req.Content,
	})
	if err != nil {
		return fmt.Errorf("e2b: write failed for %s: %w", path, err)
	}
	return nil
}

// Edit performs a string replacement in a file.
func (b *Backend) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	if req.OldString == "" {
		return fmt.Errorf("e2b: edit: old string is required")
	}
	if req.OldString == req.NewString {
		return fmt.Errorf("e2b: edit: new string must be different from old string")
	}

	path := filepath.Clean(req.FilePath)

	content, err := b.Read(ctx, &filesystem.ReadRequest{
		FilePath: path,
		Offset:   1,
		Limit:    0,
	})
	if err != nil {
		return fmt.Errorf("e2b: edit: read failed for %s: %w", path, err)
	}

	count := strings.Count(content.Content, req.OldString)
	if count == 0 {
		return fmt.Errorf("e2b: edit: string not found in file %s", path)
	}
	if count > 1 && !req.ReplaceAll {
		return fmt.Errorf("e2b: edit: string appears %d times in %s, use ReplaceAll=true to replace all occurrences", count, path)
	}

	var newContent string
	if req.ReplaceAll {
		newContent = strings.ReplaceAll(content.Content, req.OldString, req.NewString)
	} else {
		newContent = strings.Replace(content.Content, req.OldString, req.NewString, 1)
	}

	_, err = b.client.UploadFile(ctx, e2b.UploadFileRequest{
		Path:    path,
		Content: newContent,
	})
	if err != nil {
		return fmt.Errorf("e2b: edit: write failed for %s: %w", path, err)
	}
	return nil
}

// GrepRaw searches for a pattern in files using ripgrep (rg) inside the sandbox.
func (b *Backend) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	if req.Pattern == "" {
		return nil, fmt.Errorf("e2b: grep: pattern is required")
	}

	path := req.Path
	if path == "" {
		path = "."
	} else {
		path = filepath.Clean(path)
	}

	script := buildGrepScript(req)
	output, err := b.runPython(ctx, script)
	if err != nil {
		log.Printf("e2b: grep failed: path=%q pattern=%q err=%v", path, req.Pattern, err)
		return nil, fmt.Errorf("e2b: grep failed: %w", err)
	}

	var matches []filesystem.GrepMatch
	output = strings.TrimSpace(output)
	if output == "" {
		return matches, nil
	}
	if err := json.Unmarshal([]byte(output), &matches); err != nil {
		return nil, fmt.Errorf("e2b: grep: failed to parse output: %w", err)
	}
	return matches, nil
}

// GlobInfo finds files matching a glob pattern.
func (b *Backend) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	path := req.Path
	if path == "" {
		path = "."
	} else {
		path = filepath.Clean(path)
	}

	script := fmt.Sprintf(globPythonScript, path, req.Pattern)
	output, err := b.runPython(ctx, script)
	if err != nil {
		log.Printf("e2b: glob failed: path=%q pattern=%q err=%v", path, req.Pattern, err)
		return nil, fmt.Errorf("e2b: glob failed: %w", err)
	}

	var files []filesystem.FileInfo
	output = strings.TrimSpace(output)
	if output == "" {
		return files, nil
	}
	if err := json.Unmarshal([]byte(output), &files); err != nil {
		return nil, fmt.Errorf("e2b: glob: failed to parse output: %w", err)
	}
	return files, nil
}

// --- helper functions ---

func applyLineOffsetLimit(content string, offset, limit int) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if offset > totalLines {
		return ""
	}

	start := offset - 1
	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	return strings.Join(lines[start:end], "\n")
}

func buildGrepScript(req *filesystem.GrepRequest) string {
	path := req.Path
	if path == "" {
		path = "."
	}

	caseFlag := ""
	if req.CaseInsensitive {
		caseFlag = " -i"
	}

	multilineFlag := ""
	if req.EnableMultiline {
		multilineFlag = " -U --multiline-dotall"
	}

	afterFlag := ""
	if req.AfterLines > 0 {
		afterFlag += fmt.Sprintf(" -A %d", req.AfterLines)
	}
	beforeFlag := ""
	if req.BeforeLines > 0 {
		beforeFlag += fmt.Sprintf(" -B %d", req.BeforeLines)
	}

	globFlag := ""
	if req.Glob != "" {
		globFlag += fmt.Sprintf(" --glob '%s'", req.Glob)
	}
	fileTypeFlag := ""
	if req.FileType != "" {
		fileTypeFlag += fmt.Sprintf(" --type %s", req.FileType)
	}

	pattern := strings.ReplaceAll(req.Pattern, "'", "'\"'\"'")

	return fmt.Sprintf(grepPythonScript, caseFlag, multilineFlag, afterFlag, beforeFlag, globFlag, fileTypeFlag, pattern, path)
}

// --- Python scripts for sandbox operations ---

const lsInfoPythonScript = `
import json, os
from datetime import datetime, timezone

path = %[1]q
try:
    with os.scandir(path) as it:
        for entry in sorted(it, key=lambda e: e.name):
            try:
                stat = entry.stat(follow_symlinks=False)
            except OSError:
                stat = None
            mtime = stat.st_mtime if stat else 0
            if isinstance(mtime, (int, float)):
                mtime = datetime.fromtimestamp(mtime, tz=timezone.utc).isoformat()
            result = {
                'Path': entry.name,
                'IsDir': entry.is_dir(follow_symlinks=False),
                'Size': stat.st_size if stat else 0,
                'ModifiedAt': mtime if mtime else ''
            }
            print(json.dumps(result))
except FileNotFoundError:
    pass
except PermissionError:
    pass
`

const grepPythonScript = `
import subprocess, json, fnmatch, os, sys

case_flag = %[1]q
multiline_flag = %[2]q
after_flag = %[3]q
before_flag = %[4]q
glob_flag = %[5]q
file_type_flag = %[6]q
pattern = %[7]q
search_path = %[8]q

# Verify search path exists before attempting search.
if search_path and not os.path.exists(search_path):
    print("grep: path does not exist: " + repr(search_path), file=sys.stderr)
    print("[]")
    exit(0)

responses = []

def parse_output(output):
    for line in output.strip().split("\n"):
        if not line:
            continue
        try:
            data = json.loads(line)
            if data.get("type") not in ("match", "context"):
                continue
            match_data = data.get("data", {})
            match_path = match_data.get("path", {}).get("text", "")
            lines_data = match_data.get("lines", {})
            responses.append({
                "Path": match_path,
                "Line": match_data.get("line_number", 0),
                "Content": lines_data.get("text", "").rstrip("\n")
            })
            continue
        except json.JSONDecodeError:
            pass
        parts = line.split(":", 2)
        if len(parts) >= 3:
            try:
                line_num = int(parts[1])
            except ValueError:
                continue
            responses.append({
                "Path": parts[0],
                "Line": line_num,
                "Content": parts[2].rstrip("\n")
            })

try:
    result = subprocess.run(["rg", "--json"] +
        (["-i"] if case_flag.strip() else []) +
        (["-U", "--multiline-dotall"] if multiline_flag.strip() else []) +
        (["-A", after_flag.split()[-1]] if after_flag.strip() else []) +
        (["-B", before_flag.split()[-1]] if before_flag.strip() else []) +
        (["--glob", glob_flag.split()[-1].strip("'")] if glob_flag.strip() else []) +
        (["--type", file_type_flag.split()[-1]] if file_type_flag.strip() else []) +
        ["-e", pattern, "--", search_path],
        capture_output=True, text=True)
    if result.returncode == 0 or result.returncode == 1:
        parse_output(result.stdout)
    elif result.returncode > 1:
        print("rg failed: " + result.stderr, file=sys.stderr)
        raise RuntimeError(result.stderr)
except:
    try:
        grep_cmd = ["grep", "-rn"]
        if case_flag.strip():
            grep_cmd.append("-i")
        grep_cmd.extend([pattern, search_path])
        result = subprocess.run(grep_cmd, capture_output=True, text=True)
        if result.returncode <= 1:
            parse_output(result.stdout)
        elif result.returncode > 1:
            print("grep error (code " + str(result.returncode) + "): " + result.stderr[:200], file=sys.stderr)
    except Exception as e:
        print("grep search error: " + str(e), file=sys.stderr)

print(json.dumps(responses))
`

const globPythonScript = `
import glob as glob_m, os, json

path = %[1]q
pattern = %[2]q

try:
    os.chdir(path)
except (FileNotFoundError, NotADirectoryError, PermissionError) as e:
    import sys
    print("glob: path does not exist or not accessible: path=" + repr(path) + " error=" + str(e), file=sys.stderr)
    print(json.dumps([]))
    exit(0)

matches = sorted(glob_m.glob(pattern, recursive=True))
results = []
for m in matches:
    try:
        stat = os.stat(m)
    except OSError:
        stat = None
    result = {
        'Path': m,
        'Size': stat.st_size if stat else 0,
        'ModifiedAt': '',
        'IsDir': os.path.isdir(m)
    }
    results.append(result)
print(json.dumps(results))
`
