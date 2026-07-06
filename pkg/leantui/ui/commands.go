package ui

import (
	"sort"
	"strings"
)

// CommandKind distinguishes built-in lean-TUI commands (handled locally) from
// agent-provided commands and skills (resolved and sent to the agent).
type CommandKind int

const (
	CmdBuiltin CommandKind = iota
	CmdAgent
)

type Command struct {
	Name string
	Desc string
	Kind CommandKind
}

// BuiltinCommands are the slash commands the lean TUI handles itself.
func BuiltinCommands() []Command {
	return []Command{
		{Name: "new", Desc: "Start a new session", Kind: CmdBuiltin},
		{Name: "compact", Desc: "Summarize and compact the conversation", Kind: CmdBuiltin},
		{Name: "effort", Desc: "Set the model's reasoning effort (usage: /effort <level>)", Kind: CmdBuiltin},
		{Name: "clear", Desc: "Clear the screen", Kind: CmdBuiltin},
		{Name: "help", Desc: "Show keyboard shortcuts and commands", Kind: CmdBuiltin},
		{Name: "exit", Desc: "Exit", Kind: CmdBuiltin},
		{Name: "quit", Desc: "Exit", Kind: CmdBuiltin},
	}
}

// FilterCommands returns the commands whose name has the given prefix, built-in
// commands first, then agent commands, each group alphabetically sorted.
func FilterCommands(all []Command, prefix string) []Command {
	prefix = strings.ToLower(prefix)
	var out []Command
	for _, c := range all {
		if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}
