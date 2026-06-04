package e2b

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	e2b "github.com/sinhang/e2b-go-sdk/e2b"
	"github.com/cloudwego/eino/adk/filesystem"
)

// LsInfo lists files and directories at the given path.
func (b *Backend) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	path := filepath.Clean(req.Path)
	// Use a Python script to get full FileInfo (path, is_dir, size, modified_at).
	// Base64-encode the path to avoid shell escaping issues.
	script := fmt.Sprintf(lsInfoPythonScript, path)
	result, err := b.client.Commands(b.sandboxID).Run(ctx, e2b.RunCommandRequest{
		Cmd:  "python3",
		Args: []string{"-c", script},
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: ls failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("e2b: ls script exited with code %d: %s", result.ExitCode, result.Stderr)
	}

	var files []filesystem.FileInfo
	output := strings.TrimSpace(result.Stdout)
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
//
// ReplaceAll controls whether all occurrences are replaced.
// If ReplaceAll is false and the old string appears more than once,
// the operation fails. The old and new strings must be different.
func (b *Backend) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	if req.OldString == "" {
		return fmt.Errorf("e2b: edit: old string is required")
	}
	if req.OldString == req.NewString {
		return fmt.Errorf("e2b: edit: new string must be different from old string")
	}

	path := filepath.Clean(req.FilePath)

	// Read current content
	content, err := b.Read(ctx, &filesystem.ReadRequest{
		FilePath: path,
		Offset:   1,
		Limit:    0, // read all
	})
	if err != nil {
		return fmt.Errorf("e2b: edit: read failed for %s: %w", path, err)
	}

	// Count occurrences
	count := strings.Count(content.Content, req.OldString)
	if count == 0 {
		return fmt.Errorf("e2b: edit: string not found in file %s", path)
	}
	if count > 1 && !req.ReplaceAll {
		return fmt.Errorf("e2b: edit: string appears %d times in %s, use ReplaceAll=true to replace all occurrences", count, path)
	}

	// Perform replacement
	var newContent string
	if req.ReplaceAll {
		newContent = strings.ReplaceAll(content.Content, req.OldString, req.NewString)
	} else {
		newContent = strings.Replace(content.Content, req.OldString, req.NewString, 1)
	}

	// Write back
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
//
// Falls back to grep if rg is not available.
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
	result, err := b.client.Commands(b.sandboxID).Run(ctx, e2b.RunCommandRequest{
		Cmd:  "python3",
		Args: []string{"-c", script},
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: grep failed: %w", err)
	}
	if result.ExitCode != 0 && result.ExitCode != 1 { // rg exit code 1 = no matches
		return nil, fmt.Errorf("e2b: grep script exited with code %d: %s", result.ExitCode, result.Stderr)
	}

	var matches []filesystem.GrepMatch
	output := strings.TrimSpace(result.Stdout)
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
	result, err := b.client.Commands(b.sandboxID).Run(ctx, e2b.RunCommandRequest{
		Cmd:  "python3",
		Args: []string{"-c", script},
	})
	if err != nil {
		return nil, fmt.Errorf("e2b: glob failed: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("e2b: glob script exited with code %d: %s", result.ExitCode, result.Stderr)
	}

	var files []filesystem.FileInfo
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return files, nil
	}
	if err := json.Unmarshal([]byte(output), &files); err != nil {
		return nil, fmt.Errorf("e2b: glob: failed to parse output: %w", err)
	}
	return files, nil
}

// --- helper functions ---

// applyLineOffsetLimit extracts a range of lines from content.
// offset is 1-indexed. Returns the sliced content.
func applyLineOffsetLimit(content string, offset, limit int) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	// offset is 1-indexed
	if offset > totalLines {
		return ""
	}

	start := offset - 1 // convert to 0-indexed
	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	return strings.Join(lines[start:end], "\n")
}

// buildGrepScript builds a Python script that wraps ripgrep to produce JSON output
// matching the filesystem.GrepMatch format.
func buildGrepScript(req *filesystem.GrepRequest) string {
	path := req.Path
	if path == "" {
		path = "."
	}

	caseFlag := ""
	if !req.CaseInsensitive {
		caseFlag = ""
	} else {
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

	// Escape single quotes in pattern
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
import subprocess, json, fnmatch, os

case_flag = %[1]q
multiline_flag = %[2]q
after_flag = %[3]q
before_flag = %[4]q
glob_flag = %[5]q
file_type_flag = %[6]q
pattern = %[7]q
search_path = %[8]q

# Try rg first, fall back to grep
try:
    cmd = "rg --json" + case_flag + multiline_flag + after_flag + before_flag + glob_flag + file_type_flag + " -e '" + pattern.replace("'", "'\"'\"'") + "' " + search_path
    result = subprocess.run(["rg", "--json"] +
        (["-i"] if case_flag.strip() else []) +
        (["-U", "--multiline-dotall"] if multiline_flag.strip() else []) +
        (["-A", after_flag.split()[-1]] if after_flag.strip() else []) +
        (["-B", before_flag.split()[-1]] if before_flag.strip() else []) +
        (["--glob", glob_flag.split()[-1].strip("'")] if glob_flag.strip() else []) +
        (["--type", file_type_flag.split()[-1]] if file_type_flag.strip() else []) +
        ["-e", pattern, "--", search_path],
        capture_output=True, text=True)
    if result.returncode not in (0, 1):
        raise RuntimeError("rg failed: " + result.stderr)
except (FileNotFoundError, Exception):
    # Fall back to grep
    try:
        grep_cmd = ["grep", "-rn"]
        if case_flag.strip():
            grep_cmd.append("-i")
        grep_cmd.extend([pattern, search_path])
        result = subprocess.run(grep_cmd, capture_output=True, text=True)
        if result.returncode > 1:
            raise RuntimeError("grep failed: " + result.stderr)
    except FileNotFoundError:
        print("[]")
        exit(0)

# Parse output
responses = []
for line in result.stdout.strip().split("\n"):
    if not line:
        continue
    # Try JSON (rg output)
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
    # Try grep format: path:line:content
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

print(json.dumps(responses))
`

const globPythonScript = `
import glob as glob_m, os, json

path = %[1]q
pattern = %[2]q

os.chdir(path)
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
