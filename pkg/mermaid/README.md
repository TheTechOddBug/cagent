# Mermaid support

Docker Agent parses supported Mermaid syntax in this package and renders it for
the terminal through `pkg/tui/components/mermaid`. Unsupported or unparseable
Mermaid blocks remain syntax-highlighted code blocks.

## Diagram support

| Mermaid diagram | Parse | Render | Support level |
|---|:---:|:---:|---|
| `graph` / `flowchart` | ✅ | ✅ | Partial |
| `sequenceDiagram` | ✅ | ✅ | Partial |
| `stateDiagram` / `stateDiagram-v2` | ✅ | ✅ | Partial |
| `classDiagram` | ❌ | ❌ | Falls back to code |
| `erDiagram` | ❌ | ❌ | Falls back to code |
| `journey` | ❌ | ❌ | Falls back to code |
| `gantt` | ❌ | ❌ | Falls back to code |
| `pie` | ❌ | ❌ | Falls back to code |
| `quadrantChart` | ❌ | ❌ | Falls back to code |
| `requirementDiagram` | ❌ | ❌ | Falls back to code |
| `gitGraph` | ❌ | ❌ | Falls back to code |
| `mindmap` | ❌ | ❌ | Falls back to code |
| `timeline` | ❌ | ❌ | Falls back to code |
| `sankey-beta` | ❌ | ❌ | Falls back to code |
| `xychart-beta` | ❌ | ❌ | Falls back to code |
| `block-beta` | ❌ | ❌ | Falls back to code |
| `packet-beta` | ❌ | ❌ | Falls back to code |
| `kanban` | ❌ | ❌ | Falls back to code |
| `architecture-beta` | ❌ | ❌ | Falls back to code |
| C4 diagrams | ❌ | ❌ | Falls back to code |

## Flowcharts

| Feature | Parse | Render | Notes |
|---|:---:|:---:|---|
| `graph` and `flowchart` headers | ✅ | ✅ | Both are accepted |
| `TD`, `TB`, `BT`, `LR`, `RL` direction | ✅ | ✅ | Rendered in the declared direction |
| Node declarations and references | ✅ | ✅ | Explicit and implicit nodes |
| Quoted labels | ✅ | ✅ | Includes semicolons inside labels |
| Chained edges | ✅ | ✅ | For example, `A --> B --> C` |
| `-->|label|` edge labels | ✅ | ✅ | |
| `-- label -->` edge labels | ✅ | ✅ | |
| Cycles and shared targets | ✅ | ✅ | Rendered using reference nodes |
| Rectangle nodes `[text]` | ✅ | ✅ | |
| Rounded nodes `(text)` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Stadium nodes `([text])` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Subroutine nodes `[[text]]` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Cylinder nodes `[(text)]` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Circle nodes `((text))` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Decision nodes `{text}` | ✅ | ✅ | Marked distinctly with `◇` |
| Hexagon nodes `{{text}}` | ✅ | ⚠️ | Parsed; currently rendered as a box |
| Edge line and arrow styles | ⚠️ | ❌ | Accepted operators are normalized to terminal connectors |
| `subgraph` | ✅ | ✅ | Labeled and nested groups are rendered as containers |
| `classDef`, `class`, `style` | ⚠️ | ❌ | Skipped |
| `click` links | ⚠️ | ❌ | Skipped |
| `linkStyle` | ⚠️ | ❌ | Skipped |

## Sequence diagrams

| Feature | Parse | Render | Notes |
|---|:---:|:---:|---|
| `participant` | ✅ | ✅ | |
| `actor` | ✅ | ⚠️ | Rendered as a participant box, not a stick figure |
| `as` aliases | ✅ | ✅ | For example, `participant C as Client` |
| Forward messages | ✅ | ✅ | |
| Return messages | ✅ | ✅ | Correct left/right direction |
| Dashed versus solid arrows | ⚠️ | ❌ | Syntax is accepted, but visual style is normalized |
| Self-messages | ✅ | ✅ | |
| `Note over A,B` | ✅ | ✅ | |
| `Note left of A` | ✅ | ✅ | |
| `Note right of A` | ✅ | ✅ | |
| Notes in timeline order | ✅ | ✅ | |
| Apostrophes in text | ✅ | ✅ | For example, `client's SYN` |
| `autonumber` | ❌ | ❌ | Ignored |
| `activate` / `deactivate` | ❌ | ❌ | Ignored |
| `alt` / `else` / `end` | ❌ | ❌ | Control frame omitted; contained messages may still render |
| `loop` | ❌ | ❌ | Frame omitted |
| `opt` | ❌ | ❌ | Frame omitted |
| `par` / `and` | ❌ | ❌ | Frame omitted |
| `critical` / `option` | ❌ | ❌ | Frame omitted |
| `break` | ❌ | ❌ | Frame omitted |
| Participant creation and destruction | ❌ | ❌ | |
| Links and participant menus | ❌ | ❌ | |

## State diagrams

| Feature | Parse | Render | Notes |
|---|:---:|:---:|---|
| `stateDiagram` | ✅ | ✅ | |
| `stateDiagram-v2` | ✅ | ✅ | |
| Simple transitions | ✅ | ✅ | |
| Transition labels | ✅ | ✅ | For example, `Idle --> Running: start` |
| Named state declarations | ✅ | ✅ | For example, `state "Processing" as Working` |
| Start state `[*] --> State` | ✅ | ✅ | Rendered as `Start` |
| End state `State --> [*]` | ✅ | ✅ | Rendered as `End` |
| Cyclic transitions | ✅ | ✅ | |
| Composite and nested states | ❌ | ❌ | |
| Concurrent states | ❌ | ❌ | |
| State notes | ❌ | ❌ | |
| Choice, fork, and join pseudostates | ❌ | ❌ | |
| State direction declarations | ✅ | ✅ | `TD`, `TB`, `BT`, `LR`, and `RL` are supported |
| Styling and classes | ❌ | ❌ | |
