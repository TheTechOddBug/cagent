package main

import (
	"go/ast"
	"go/types"

	"github.com/dgageot/rubocop-go/cop"
)

// ConstructorCommandExec enforces that constructors do not prepare or run
// external commands directly.
//
// Constructors should assemble state and return it. Creating or executing an
// exec.Cmd there hides process lifecycle and I/O side effects behind New*, before
// the caller can decide when to start work, provide cancellation, or handle
// failures as part of an explicit operation.
//
// Detection is intentionally local and direct: calls to os/exec.Command and
// CommandContext are flagged, as are selector calls named Start, Run, Output, or
// CombinedOutput in constructor bodies. The selector-call check is deliberately
// broad at first and may catch non-exec methods with those names; keep any
// suppression close to the intentional constructor side effect.
//
// Calls inside nested function literals are ignored unless the literal is
// immediately invoked as part of constructor execution.
//
// Annotate an intentional case with //rubocop:disable Lint/ConstructorCommandExec.
var ConstructorCommandExec = &cop.Func{
	Meta: cop.Meta{
		Name:        "Lint/ConstructorCommandExec",
		Description: "constructors (New*) must not create or execute commands",
		Severity:    cop.Error,
	},
	Types: true,
	Run: func(p *cop.Pass) {
		p.ForEachFunc(func(fn *ast.FuncDecl) {
			if !isConstructor(fn) || fn.Body == nil {
				return
			}
			forEachConstructionCallExpr(fn.Body, func(call *ast.CallExpr) {
				if name, ok := commandConstructorCall(p, call); ok {
					p.Reportf(call,
						"constructor %s calls os/exec.%s; move command setup/execution behind an explicit method so process side effects are deliberate",
						fn.Name.Name, name)
				}
				if name, ok := commandExecutionMethodCall(call); ok {
					p.Reportf(call,
						"constructor %s calls .%s(); move command execution behind an explicit method so process side effects are deliberate",
						fn.Name.Name, name)
				}
			})
		})
	},
}

func commandConstructorCall(p *cop.Pass, call *ast.CallExpr) (string, bool) {
	if name, ok := osExecFuncName(calleeObject(p.Info, call)); ok {
		return name, true
	}
	return cop.CallTo(call, "exec", "Command", "CommandContext")
}

// calleeObject resolves the object a call's callee denotes, whether the callee
// is a bare identifier (f()) or a selector (pkg.F() / recv.F()). Returns nil
// when info is unavailable or the callee has no resolvable object.
func calleeObject(info *types.Info, call *ast.CallExpr) types.Object {
	if info == nil {
		return nil
	}
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return info.Uses[fun.Sel]
	case *ast.Ident:
		return info.Uses[fun]
	default:
		return nil
	}
}

func osExecFuncName(obj types.Object) (string, bool) {
	fn, ok := obj.(*types.Func)
	if !ok {
		return "", false
	}
	pkg := fn.Pkg()
	if pkg != nil && pkg.Path() == "os/exec" && (fn.Name() == "Command" || fn.Name() == "CommandContext") {
		return fn.Name(), true
	}
	return "", false
}

func commandExecutionMethodCall(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	name := sel.Sel.Name
	switch name {
	case "Start", "Run", "Output", "CombinedOutput":
		return name, true
	default:
		return "", false
	}
}

// forEachConstructionCallExpr invokes fn for every call expression that runs as
// part of executing body.
func forEachConstructionCallExpr(body *ast.BlockStmt, fn func(*ast.CallExpr)) {
	inspectConstructionBody(body, func(n ast.Node) {
		if call, ok := n.(*ast.CallExpr); ok {
			fn(call)
		}
	})
}

// isConstructor reports whether fn is a plain top-level function named New or
// New<Something> (the next rune after "New" is upper-case) that returns at
// least one result.
func isConstructor(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Name == nil {
		return false
	}
	name := fn.Name.Name
	if !isConstructorName(name) {
		return false
	}
	return fn.Type.Results != nil && len(fn.Type.Results.List) > 0
}

func isConstructorName(name string) bool {
	return name == "New" || (len(name) > 3 && name[:3] == "New" && name[3] >= 'A' && name[3] <= 'Z')
}

// inspectConstructionBody walks body and visits nodes that run as part of
// executing the constructor. Nested function literals are skipped unless they
// are immediately invoked (func() { ... })(), in which case their body runs
// during construction and is inspected.
func inspectConstructionBody(body *ast.BlockStmt, visit func(ast.Node)) {
	if body == nil {
		return
	}
	inspectConstructionNode(body, visit)
}

func inspectConstructionNode(root ast.Node, visit func(ast.Node)) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch s := n.(type) {
		case nil:
			return true
		case *ast.FuncLit:
			return false
		case *ast.GoStmt:
			visit(s)
			return false
		case *ast.CallExpr:
			visit(s)
			if fl := calledFuncLit(s.Fun); fl != nil {
				for _, arg := range s.Args {
					inspectConstructionNode(arg, visit)
				}
				inspectConstructionNode(fl.Body, visit)
				return false
			}
		default:
			visit(s)
		}
		return true
	})
}

func calledFuncLit(expr ast.Expr) *ast.FuncLit {
	for {
		switch e := expr.(type) {
		case *ast.FuncLit:
			return e
		case *ast.ParenExpr:
			expr = e.X
		default:
			return nil
		}
	}
}
