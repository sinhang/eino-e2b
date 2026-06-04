package e2b

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	e2b "github.com/sinhang/e2b-go-sdk/e2b"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHandler is a mutable handler for the mock server (set per-test to handle
// filesystem and command operations). Sandbox creation/deletion is handled automatically.
var mockHandler http.HandlerFunc

// setupTest creates a mock HTTP server and a Backend configured to use it.
// The mock server automatically handles:
//   - POST /sandboxes → returns sandbox ID
//   - DELETE /sandboxes/{id} → returns 204
//
// All other requests are dispatched to mockHandler, which tests must set.
func setupTest(t *testing.T) (*Backend, *httptest.Server) {
	t.Helper()

	const testSandboxID = "test-sandbox-123"

	// Use a closure to capture both the auto-handler and the mutable mockHandler.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle sandbox lifecycle automatically.
		if r.URL.Path == "/sandboxes" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockSandboxResponse(testSandboxID)))
			return
		}
		if strings.Contains(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Delegate everything else to the test's mockHandler.
		if mockHandler != nil {
			mockHandler(w, r)
		} else {
			http.Error(w, "mockHandler not set", http.StatusInternalServerError)
		}
	}))

	// Point both control plane and data plane to the mock server.
	cfg := &Config{
		APIKey:       "test-api-key",
		BaseURL:      server.URL,
		DataPlaneURL: server.URL,
		Domain:       "test.local",
		Template:     "test-template",
		TimeoutSec:   300,
	}

	backend, err := NewBackend(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.True(t, backend.autoCreated)
	assert.Equal(t, testSandboxID, backend.SandboxID())

	return backend, server
}

// mockSandboxResponse returns a valid sandbox creation JSON response.
func mockSandboxResponse(sandboxID string) string {
	return fmt.Sprintf(`{"sandboxID":"%s","templateID":"test-template","state":"running"}`, sandboxID)
}

// decodeBody is a helper to read and unmarshal a JSON request body.
func decodeBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	err = json.Unmarshal(body, v)
	require.NoError(t, err)
}

// ============================== Constructor Tests ==============================

func TestNewBackend(t *testing.T) {
	t.Run("Success: AutoCreateSandbox", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/sandboxes" && r.Method == http.MethodPost {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(mockSandboxResponse("test-sandbox-123")))
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := &Config{
			APIKey:       "test-key",
			BaseURL:      server.URL,
			DataPlaneURL: server.URL,
			Template:     "base",
		}
		backend, err := NewBackend(context.Background(), cfg)
		require.NoError(t, err)
		assert.Equal(t, "test-sandbox-123", backend.SandboxID())
		assert.True(t, backend.autoCreated)
	})

	t.Run("Success: PreExistingSandbox", func(t *testing.T) {
		backend, err := NewBackend(context.Background(), &Config{
			APIKey:    "test-key",
			SandboxID: "existing-sandbox",
		})
		require.NoError(t, err)
		assert.Equal(t, "existing-sandbox", backend.SandboxID())
		assert.False(t, backend.autoCreated)
	})

	t.Run("Failure: NilConfig", func(t *testing.T) {
		_, err := NewBackend(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config is required")
	})

	t.Run("Failure: SandboxCreateFails", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := &Config{
			APIKey:       "test-key",
			BaseURL:      server.URL,
			DataPlaneURL: server.URL,
			Template:     "base",
		}
		_, err := NewBackend(context.Background(), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create sandbox")
	})

	t.Run("Success: DefaultValues", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/sandboxes" && r.Method == http.MethodPost {
				var req e2b.CreateSandboxRequest
				decodeBody(t, r, &req)
				assert.Equal(t, "base", req.TemplateID)
				assert.Equal(t, 300, req.Timeout)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(mockSandboxResponse("default-sandbox")))
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := &Config{
			APIKey:       "test-key",
			BaseURL:      server.URL,
			DataPlaneURL: server.URL,
			// Template and TimeoutSec left as zero → should default
		}
		backend, err := NewBackend(context.Background(), cfg)
		require.NoError(t, err)
		assert.Equal(t, "default-sandbox", backend.SandboxID())
	})
}

