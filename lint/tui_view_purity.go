package main

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/dgageot/rubocop-go/cop"
)

// TUIViewPurity enforces that `View() string` methods on TUI models do not
// mutate the receiver's fields. This is the Bubble Tea / Elm-Architecture
// purity contract: rendering must be a pure function of state, otherwise
// rendering twice in a row can produce different output and the runtime is
// free to do exactly that (e.g. on resize, on a missed alt-screen redraw).
//
// The cop runs on every Go file under pkg/tui/ and inspects each method
// named View whose signature is `View() string`. Any assignment whose
// left-hand side is `recv.field` is flagged, with two pragmatic exemptions:
//
//   - Slice cache patterns are allowed:
//     recv.field = nil
//     recv.field = recv.field[:0]
//     recv.field = append(recv.field, …)
//     These are common for click-zone caches that View populates while
//     rendering and Update consumes when handling mouse events. They are a
//     known compromise; the field is not used elsewhere in View to influence
//     control flow.
//
//   - Lines carrying a //rubocop:disable Lint/TUIViewPurity comment (the
//     rubocop-go suppression form, distinct from golangci-lint's //nolint
//     so the two tools do not validate each other's rule names) are
//     skipped, allowing deliberate caches to be marked locally with a
//     short justification.
//
// Anything else — assigning a literal, a method call result, or a value
// that is also read elsewhere in View — is reported. Such mutations make
// View() effectively part of the state machine, which is exactly what
// Update() exists for.
type TUIViewPurity struct {
	cop.Meta
}

// NewTUIViewPurity returns a fully configured TUIViewPurity cop.
func NewTUIViewPurity() *TUIViewPurity {
	return &TUIViewPurity{Meta: cop.Meta{
		CopName:     "Lint/TUIViewPurity",
		CopDesc:     "View() methods on TUI models must not mutate the receiver",
		CopSeverity: cop.Warning,
	}}
}

func (c *TUIViewPurity) Check(p *cop.Pass) {
	if !isTUIFile(p.Filename()) {
		return
	}

	suppress := suppressedLines(p, c.Name())

	p.ForEachFunc(func(fn *ast.FuncDecl) {
		recv, ok := pointerReceiver(fn)
		if !ok || !isViewMethod(fn) {
			return
		}
		c.checkBody(p, fn.Body, recv, suppress)
	})
}

// checkBody walks fn body and reports an offense for every assignment to a
// receiver field that is not part of the slice-cache exemption set.
func (c *TUIViewPurity) checkBody(p *cop.Pass, body *ast.BlockStmt, recv string, suppress map[int]bool) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN {
			return true
		}
		for i, lhs := range assign.Lhs {
			field, ok := receiverField(lhs, recv)
			if !ok {
				continue
			}
			if i < len(assign.Rhs) && isSliceCachePattern(assign.Rhs[i], recv, field) {
				continue
			}
			line := p.FileSet.Position(assign.Pos()).Line
			if suppress[line] {
				continue
			}
			p.Report(assign,
				"View() must not mutate %s.%s; move the side effect to Update or compute it in a local variable"+
					" (or annotate the line with //rubocop:disable Lint/TUIViewPurity if it is an intentional click-zone cache)",
				recv, field)
		}
		return true
	})
}

// pointerReceiver returns the receiver name when fn has exactly one
// pointer receiver, e.g. (d *fooDialog).
func pointerReceiver(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return "", false
	}
	r := fn.Recv.List[0]
	if _, ok := r.Type.(*ast.StarExpr); !ok {
		return "", false
	}
	if len(r.Names) == 0 {
		return "", false
	}
	return r.Names[0].Name, true
}

// isViewMethod reports whether fn is exactly `func (...) View() string`.
// The cop intentionally ignores helpers that happen to be called View*
// because they are not part of the Bubble Tea contract.
func isViewMethod(fn *ast.FuncDecl) bool {
	if fn.Name.Name != "View" {
		return false
	}
	if fn.Type.Params != nil && len(fn.Type.Params.List) > 0 {
		return false
	}
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	id, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "string"
}

// receiverField returns the field name when expr is `recv.<field>` (a direct
// selector on the receiver) and reports whether the match succeeded.
// Nested selectors like recv.sub.field are intentionally not flagged because
// the cop cannot tell whether `recv.sub` aliases the receiver state.
func receiverField(expr ast.Expr, recv string) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != recv {
		return "", false
	}
	return sel.Sel.Name, true
}

// isSliceCachePattern reports whether rhs is one of the recognised
// "slice cache" idioms for the field recv.field:
//
//	nil
//	recv.field[:0]
//	append(recv.field, …)
//
// These are the shapes used by the click-zone caches that several TUI
// components populate during rendering.
func isSliceCachePattern(rhs ast.Expr, recv, field string) bool {
	if id, ok := rhs.(*ast.Ident); ok && id.Name == "nil" {
		return true
	}
	if slc, ok := rhs.(*ast.SliceExpr); ok && isReceiverField(slc.X, recv, field) {
		return true
	}
	if call, ok := rhs.(*ast.CallExpr); ok {
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "append" && len(call.Args) > 0 {
			if isReceiverField(call.Args[0], recv, field) {
				return true
			}
		}
	}
	return false
}

// isReceiverField reports whether expr is exactly `recv.field`.
func isReceiverField(expr ast.Expr, recv, field string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == recv && sel.Sel.Name == field
}

// isTUIFile reports whether filename lives under pkg/tui/, which is where
// Bubble Tea models are defined. The cop is scoped to this directory so
// that ad-hoc View() helpers elsewhere in the repository (e.g. test fakes)
// are not subject to the rule.
func isTUIFile(filename string) bool {
	slash := strings.ReplaceAll(filename, "\\", "/")
	return strings.Contains(slash, "/pkg/tui/") || strings.HasPrefix(slash, "pkg/tui/")
}

// suppressedLines builds the set of source lines that carry a
// //rubocop:disable <copName> comment. The directive intentionally uses a
// different prefix from golangci-lint's //nolint so that the two tools do
// not validate each other's rule names — golangci-lint's `nolintlint`
// rejects custom cops it has never heard of.
// Both inline trailing comments and full-line comments above the
// offending statement are honoured (covering the line on which the
// comment ends and the next line, respectively).
func suppressedLines(p *cop.Pass, copName string) map[int]bool {
	suppressed := map[int]bool{}
	for _, group := range p.File.Comments {
		for _, c := range group.List {
			if !mentionsCop(c.Text, copName) {
				continue
			}
			pos := p.FileSet.Position(c.Slash)
			end := p.FileSet.Position(c.End())
			// Inline comment: applies to the line where the comment ends
			// (Go scanner positions inline //... on the same line as code).
			suppressed[end.Line] = true
			// Full-line comment: applies to the next non-blank line.
			suppressed[pos.Line+1] = true
		}
	}
	return suppressed
}

// mentionsCop reports whether comment is a //rubocop:disable directive that
// names copName (case-sensitive, matched as a comma-separated token to
// avoid substring false positives such as "Lint/TUIViewPurityExtra").
func mentionsCop(comment, copName string) bool {
	const prefix = "//rubocop:disable"
	rest, ok := strings.CutPrefix(comment, prefix)
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	// Trim any trailing " // explanation" that follows the directive.
	if idx := strings.Index(rest, " "); idx >= 0 {
		rest = rest[:idx]
	}
	for name := range strings.SplitSeq(rest, ",") {
		if strings.TrimSpace(name) == copName {
			return true
		}
	}
	return false
}
