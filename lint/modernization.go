package main

import (
	"go/ast"
	"path/filepath"

	"github.com/dgageot/rubocop-go/cop"
	"github.com/dgageot/rubocop-go/prog"
)

// Share the program's resolved imports instead of the file runner's partial types.
func modernizationCop(fileCop *cop.Func) *prog.Func {
	return &prog.Func{
		Meta: fileCop.Meta,
		Run: func(p *prog.Pass) {
			for _, pkg := range p.Program.Packages {
				for _, file := range pkg.Syntax {
					if ast.IsGenerated(file) || frozenConfigPath.MatchString(filepath.ToSlash(p.Program.Fset.Position(file.Pos()).Filename)) {
						continue
					}
					pass := &cop.Pass{Cop: fileCop, FileSet: p.Program.Fset, File: file, Info: pkg.TypesInfo, Package: pkg.Types}
					fileCop.Check(pass)
					for _, offense := range pass.Offenses() {
						tf := p.Program.Fset.File(file.Pos())
						p.ReportAtf(tf.Pos(offense.Pos.Offset), tf.Pos(offense.End.Offset), "%s", offense.Message)
					}
				}
			}
		},
	}
}