// ============================== LsInfo Tests ==============================

func TestBackend_LsInfo(t *testing.T) {
	backend, server := setupTest(t)
	defer server.Close()

	t.Run("Success: ListFiles", func(t *testing.T) {
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				lsOutput := `{"Path": "file1.txt", "IsDir": false, "Size": 100, "ModifiedAt": "2025-01-15T10:30:00Z"}
{"Path": "subdir", "IsDir": true, "Size": 0, "ModifiedAt": "2025-01-14T08:00:00Z"}`
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    lsOutput,
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		files, err := backend.LsInfo(context.Background(), &filesystem.LsInfoRequest{Path: "/home/user"})
		require.NoError(t, err)
		require.Len(t, files, 2)
		assert.Equal(t, "file1.txt", files[0].Path)
		assert.False(t, files[0].IsDir)
		assert.Equal(t, int64(100), files[0].Size)
		assert.Equal(t, "2025-01-15T10:30:00Z", files[0].ModifiedAt)
		assert.Equal(t, "subdir", files[1].Path)
		assert.True(t, files[1].IsDir)
	})

	t.Run("EmptyDirectory", func(t *testing.T) {
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "",
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		files, err := backend.LsInfo(context.Background(), &filesystem.LsInfoRequest{Path: "/empty"})
		require.NoError(t, err)
		assert.Len(t, files, 0)
	})

	t.Run("Failure: ScriptError", func(t *testing.T) {
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "Permission denied",
					"stderr":    "PermissionError",
					"exit_code": float64(1),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		_, err := backend.LsInfo(context.Background(), &filesystem.LsInfoRequest{Path: "/root"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exited with code 1")
	})
}

// ============================== Read Tests ==============================

func TestBackend_Read(t *testing.T) {
	t.Run("Success: FullFile", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/download" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "line1\nline2\nline3\nline4\nline5",
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		content, err := backend.Read(context.Background(), &filesystem.ReadRequest{
			FilePath: "/home/user/test.txt",
		})
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2\nline3\nline4\nline5", content.Content)
	})

	t.Run("Success: WithOffsetAndLimit", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/download" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "line1\nline2\nline3\nline4\nline5",
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		content, err := backend.Read(context.Background(), &filesystem.ReadRequest{
			FilePath: "/home/user/test.txt",
			Offset:   2,
			Limit:    2,
		})
		require.NoError(t, err)
		assert.Equal(t, "line2\nline3", content.Content)
	})

	t.Run("Success: OffsetBeyondFile", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/download" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "line1\nline2",
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		content, err := backend.Read(context.Background(), &filesystem.ReadRequest{
			FilePath: "/home/user/test.txt",
			Offset:   10,
		})
		require.NoError(t, err)
		assert.Equal(t, "", content.Content)
	})

	t.Run("Failure: FileNotFound", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}

		_, err := backend.Read(context.Background(), &filesystem.ReadRequest{
			FilePath: "/nonexistent.txt",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read failed")
	})
}

// ============================== Write Tests ==============================

func TestBackend_Write(t *testing.T) {
	t.Run("Success: WriteFile", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		var uploadedPath, uploadedContent string
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/upload" && r.Method == http.MethodPost {
				var req e2b.UploadFileRequest
				decodeBody(t, r, &req)
				uploadedPath = req.Path
				uploadedContent = req.Content
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.UploadFileResponse{})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		err := backend.Write(context.Background(), &filesystem.WriteRequest{
			FilePath: "/home/user/new.txt",
			Content:  "hello world",
		})
		require.NoError(t, err)
		assert.Equal(t, "/home/user/new.txt", uploadedPath)
		assert.Equal(t, "hello world", uploadedContent)
	})

	t.Run("Failure: APIError", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "disk full", http.StatusInternalServerError)
		}

		err := backend.Write(context.Background(), &filesystem.WriteRequest{
			FilePath: "/home/user/test.txt",
			Content:  "data",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "write failed")
	})
}

// ============================== Edit Tests ==============================

