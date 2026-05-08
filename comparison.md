# How Harness compares with other agent frameworks

This document compares Harness's architectural approach against the
major agent frameworks and runtimes currently shaping the ecosystem:
LangGraph, CrewAI, AutoGen/AG2, Semantic Kernel, OpenAI Agents SDK,
Claude Agent SDK, LlamaIndex agents, Mastra, and related orchestration
systems.

## The short version

Harness occupies a somewhat unusual position in the ecosystem. It is
not primarily a "workflow DSL framework" like LangGraph, nor a
"role-play orchestration framework" like CrewAI, nor a
"conversation-as-computation framework" like AutoGen. It is closer to
a lightweight, programmable agent runtime and harness layer written
in Go, with strong emphasis on composability, execution control,
portability, and developer ownership of the orchestration substrate
itself.

That distinction matters.

Most popular agent frameworks today are Python-first and increasingly
converge toward graph-based orchestration abstractions. Harness
instead feels closer philosophically to:

- an application runtime,
- a systems programming toolkit,
- and an execution harness for agent loops.

This makes it more comparable to the underlying architectural layer
beneath systems like Claude Code or OpenClaw than to higher-level
"business workflow" agent frameworks.

## The landscape today

| Framework / Platform | Core abstraction | Primary language | Design center |
|---|---|---|---|
| LangGraph | Stateful graph / workflow | Python | Explicit orchestration and durable execution |
| CrewAI | Teams and roles | Python | Multi-agent collaboration |
| AutoGen / AG2 | Conversational agents | Python | Agent-to-agent dialogue |
| Semantic Kernel | Plugins + planners | C# / Python | Enterprise integration |
| OpenAI Agents SDK | Thin agent runtime | Python / TS | Simplicity around the OpenAI ecosystem |
| LlamaIndex Agents | Retrieval-centric agents | Python | RAG and knowledge workflows |
| Claude Agent SDK | Tool-using coding / runtime agents | TS / Python | Anthropic-native agent execution |
| **Harness** | **Agent execution harness / runtime** | **Go** | **Systems-level control and ownership** |

### Current GitHub stats (as of 2026-05-08)

| Project | Stars | Last push | Description |
|---|---:|---|---|
| `microsoft/autogen` | 57.8k | 2026-04-15 | Programming framework for agentic AI |
| `crewAIInc/crewAI` | 50.9k | 2026-05-07 | Role-playing autonomous AI agents |
| `run-llama/llama_index` | 49.2k | 2026-05-07 | Document agent + OCR platform |
| `langchain-ai/langgraph` | 31.5k | 2026-05-08 | Resilient language agents as graphs |
| `openai/openai-agents-python` | 26.0k | 2026-05-08 | Lightweight multi-agent workflows |
| `mastra-ai/mastra` | 23.7k | 2026-05-08 | TS framework for AI-powered apps |
| `letta-ai/letta` | 22.5k | 2026-04-12 | Stateful agents with advanced memory |
| `google/adk-python` | 19.5k | 2026-05-08 | Google's Agent Development Kit |
| `pydantic/pydantic-ai` | 16.9k | 2026-05-08 | Type-safe agents the Pydantic way |
| `cloudwego/eino` | 11.1k | 2026-05-08 | Go LLM/AI app development framework |
| `tmc/langchaingo` | 9.2k | 2026-01-11 | LangChain port to Go |
| `mark3labs/mcp-go` | 8.7k | 2026-05-07 | Go MCP implementation |
| `anthropics/claude-agent-sdk-python` | 6.7k | 2026-05-08 | Anthropic's official agent SDK |
| `modelcontextprotocol/go-sdk` | 4.5k | 2026-05-07 | Official Go MCP SDK (used by Harness) |
| **`sausheong/harness`** | new | 2026-05-08 | Go agent harness — this project |

## The core differentiator

The biggest differentiator is probably this:

> **Harness is built around the idea that the "harness" itself is the
> product.**

That aligns with the broader trend now emerging in agent engineering
where sophisticated teams increasingly see:

- prompts as replaceable,
- models as interchangeable,
- but the orchestration / runtime / context layer as strategic
  infrastructure.

Several industry analyses now explicitly discuss "agent harnesses" as
the core production layer. Harness is unusually aligned with that
direction compared to many mainstream frameworks.

## Harness vs LangGraph

LangGraph's worldview is graph-centric:

- nodes,
- edges,
- state transitions,
- checkpointing,
- explicit flow control.

It is essentially a deterministic workflow engine augmented with LLM
calls.

Harness is more runtime-centric:

- agent loop,
- tools,
- execution context,
- memory,
- orchestration primitives,
- iterative control,
- direct programming model.

That makes Harness less visually declarative but more operationally
flexible.

In practice:

- **LangGraph** excels when you want inspectable workflows,
  compliance-friendly orchestration, branching logic, resumability,
  and deterministic execution.
- **Harness** excels when you want deeply programmable autonomous
  loops and tight control over execution behavior.

This is similar to the difference between a Kubernetes workflow
engine vs. writing a custom distributed runtime.

## Harness vs CrewAI

CrewAI models agents as organizational roles:

- researcher,
- writer,
- reviewer,
- planner,
- coordinator.

It is intentionally high-level and optimized for rapid assembly of
collaborative multi-agent systems.

Harness is substantially lower-level and more infrastructural.

CrewAI gives:

- fast prototyping,
- approachable abstractions,
- less boilerplate,
- stronger business-process metaphors.

Harness gives:

- deeper runtime control,
- stronger composability,
- less abstraction leakage,
- potentially better operational transparency.

This means Harness likely scales better for:

