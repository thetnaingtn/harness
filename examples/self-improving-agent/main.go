// Self-improving agent example — fourth shipped example, demonstrating
// the v0.2.0 self-improvement primitives wired together end-to-end:
//
//   - tool/memory: durable cross-session preferences/facts
//   - tool/skills: durable procedural knowledge
//   - runtime.Review: end-of-turn reviewer that decides what to save
//
// Storage lives at ~/.harness/self-improving-agent/{memory.jsonl,skills/}.
// The agent has file (read/write/edit) and bash tools for foreground work;
// the reviewer has WRITE-ONLY memory + skills tools so it can curate but
// not modify the workspace.
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	go run ./examples/self-improving-agent
//
// Type prompts at the > marker. After each turn, an "💾 self-review:"
// line summarizes anything the reviewer saved.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sausheong/harness/providers/anthropic"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	memorytool "github.com/sausheong/harness/tool/memory"
	memoryjsonl "github.com/sausheong/harness/tool/memory/jsonl"
	skilltool "github.com/sausheong/harness/tool/skills"
	skilldisk "github.com/sausheong/harness/tool/skills/disk"
	"github.com/sausheong/harness/tools/bash"
	"github.com/sausheong/harness/tools/file"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY must be set")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}
	storeRoot := filepath.Join(home, ".harness", "self-improving-agent")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		log.Fatalf("mkdir store: %v", err)
	}

	memStore := memoryjsonl.NewStore(filepath.Join(storeRoot, "memory.jsonl"))
	skillStore := skilldisk.NewStore(filepath.Join(storeRoot, "skills"))

	workspace := storeRoot

	// Foreground tool registry: read tools + memory/skills (so the
	// agent CAN save during the user turn if it wants, in addition to
	// the reviewer).
	foreground := tool.NewRegistry()
	foreground.Register(&file.ReadFileTool{WorkDir: workspace})
	foreground.Register(&file.WriteFileTool{WorkDir: workspace})
	foreground.Register(&file.EditFileTool{WorkDir: workspace})
	foreground.Register(&bash.BashTool{WorkDir: workspace})
	foreground.Register(&memorytool.MemoryTool{Store: memStore})
	foreground.Register(&skilltool.SkillTool{Store: skillStore})

	// Reviewer registry: WRITE-ONLY memory + skills. No file/bash —
	// the reviewer's job is curation, not workspace mutation.
	reviewTools := tool.NewRegistry()
	reviewTools.Register(&memorytool.MemoryTool{Store: memStore})
	reviewTools.Register(&skilltool.SkillTool{Store: skillStore})

	provider := anthropic.NewAnthropicProvider(apiKey, "")

	// Declare rt early so the OnStop closure can capture it by
	// reference. The hook only fires AFTER rt is assigned, so by the
	// time it runs, the closure sees the constructed Runtime.
	var rt *runtime.Runtime

	spec := runtime.AgentSpec{
		ID:           "self-improving",
		Name:         "Self-Improving Agent",
		Model:        "anthropic/claude-haiku-4-5-20251001",
		Workspace:    workspace,
		SystemPrompt: "You are a helpful assistant. Save important user preferences and reusable workflows for future sessions.",
		MaxTurns:     25,
	}

	// OnStop: at end of every successful turn, fire a Review against
	// the parent session in a goroutine. Surfaces a one-line
	// "💾 self-review:" summary if the reviewer saved anything.
	spec.Loop.Hooks.OnStop = func(ctx context.Context, reason string) {
		if reason != "completed" {
			return
		}
		// Capture rt by closure so we can pass it to Review.
		go func() {
			res := runtime.Review(context.Background(), rt, runtime.ReviewSpec{
				Prompt: runtime.ReviewPromptDefault,
				Tools:  reviewTools,
			})
			if res.Err != nil {
				fmt.Printf("⚠️  self-review error: %v\n", res.Err)
				return
			}
			if len(res.Actions) > 0 {
				fmt.Printf("💾 self-review: %s\n", strings.Join(res.Actions, " · "))
			}
		}()
	}

	rt, err = runtime.BuildRuntime(
		runtime.RuntimeDeps{
			Memory: memStore.AsMemoryProvider(),
			Skills: skillStore.AsSkillProvider(),
		},
		runtime.RuntimeInputs{
			Provider: provider,
			Tools:    foreground,
			Session:  session.NewSession("self-improving", "main"),
		},
		spec,
	)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	// REPL.
	fmt.Println("Self-improving agent ready. Type a prompt and press Enter (Ctrl+D to exit).")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		events, err := rt.Run(context.Background(), prompt, nil)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}
		for ev := range events {
			switch ev.Type {
			case runtime.EventTextDelta:
				fmt.Print(ev.Text)
			case runtime.EventDone:
				fmt.Println()
			case runtime.EventError:
				fmt.Printf("\nerror: %v\n", ev.Error)
			}
		}
	}
}
