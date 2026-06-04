// Example: E2B backend with filesystem middleware and a chat agent.
//
// This demonstrates integrating the E2B backend into an EINO ADK agent via
// the filesystem middleware, giving the agent the ability to perform file
// operations inside a secure E2B sandbox.
//
// Set environment variables:
//
//	E2B_API_KEY  - Your E2B/Cube API key
//
// Optional:
//
//	E2B_CUSTOM_BASE_URL  - Custom API base URL
//	E2B_SANDBOX_ID       - Use an existing sandbox
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	e2bbackend "github.com/sinhang/eino-e2b"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func main() {
	ctx := context.Background()

	fmt.Println("========================================")
	fmt.Println("E2B Sandbox Middleware Example")
	fmt.Println("========================================")
	fmt.Println()

	// Step 1: Load configuration
	fmt.Println("Step 1: Loading configuration...")

	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		log.Fatal("Error: E2B_API_KEY environment variable is required.\n" +
			"Please set it to your E2B/Cube API key.")
	}

	baseURL := os.Getenv("E2B_CUSTOM_BASE_URL")

	config := &e2bbackend.Config{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		Template:   "base",
		TimeoutSec: 300,
		// SandboxID 留空，NewBackend 自动创建 sandbox，Close 时自动销毁
		// 如需复用已有 sandbox: SandboxID: os.Getenv("E2B_SANDBOX_ID"),
	}

	fmt.Println("✓ Configuration loaded")
	fmt.Println()

	// Step 2: Initialize E2B Backend
	fmt.Println("Step 2: Initializing E2B sandbox backend...")

	backend, err := e2bbackend.NewBackend(ctx, config)
	if err != nil {
		log.Fatalf("Failed to create E2B backend: %v", err)
	}
	defer func() {
		if err := backend.Close(ctx); err != nil {
			log.Printf("Warning: sandbox close failed: %v", err)
		}
	}()

	fmt.Printf("✓ Backend initialized (sandbox: %s)\n", backend.SandboxID())
	fmt.Println()

	// Step 3: Initialize Filesystem Middleware
	fmt.Println("Step 3: Initializing filesystem middleware...")

	fsMiddleware, err := filesystem.NewMiddleware(ctx, &filesystem.Config{
		Backend: backend,
	})
	if err != nil {
		log.Fatalf("Failed to create filesystem middleware: %v", err)
	}

	fmt.Println("✓ Middleware initialized")
	fmt.Println()

	// Step 4: Initialize Chat Model
	fmt.Println("Step 4: Initializing chat model...")

	chatModel, err := newChatModel(ctx)
	if err != nil {
		log.Fatalf("Failed to create chat model: %v", err)
	}
	if chatModel == nil {
		fmt.Println("⚠ Chat model is nil — using echo mode for demonstration.")
		fmt.Println("  In production, replace newChatModel() with a real model (OpenAI, Claude, Doubao, etc.)")
		fmt.Println("  The middleware is still properly configured, but the agent won't produce real responses.")
		fmt.Println()
		// In demo mode, we still show the middleware setup is correct by
		// printing what would happen. The user would see real agent behavior
		// with an actual chat model implementation.
		printSetupSummary(backend.SandboxID())
		return
	}

	fmt.Println("✓ Chat model initialized")
	fmt.Println()

	// Step 5: Create Agent with Middleware
	fmt.Println("Step 5: Creating filesystem agent...")

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "E2BFileSystemAgent",
		Description: "An AI agent that performs filesystem operations in a secure E2B sandbox",
		Model:       chatModel,
		Middlewares: []adk.AgentMiddleware{fsMiddleware},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	fmt.Println("✓ Agent created")
	fmt.Println()

	// Step 6: Run Agent with User Query
	fmt.Println("Step 6: Running agent...")
	fmt.Println()

	query := "List all files in the /home/user directory."
	fmt.Printf("User: %s\n", query)
	fmt.Println()
	fmt.Println("Agent is processing...")
	fmt.Println("─────────────────────────")

	iterator := agent.Run(ctx, &adk.AgentInput{
		Messages: []*schema.Message{
			schema.UserMessage(query),
		},
	})

	eventCount := 0
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		eventCount++
		fmt.Printf("\n[Event %d]\n", eventCount)
		fmt.Printf("Type: %T\n", event)
		// Print event details without overwhelming the output
		s := fmt.Sprintf("%+v", event)
		if len(s) > 500 {
			s = s[:500] + "..."
		}
		fmt.Printf("%s\n", s)
	}
	fmt.Println("─────────────────────────")
	fmt.Printf("✓ Agent finished (%d events)\n", eventCount)
	fmt.Println()

	fmt.Println("========================================")
	fmt.Println("✓ Example completed!")
	fmt.Println("========================================")
}

// newChatModel should be replaced with a real chat model implementation.
// Examples:
//
//	import openai "github.com/cloudwego/eino-ext/components/model/openai"
//	return openai.NewChatModel(ctx, &openai.ChatModelConfig{...})
//
//	import claude "github.com/cloudwego/eino-ext/components/model/claude"
//	return claude.NewChatModel(ctx, &claude.ChatModelConfig{...})
func newChatModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	// Placeholder — return nil to indicate demo mode.
	// Replace this with a real chat model for production use.
	return nil, nil
}

func printSetupSummary(sandboxID string) {
	var sb strings.Builder
	sb.WriteString("Setup Summary:\n")
	sb.WriteString("──────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("  Sandbox ID   : %s\n", sandboxID))
	sb.WriteString("  Backend      : e2b (filesystem.Backend)\n")
	sb.WriteString("  Middleware   : filesystem.NewMiddleware\n")
	sb.WriteString("  Shell support: yes (filesystem.Shell)\n")
	sb.WriteString("\n")
	sb.WriteString("Available tools (auto-generated by middleware):\n")
	sb.WriteString("  • ls         - List directory contents\n")
	sb.WriteString("  • read_file  - Read file content\n")
	sb.WriteString("  • write_file - Create or overwrite a file\n")
	sb.WriteString("  • edit_file  - Find and replace in a file\n")
	sb.WriteString("  • glob       - Find files by pattern\n")
	sb.WriteString("  • grep       - Search file content\n")
	sb.WriteString("  • execute    - Run shell commands\n")
	sb.WriteString("\n")
	sb.WriteString("To see real agent behavior, replace newChatModel()\n")
	sb.WriteString("with a real chat model implementation.\n")
	fmt.Print(sb.String())
}