func TestBackend_Edit(t *testing.T) {
	t.Run("Success: SingleReplacement", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		uploadCount := 0
		var finalContent string
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/filesystem/download":
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "hello old world",
				})
			case r.URL.Path == "/filesystem/upload":
				uploadCount++
				var req e2b.UploadFileRequest
				decodeBody(t, r, &req)
				finalContent = req.Content
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.UploadFileResponse{})
			default:
				http.Error(w, "unexpected", http.StatusInternalServerError)
			}
		}

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:   "/home/user/test.txt",
			OldString:  "old",
			NewString:  "new",
			ReplaceAll: false,
		})
		require.NoError(t, err)
		assert.Equal(t, 1, uploadCount)
		assert.Equal(t, "hello new world", finalContent)
	})

	t.Run("Success: ReplaceAll", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		var finalContent string
		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/filesystem/download":
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "foo bar foo baz foo",
				})
			case r.URL.Path == "/filesystem/upload":
				var req e2b.UploadFileRequest
				decodeBody(t, r, &req)
				finalContent = req.Content
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.UploadFileResponse{})
			default:
				http.Error(w, "unexpected", http.StatusInternalServerError)
			}
		}

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:   "/home/user/test.txt",
			OldString:  "foo",
			NewString:  "qux",
			ReplaceAll: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "qux bar qux baz qux", finalContent)
	})

	t.Run("Failure: OldStringNotFound", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/download" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "hello world",
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:  "/home/user/test.txt",
			OldString: "nonexistent",
			NewString: "replacement",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "string not found")
	})

	t.Run("Failure: MultipleMatchesWithoutReplaceAll", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/filesystem/download" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(e2b.DownloadFileResponse{
					Path:    "/home/user/test.txt",
					Content: "foo bar foo",
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:   "/home/user/test.txt",
			OldString:  "foo",
			NewString:  "qux",
			ReplaceAll: false,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "appears 2 times")
	})

	t.Run("Failure: IdenticalOldAndNew", func(t *testing.T) {
		backend, _ := setupTest(t)

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:  "/home/user/test.txt",
			OldString: "same",
			NewString: "same",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be different")
	})

	t.Run("Failure: EmptyOldString", func(t *testing.T) {
		backend, _ := setupTest(t)

		err := backend.Edit(context.Background(), &filesystem.EditRequest{
			FilePath:  "/home/user/test.txt",
			OldString: "",
			NewString: "something",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "old string is required")
	})
}

// ============================== GrepRaw Tests ==============================

func TestBackend_GrepRaw(t *testing.T) {
	t.Run("Success: PatternFound", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				grepOutput := `[{"Path": "/home/user/test.txt", "Line": 1, "Content": "hello world"}]`
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    grepOutput,
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		matches, err := backend.GrepRaw(context.Background(), &filesystem.GrepRequest{
			Pattern: "hello",
			Path:    "/home/user",
		})
		require.NoError(t, err)
		require.Len(t, matches, 1)
		assert.Equal(t, "/home/user/test.txt", matches[0].Path)
		assert.Equal(t, 1, matches[0].Line)
		assert.Equal(t, "hello world", matches[0].Content)
	})

	t.Run("Success: NoMatches", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "[]",
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		matches, err := backend.GrepRaw(context.Background(), &filesystem.GrepRequest{
			Pattern: "nonexistent",
			Path:    "/home/user",
		})
		require.NoError(t, err)
		assert.Len(t, matches, 0)
	})

	t.Run("Failure: EmptyPattern", func(t *testing.T) {
		backend, _ := setupTest(t)

		_, err := backend.GrepRaw(context.Background(), &filesystem.GrepRequest{
			Pattern: "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pattern is required")
	})
}

// ============================== GlobInfo Tests ==============================