- coding agents,
- system agents,
- long-running execution loops,
- terminal-integrated agents,
- infrastructure automation,
- embedded agent runtimes,
- and highly customized execution environments.

## Harness vs AutoGen / AG2

AutoGen's core abstraction is **conversation**. Agents communicate
through messages, often recursively.

Harness is more **action-loop-oriented**:

- perceive,
- reason,
- act,
- observe,
- repeat.

This matters operationally because conversational architectures
often become:

- verbose,
- token-heavy,
- difficult to constrain,
- and harder to debug at scale.

Harness's approach is likely more efficient for coding agents and
tool-heavy agents because the orchestration is procedural rather
than socially simulated.

This is an important distinction in 2026: many teams are moving
away from "agent societies" toward:

- compact execution loops,
- deterministic tool invocation,
- tighter context management,
- bounded autonomy.

Harness fits that newer pattern more closely.

## Harness vs Semantic Kernel

Semantic Kernel is the closest comparison in one specific dimension:
both think somewhat like systems frameworks rather than research
frameworks.

Semantic Kernel emphasizes:

- plugins,
- planners,
- enterprise interoperability,
- structured execution,
- dependency injection,
- typed interfaces.

Harness similarly appears designed by someone thinking about:

- runtime architecture,
- execution semantics,
- composable components,
- operational ownership.

But Semantic Kernel is fundamentally **enterprise platform
infrastructure inside the Microsoft ecosystem**.

Harness is closer to:

- hacker-native,
- runtime-native,
- local-first,
- agent-loop-native infrastructure.

## The language choice

Most modern frameworks are Python-first because:

- Python dominates AI tooling,
- libraries arrive there first,
- experimentation is faster.

Harness being in Go changes the operational profile substantially.

**Advantages:**

- simpler deployment,
- static binaries,
- lower memory footprint,
- stronger concurrency primitives,
- better operational predictability,
- easier embedding,
- easier cross-platform shipping,
- less dependency chaos,
- better long-running reliability.

**Disadvantages:**

- smaller ecosystem,
- fewer AI-native libraries,
- less community momentum,
- slower access to cutting-edge research tooling,
- fewer pretrained integrations.

This means Harness is likely stronger for:

- production runtimes,
- infrastructure agents,
- terminal agents,
- local agents,
- developer tooling,
- embedded systems,
- enterprise deployment.

But weaker for:

- rapid experimentation,
- academic workflows,
- notebook-centric research,
- and bleeding-edge ML integration.

## Abstraction philosophy

Most frameworks today add increasingly elaborate abstractions:

- planners,
- crews,
- graphs,
- role systems,
- memory hierarchies,
- declarative orchestration,
- event buses.

Harness is comparatively minimal and explicit.

That matters because the ecosystem is currently experiencing
**abstraction fatigue**.

Many teams are discovering:

- frameworks become constraining,
- hidden orchestration becomes difficult to debug,
- "magic" abstractions create production instability.

The industry trend is gradually shifting toward:

- thinner frameworks,
- explicit orchestration,
- agent runtimes instead of agent DSLs,
- ownership of execution semantics.

Harness aligns unusually well with that direction.

## The closer cousins: coding-agent runtimes

There is a notable resemblance between Harness and the emerging
"coding agent runtime" category:

- Claude Code internals,
- OpenClaw,
- OpenHands / OpenDevin runtime layers,
- terminal-native agent harnesses,
- execution-loop shells.

In that sense, Harness may actually be competing less with
LangGraph and more with:

- the substrate beneath coding agents,
- local execution runtimes,
- and autonomous execution environments.

That is strategically interesting because coding agents increasingly
need:

- filesystem control,
- shell orchestration,
- tool multiplexing,
- checkpointing,
- context engineering,
- long-lived execution,
- and recoverability.

Traditional "workflow agent frameworks" are often poor fits for
those needs.

## Where Harness is the right pick

- Coding agents
- Terminal-native agents
- Local-first agents
- Infrastructure automation
- Long-running autonomous loops
- Tool-heavy agent systems
- Systems-level orchestration
- Multi-agent runtimes where coordination is procedural rather than
  conversational
- Embedded agent execution
- Developer-centric agent platforms

Conceptually, Harness sits closest to:

- OpenClaw's runtime philosophy,
- Claude Code-style harnesses,
- lightweight autonomous execution runtimes,
- and programmable agent substrates.

Not to:

- BPMN-style orchestration systems,
- low-code agent builders,
- or research-oriented multi-agent simulators.

## Where Harness is the wrong pick

These are not bugs in Harness — they're the cost of staying minimal
and Go-native:

1. **Ecosystem gravity.** Python dominates AI orchestration today.
   That affects integrations, examples, tutorials, community
   tooling, observability vendors, and hosted platforms.

2. **Missing enterprise layers.** Compared to LangGraph or Semantic
   Kernel, things like observability, governance, HITL tooling,
   policy enforcement, deployment tooling, evaluation harnesses,
   tracing, RBAC, and managed execution may require more custom
   implementation.

3. **Smaller conceptual ecosystem.** CrewAI and LangGraph now have
   large mental-model ecosystems with tutorials, design patterns,
   and community recipes. Harness is currently more
   expert-oriented.

4. **Less declarative orchestration.** For highly regulated
   enterprise workflows, explicit graphs, deterministic execution,
   visual flows, and replayability are increasingly important.
   Harness may need additional layers for this.

## The strategic observation

Most current frameworks optimize for building **agents**.

Harness appears to optimize for **owning the runtime in which agents
execute**.

That distinction is becoming increasingly important as the ecosystem
matures.
