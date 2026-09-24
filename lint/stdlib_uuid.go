package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"github.com/dgageot/rubocop-go/cop"
	"github.com/dgageot/rubocop-go/prog"
)

// StdlibUUID leaves UUIDv5, custom random sources, and legacy parsers alone.
var StdlibUUID = &prog.Func{
	Meta: stdlibUUIDFile.Meta,
	Run: func(p *prog.Pass) {
		candidates := &prog.Pass{Cop: p.Cop, Program: p.Program}
		modernizationCop(stdlibUUIDFile).Check(candidates)
		if len(candidates.Offenses()) == 0 {
			return
		}
		pkgs, err := loadModernizationTests(p)
		if err != nil {
			p.Reportf(token.NoPos, "cannot check test UUID randomness: %v", err)
			return
		}
		// Test-installed randomness also controls production UUID calls.
		for _, pkg := range pkgs {
			for _, obj := range pkg.TypesInfo.Uses {
				if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "github.com/google/uuid" {
					switch obj.Name() {
					case "SetRand", "EnableRandPool", "DisableRandPool":
						return
					}
				}
			}
		}
		modernizationCop(stdlibUUIDFile).Check(p)
	},
}

var stdlibUUIDFile = &cop.Func{
	Meta: cop.Meta{
		Name:        "Lint/StdlibUUID",
		Description: "prefer standard-library UUIDs for random strings and canonical literals",
		Severity:    cop.Warning,
	},
	Types: true,
	Run: func(p *cop.Pass) {
		if p.Info == nil {
			return
		}
		p.ForEachCall(func(call *ast.CallExpr) {
			fn, ok := calleeObject(p.Info, call).(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/google/uuid" {
				return
			}
			switch fn.Name() {
			case "NewString":
				p.Report(call, "prefer uuid.NewV4().String() from the standard library unless custom Google UUID randomness is required; do not change configured random sources")
			case "String":
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return
				}
				inner, ok := ast.Unparen(selector.X).(*ast.CallExpr)
				if !ok {
					return
				}
				source, ok := calleeObject(p.Info, inner).(*types.Func)
				if !ok || source.Pkg() == nil || source.Pkg().Path() != "github.com/google/uuid" {
					return
				}
				switch source.Name() {
				case "New":
					p.Report(call, "prefer uuid.NewV4().String() from the standard library; preserve random UUIDv4 string semantics")
				case "MustParse":
					if len(inner.Args) != 1 {
						return
					}
					value := p.Info.Types[inner.Args[0]].Value
					if value != nil && value.Kind() == constant.String && canonicalUUID(constant.StringVal(value)) {
						p.Report(call, "prefer standard-library uuid.MustParse(literal).String() for this canonical literal; retain legacy compatibility parsers and Google UUIDv5 consumers")
					}
				}
			}
		})
	},
}

func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, ch := range s {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
				return false
			}
		}
	}
	return true
}