func TestBackend_GlobInfo(t *testing.T) {
	t.Run("Success: GlobMatch", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				globOutput := `[{"Path": "file.go", "IsDir": false, "Size": 2048, "ModifiedAt": ""}, {"Path": "main.go", "IsDir": false, "Size": 1024, "ModifiedAt": ""}]`
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    globOutput,
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		files, err := backend.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{
			Pattern: "*.go",
			Path:    "/home/user",
		})
		require.NoError(t, err)
		require.Len(t, files, 2)
		assert.Equal(t, "file.go", files[0].Path)
		assert.Equal(t, "main.go", files[1].Path)
	})
}

// ============================== Execute Tests ==============================

func TestBackend_Execute(t *testing.T) {
	t.Run("Success: ExecuteCommand", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "hello from sandbox",
					"stderr":    "",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		resp, err := backend.Execute(context.Background(), &filesystem.ExecuteRequest{
			Command: "echo hello",
		})
		require.NoError(t, err)
		assert.Equal(t, "hello from sandbox", resp.Output)
		assert.Equal(t, 0, *resp.ExitCode)
	})

	t.Run("Success: CommandWithStderr", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "",
					"stderr":    "error message",
					"exit_code": float64(1),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		resp, err := backend.Execute(context.Background(), &filesystem.ExecuteRequest{
			Command: "invalid-cmd",
		})
		require.NoError(t, err)
		assert.Contains(t, resp.Output, "error message")
		assert.Equal(t, 1, *resp.ExitCode)
	})

	t.Run("Failure: EmptyCommand", func(t *testing.T) {
		backend, _ := setupTest(t)

		_, err := backend.Execute(context.Background(), &filesystem.ExecuteRequest{
			Command: "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "command is required")
	})

	t.Run("Success: StdoutAndStderr", func(t *testing.T) {
		backend, server := setupTest(t)
		defer server.Close()

		mockHandler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/process/start" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"stdout":    "output line",
					"stderr":    "warning message",
					"exit_code": float64(0),
				})
				return
			}
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}

		resp, err := backend.Execute(context.Background(), &filesystem.ExecuteRequest{
			Command: "echo test",
		})
		require.NoError(t, err)
		assert.Contains(t, resp.Output, "output line")
		assert.Contains(t, resp.Output, "warning message")
	})
}

// ============================== Close Tests ==============================

func TestBackend_Close(t *testing.T) {
	t.Run("AutoCreatedSandbox: Killed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/sandboxes" && r.Method == http.MethodPost:
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(mockSandboxResponse("auto-sandbox")))
			case strings.Contains(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "unexpected", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		cfg := &Config{
			APIKey:       "test-key",
			BaseURL:      server.URL,
			DataPlaneURL: server.URL,
			Template:     "base",
		}
		backend, err := NewBackend(context.Background(), cfg)
		require.NoError(t, err)
		assert.True(t, backend.autoCreated)

		err = backend.Close(context.Background())
		require.NoError(t, err)
	})

	t.Run("PreExistingSandbox: NotKilled", func(t *testing.T) {
		backend, err := NewBackend(context.Background(), &Config{
			APIKey:    "test-key",
			SandboxID: "existing-sandbox",
		})
		require.NoError(t, err)
		assert.False(t, backend.autoCreated)

		// Close should not fail (no-op)
		err = backend.Close(context.Background())
		require.NoError(t, err)
	})
}

// ============================== Edge Case Tests ==============================

func TestApplyLineOffsetLimit(t *testing.T) {
	tests := []struct {
		name    string
		content string
		offset  int
		limit   int
		want    string
	}{
		{"full read", "a\nb\nc", 1, 10, "a\nb\nc"},
		{"offset middle", "a\nb\nc\nd", 2, 2, "b\nc"},
		{"offset at end", "a\nb", 3, 1, ""},
		{"empty content", "", 1, 10, ""},
		{"single line", "only", 1, 1, "only"},
		{"limit truncated", "a\nb\nc\nd\ne", 1, 3, "a\nb\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyLineOffsetLimit(tt.content, tt.offset, tt.limit)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ============================== Interface Checks ==============================

func TestInterfaceCompliance(t *testing.T) {
	var backend filesystem.Backend = (*Backend)(nil)
	_ = backend // unused but verifies interface compliance at compile time

	var shell filesystem.Shell = (*Backend)(nil)
	_ = shell
}
