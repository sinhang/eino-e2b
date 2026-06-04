// Package e2b implements the filesystem.Backend interface backed by E2B sandboxes.
//
// It uses github.com/sinhang/e2b-go-sdk for sandbox management, file operations,
// and command execution inside E2B's isolated cloud sandboxes.
package e2b

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/cloudwego/eino/adk/filesystem"
	e2b "github.com/sinhang/e2b-go-sdk/e2b"
)

// Config holds the configuration for the E2B filesystem backend.
type Config struct {
	// APIKey is the E2B API key. Falls back to E2B_API_KEY env var if empty.
	APIKey string

	// BaseURL is the E2B API base URL. Default: http://127.0.0.1:13000 (Cube compat).
	BaseURL string

	// Domain is the sandbox domain for data-plane hostname construction.
	// Default: "cube.app".
	Domain string

	// DataPlaneURL is the data-plane proxy endpoint (CubeProxy).
	// In dev mode this is typically "https://127.0.0.1:11443".
	DataPlaneURL string

	// SandboxID is an optional pre-existing sandbox ID. If empty, a new sandbox
	// is created on NewBackend using Template.
	SandboxID string

	// Template is the template ID for auto-creating a sandbox. Default: "base".
	Template string

	// TimeoutSec is the sandbox idle timeout in seconds. Default: 300 (5 min).
	TimeoutSec int

	// Envs are environment variables injected into the sandbox at creation time.
	// Only used when auto-creating a sandbox (SandboxID is empty).
	Envs map[string]string

	// VolumeMounts are volumes attached to the sandbox at creation time.
	// Each entry should contain "name" and "path" fields:
	//
	//   VolumeMounts: []map[string]any{
	//       {"name": "my-volume", "path": "/mnt/data"},
	//   }
	//
	// Volumes must be created first via the E2B/Cube volume API, then mounted
	// here by name. The mount path becomes accessible inside the sandbox for all
	// filesystem operations.
	//
	// Only used when auto-creating a sandbox (SandboxID is empty).
	VolumeMounts []map[string]any

	// HTTPClient allows injecting a custom HTTP client (useful for testing).
	HTTPClient *http.Client
}

// Backend implements filesystem.Backend backed by an E2B sandbox.
//
// Command execution goes through the Python code interpreter (port 49999),
// NOT through envd (port 49983), because Cube deployments typically only
// expose the code interpreter port in their templates.
type Backend struct {
	client      *e2b.Client
	sandboxID   string
	autoCreated bool                 // true if we created the sandbox and should clean up on Close
	interpreter *e2b.CodeInterpreter // cached Python code interpreter (port 49999)
}

// NewBackend creates a new E2B filesystem backend.
//
// If Config.SandboxID is provided, it connects to the existing sandbox.
// Otherwise, it creates a new sandbox using Config.Template (default: "base").
// Auto-created sandboxes are destroyed on Close.
func NewBackend(ctx context.Context, cfg *Config) (*Backend, error) {
	if cfg == nil {
		return nil, fmt.Errorf("e2b: config is required")
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("E2B_API_KEY")
	}

	template := cfg.Template
	if template == "" {
		template = "base"
	}

	timeoutSec := cfg.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 300
	}

	// Build e2b.Client options
	var opts []e2b.ClientOption
	opts = append(opts, e2b.WithAPIKey(apiKey))
	opts = append(opts, e2b.WithCompatMode(true))

	if cfg.BaseURL != "" {
		opts = append(opts, e2b.WithBaseURL(cfg.BaseURL))
	}
	if cfg.Domain != "" {
		opts = append(opts, e2b.WithSandboxDomain(cfg.Domain))
	}
	if cfg.DataPlaneURL != "" {
		opts = append(opts, e2b.WithDataPlaneURL(cfg.DataPlaneURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, e2b.WithHTTPClient(cfg.HTTPClient))
	}

	client := e2b.NewClient(opts...)

	var sandboxID string
	autoCreated := false

	if cfg.SandboxID != "" {
		// Connect to existing sandbox
		sandboxID = cfg.SandboxID
	} else {
		// Create new sandbox
		createReq := e2b.CreateSandboxRequest{
			TemplateID: template,
			Timeout:    timeoutSec,
		}

		// Pass environment variables if provided.
		if len(cfg.Envs) > 0 {
			envMap := make(e2b.JSONMap, len(cfg.Envs))
			for k, v := range cfg.Envs {
				envMap[k] = v
			}
			createReq.EnvVars = envMap
		}

		// Pass volume mounts if provided.
		if len(cfg.VolumeMounts) > 0 {
			createReq.VolumeMounts = make([]e2b.JSONMap, 0, len(cfg.VolumeMounts))
			for _, vm := range cfg.VolumeMounts {
				createReq.VolumeMounts = append(createReq.VolumeMounts, e2b.JSONMap(vm))
			}
		}

		sandbox, err := client.CreateSandbox(ctx, createReq)
		if err != nil {
			return nil, fmt.Errorf("e2b: failed to create sandbox: %w", err)
		}
		sandboxID = sandbox.SandboxID
		if sandboxID == "" {
			return nil, fmt.Errorf("e2b: sandbox created but SandboxID is empty")
		}
		autoCreated = true
	}

	return &Backend{
		client:      client,
		sandboxID:   sandboxID,
		autoCreated: autoCreated,
		interpreter: e2b.NewCodeInterpreter(client, sandboxID),
	}, nil
}

// Close shuts down the backend. If the sandbox was auto-created, it is destroyed.
func (b *Backend) Close(ctx context.Context) error {
	if b.autoCreated {
		return b.client.DeleteSandbox(ctx, b.sandboxID)
	}
	return nil
}

// SandboxID returns the underlying E2B sandbox identifier.
func (b *Backend) SandboxID() string {
	return b.sandboxID
}

// Client returns the underlying e2b-go-sdk Client, giving direct access to
// sandbox lifecycle, volume management, and other low-level APIs not covered
// by the filesystem.Backend interface.
//
// Common use cases:
//
//	// Create a volume
//	backend.Client().PostVolumes(ctx, e2b.JSONMap{"name": "my-vol"})
//
//	// List all sandboxes
//	sandboxes, _ := backend.Client().ListSandboxes(ctx, "")
func (b *Backend) Client() *e2b.Client {
	return b.client
}

// Compile-time interface checks.
var (
	_ filesystem.Backend = (*Backend)(nil)
)
