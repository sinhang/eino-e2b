// Example: Direct E2B backend usage for filesystem operations.
//
// This demonstrates how to use the e2b backend directly for file operations
// inside an E2B sandbox, without the middleware/agent layer.
//
// Set environment variables:
//
//	E2B_API_KEY  - Your E2B/Cube API key
//
// Optional:
//
//	E2B_CUSTOM_BASE_URL  - Custom API base URL (default: http://127.0.0.1:13000)
//	E2B_SANDBOX_ID       - Use an existing sandbox instead of creating a new one
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	e2bbackend "eino-e2b"

	"github.com/cloudwego/eino/adk/filesystem"
)

func main() {
	ctx := context.Background()

	// Step 1: Load configuration from environment
	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		fmt.Println("Warning: E2B_API_KEY not set. The backend may fail to authenticate.")
		fmt.Println("Set E2B_API_KEY to your E2B/Cube API key.")
	}

	baseURL := os.Getenv("E2B_CUSTOM_BASE_URL")
	sandboxID := os.Getenv("E2B_SANDBOX_ID")
	template := "base"

	config := &e2bbackend.Config{
		APIKey:     apiKey,
		BaseURL:    baseURL,
		SandboxID:  sandboxID,
		Template:   template,
		TimeoutSec: 300,
	}

	backend, err := e2bbackend.NewBackend(ctx, config)
	if err != nil {
		log.Fatalf("Failed to create E2B backend: %v", err)
	}
	defer func() {
		if err := backend.Close(ctx); err != nil {
			log.Printf("Warning: failed to close sandbox: %v", err)
		}
	}()

	fmt.Printf("✓ E2B backend initialized (sandbox: %s)\n", backend.SandboxID())
	fmt.Println()

	// Example 1: Write a file
	fmt.Println("Example 1: Write a file")
	fmt.Println("----------------------")
	testPath := "/home/user/hello.txt"
	err = backend.Write(ctx, &filesystem.WriteRequest{
		FilePath: testPath,
		Content:  "Hello from E2B Sandbox!\nThis file was created by the e2b backend.\n",
	})
	if err != nil {
		log.Printf("⚠ Write failed: %v\n", err)
	} else {
		fmt.Println("✓ File written successfully")
	}
	fmt.Println()

	// Example 2: Read a file
	fmt.Println("Example 2: Read a file")
	fmt.Println("----------------------")
	content, err := backend.Read(ctx, &filesystem.ReadRequest{
		FilePath: testPath,
	})
	if err != nil {
		log.Printf("⚠ Read failed: %v\n", err)
	} else {
		fmt.Println("File content:")
		fmt.Println("─────────────────────────")
		fmt.Print(content.Content)
		fmt.Println("─────────────────────────")
	}
	fmt.Println()

	// Example 3: List directory contents
	fmt.Println("Example 3: List directory contents")
	fmt.Println("-----------------------------------")
	fmt.Println("Listing: /home/user")
	files, err := backend.LsInfo(ctx, &filesystem.LsInfoRequest{
		Path: "/home/user",
	})
	if err != nil {
		log.Printf("⚠ LsInfo failed: %v\n", err)
	} else if len(files) == 0 {
		fmt.Println("(empty directory)")
	} else {
		fmt.Printf("Found %d item(s):\n", len(files))
		for i, f := range files {
			dirMarker := ""
			if f.IsDir {
				dirMarker = "/"
			}
			fmt.Printf("  %d. %s%s (%d bytes)\n", i+1, f.Path, dirMarker, f.Size)
		}
	}
	fmt.Println()

	// Example 4: Search file content (Grep)
	fmt.Println("Example 4: Search file content (Grep)")
	fmt.Println("--------------------------------------")
	matches, err := backend.GrepRaw(ctx, &filesystem.GrepRequest{
		Path:    "/home/user",
		Pattern: "Sandbox",
		Glob:    "*.txt",
	})
	if err != nil {
		log.Printf("⚠ GrepRaw failed: %v\n", err)
	} else if len(matches) == 0 {
		fmt.Println("No matches found")
	} else {
		fmt.Printf("✓ Found %d match(es):\n", len(matches))
		for _, match := range matches {
			fmt.Printf("  • %s:%d - %s\n", match.Path, match.Line, match.Content)
		}
	}
	fmt.Println()

	// Example 5: Find files by pattern (Glob)
	fmt.Println("Example 5: Find files by pattern (Glob)")
	fmt.Println("----------------------------------------")
	globFiles, err := backend.GlobInfo(ctx, &filesystem.GlobInfoRequest{
		Path:    "/home/user",
		Pattern: "*.txt",
	})
	if err != nil {
		log.Printf("⚠ GlobInfo failed: %v\n", err)
	} else if len(globFiles) == 0 {
		fmt.Println("No matching files found")
	} else {
		fmt.Printf("✓ Found %d file(s):\n", len(globFiles))
		for i, f := range globFiles {
			fmt.Printf("  %d. %s\n", i+1, f.Path)
		}
	}
	fmt.Println()

	// Example 6: Edit a file
	fmt.Println("Example 6: Edit a file (string replacement)")
	fmt.Println("---------------------------------------------")
	err = backend.Edit(ctx, &filesystem.EditRequest{
		FilePath:  testPath,
		OldString: "E2B Sandbox",
		NewString: "Awesome E2B Sandbox",
	})
	if err != nil {
		log.Printf("⚠ Edit failed: %v\n", err)
	} else {
		fmt.Println("✓ File edited successfully")
		// Read again to verify
		content, _ = backend.Read(ctx, &filesystem.ReadRequest{FilePath: testPath})
		fmt.Println("New content:")
		fmt.Println("─────────────────────────")
		fmt.Print(content.Content)
		fmt.Println("─────────────────────────")
	}
	fmt.Println()

	// Example 7: Execute a shell command
	fmt.Println("Example 7: Execute a shell command")
	fmt.Println("-----------------------------------")
	result, err := backend.Execute(ctx, &filesystem.ExecuteRequest{
		Command: "echo Current time: $(date) && uname -a",
	})
	if err != nil {
		log.Printf("⚠ Execute failed: %v\n", err)
	} else {
		fmt.Println("Command output:")
		fmt.Println("─────────────────────────")
		fmt.Println(result.Output)
		if result.ExitCode != nil && *result.ExitCode != 0 {
			fmt.Printf("(exit code: %d)\n", *result.ExitCode)
		}
		fmt.Println("─────────────────────────")
	}
	fmt.Println()

	fmt.Println("========================================")
	fmt.Println("✓ All examples completed successfully!")
	fmt.Println("========================================")

	// Print a helpful note for first-time users
	if os.Getenv("E2B_API_KEY") == "" {
		fmt.Println()
		fmt.Println("Note: To run against a real sandbox, set E2B_API_KEY and start the")
		fmt.Println("API server. Without credentials this example will fail on sandbox creation.")
	}
}
