# eino-e2b

E2B backend for the [EINO](https://github.com/cloudwego/eino) ADK framework. Implements `filesystem.Backend` backed by E2B / Cube sandboxes — all file operations execute securely inside isolated cloud sandboxes.

Built on top of [e2b-go-sdk](https://github.com/sinhang/e2b-go-sdk) for sandbox lifecycle management and data-plane communication.

## Installation

```bash
go get github.com/sinhang/eino-e2b
```

Requires Go 1.22+.

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "os"

    e2b "eino-e2b"
    "github.com/cloudwego/eino/adk/filesystem"
)

func main() {
    ctx := context.Background()

    backend, err := e2b.NewBackend(ctx, &e2b.Config{
        APIKey:   os.Getenv("E2B_API_KEY"),
        Template: "base",
    })
    if err != nil {
        panic(err)
    }
    defer backend.Close(ctx)

    // Write a file
    backend.Write(ctx, &filesystem.WriteRequest{
        FilePath: "/home/user/hello.txt",
        Content:  "Hello from E2B!",
    })

    // Read it back
    content, _ := backend.Read(ctx, &filesystem.ReadRequest{
        FilePath: "/home/user/hello.txt",
    })
    fmt.Print(content.Content)
}
```

## Configuration

| Field | Type | Default | Description |
|---|---|---|---|
| `APIKey` | `string` | `$E2B_API_KEY` | E2B / Cube API key |
| `BaseURL` | `string` | `http://127.0.0.1:13000` | Control-plane API base URL |
| `Domain` | `string` | `cube.app` | Sandbox domain for data-plane routing |
| `DataPlaneURL` | `string` | — | Data-plane proxy endpoint (dev mode) |
| `SandboxID` | `string` | — | Pre-existing sandbox ID; if empty, creates a new one |
| `Template` | `string` | `base` | Template ID for auto-creating sandboxes |
| `TimeoutSec` | `int` | `300` | Sandbox idle timeout in seconds |
| `Envs` | `map[string]string` | — | Environment variables injected into the sandbox |
| `VolumeMounts` | `[]map[string]any` | — | Volumes to attach at sandbox creation (see below) |
| `HTTPClient` | `*http.Client` | — | Custom HTTP client (useful for testing) |

### Sandbox Lifecycle

- **`SandboxID` is empty** → a new sandbox is created on `NewBackend` and destroyed on `Close`.
- **`SandboxID` is set** → connects to an existing sandbox; `Close` is a no-op.

### Working Directory & Volume Mounts

The default user home directory inside the sandbox is **`/home/user`**. All `filesystem.Backend` methods take explicit paths, so you can operate on any directory by passing the full path.

To mount external volumes into the sandbox, use `VolumeMounts`. Each entry takes `"name"` (an existing volume created via the E2B/Cube API) and `"path"` (the mount point inside the sandbox):

```go
backend, err := e2b.NewBackend(ctx, &e2b.Config{
    APIKey:   os.Getenv("E2B_API_KEY"),
    Template: "base",
    Envs: map[string]string{
        "MY_VAR": "hello",
    },
    VolumeMounts: []map[string]any{
        {"name": "my-data-volume", "path": "/mnt/data"},
        {"name": "shared-storage", "path": "/mnt/shared"},
    },
})
defer backend.Close(ctx)

// Operate on mounted volumes directly
files, _ := backend.LsInfo(ctx, &filesystem.LsInfoRequest{Path: "/mnt/data"})

backend.Write(ctx, &filesystem.WriteRequest{
    FilePath: "/mnt/shared/output.txt",
    Content:  "data persisted to volume",
})
```

**Volume lifecycle**: volumes are created separately via the E2B/Cube API and persist across sandbox restarts. Use the `e2b-go-sdk` volume methods directly if you need to manage volumes:

```go
client := e2b.NewClient(e2b.WithAPIKey("..."))
client.PostVolumes(ctx, e2b.JSONMap{"name": "my-data-volume", "size": "10Gi"})
```

## Implemented Interfaces

### `filesystem.Backend`

| Method | Description | Implementation |
|---|---|---|
| `LsInfo` | List directory contents | `os.scandir` via Python script in sandbox |
| `Read` | Read file with offset/limit | `DownloadFile` API + client-side line slicing |
| `Write` | Create or overwrite file | `UploadFile` API (auto-creates parent dirs) |
| `Edit` | String replacement in file | Read → client-side replace → Write |
| `GrepRaw` | Pattern search across files | `rg --json` in sandbox (falls back to `grep`) |
| `GlobInfo` | Find files by glob pattern | `glob.glob` via Python script in sandbox |

### `filesystem.Shell`

| Method | Description |
|---|---|
| `Execute` | Run shell command in sandbox via `/bin/sh -c` |

## Integration with EINO Agents

Use the filesystem middleware to give an AI agent sandboxed file operations:

```go
import (
    e2b "eino-e2b"
    "github.com/cloudwego/eino/adk"
    "github.com/cloudwego/eino/adk/middlewares/filesystem"
)

backend, _ := e2b.NewBackend(ctx, &e2b.Config{
    APIKey:   os.Getenv("E2B_API_KEY"),
    Template: "base",
})
defer backend.Close(ctx)

fsMiddleware, _ := filesystem.NewMiddleware(ctx, &filesystem.Config{
    Backend: backend,
})

agent, _ := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:        "FileSystemAgent",
    Description: "Agent with sandboxed filesystem access",
    Model:       chatModel,
    Middlewares:  []adk.AgentMiddleware{fsMiddleware},
})
```

The middleware automatically generates these tools for the agent:

| Tool | Backend Method |
|---|---|
| `ls` | `LsInfo` |
| `read_file` | `Read` |
| `write_file` | `Write` |
| `edit_file` | `Edit` |
| `glob` | `GlobInfo` |
| `grep` | `GrepRaw` |
| `execute` | `Execute` |

## Architecture

```
EINO ADK Agent / Middleware
        │
        ▼
  filesystem.Backend interface
        │
        ▼
  e2b.Backend ── uses ──▶ e2b-go-sdk.Client
                              │
                   ┌──────────┼──────────┐
                   ▼          ▼          ▼
             Sandbox Mgmt  Filesystem  Commands
             (REST API)    (REST API)  (Data Plane)
```

- **Control plane** (`BaseURL`): sandbox create/delete, file upload/download
- **Data plane** (`DataPlaneURL` or `https://49983-{sandboxID}.{domain}`): command execution, Python scripts

## Running the Examples

```bash
# Direct backend usage
cd examples/backend
E2B_API_KEY=your-key go run main.go

# Agent + middleware integration
cd examples/middlewares
E2B_API_KEY=your-key go run main.go
```

## License

Apache License 2.0

### tag
```shell
git push -u origin main
git tag v0.0.1
git push origin v0.0.1
```