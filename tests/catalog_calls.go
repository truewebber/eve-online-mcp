package tests

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	pathIdent  = "path"
	minESIArgs = 2
	pathArg    = 1
)

func esiVerb(name string) (string, bool) {
	switch name {
	case "Get", "GetAllPages", "GetCursorPages":
		return methodGET, true
	case "Post":
		return methodPOST, true
	case "Put":
		return methodPUT, true
	case "Delete":
		return methodDELETE, true
	default:
		return "", false
	}
}

type pkgIndex struct {
	funcs map[string]*fnScope
}

type fnScope struct {
	name   string
	params []string
	assign map[string][]ast.Expr
	calls  []rawCall
	invoc  []invocation
}

type rawCall struct {
	methods []string
	path    ast.Expr
	fun     ast.Expr
}

type invocation struct {
	name string
	args []ast.Expr
}

func extractCalls(root string) ([]esiCall, error) {
	fset := token.NewFileSet()
	var files []*ast.File
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("tests: walk %s: %w", p, err)
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || skipSource(p) {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return fmt.Errorf("tests: parse %s: %w", p, err)
		}
		files = append(files, f)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("tests: walk internal: %w", err)
	}
	pkg := indexPackage(files)
	var out []esiCall
	for _, f := range files {
		file := relInternal(root, fset.File(f.Pos()).Name())
		out = append(out, resolveFile(pkg, f, file)...)
	}

	return dedupeCalls(out), nil
}

func extractSource(filename, src string) ([]esiCall, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, fmt.Errorf("tests: parse %s: %w", filename, err)
	}
	pkg := indexPackage([]*ast.File{f})

	return dedupeCalls(resolveFile(pkg, f, filename)), nil
}

func indexPackage(files []*ast.File) *pkgIndex {
	idx := &pkgIndex{funcs: map[string]*fnScope{}}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			sc := walkFunc(fd.Name.Name, fd.Type, fd.Body)
			idx.funcs[fd.Name.Name] = sc
			for name, inner := range nestedFuncs(fd.Body) {
				idx.funcs[fd.Name.Name+"."+name] = inner
				idx.funcs[name] = inner
			}
		}
	}

	return idx
}

func walkFunc(name string, sig *ast.FuncType, body *ast.BlockStmt) *fnScope {
	sc := &fnScope{name: name, assign: map[string][]ast.Expr{}}
	if sig != nil && sig.Params != nil {
		for _, f := range sig.Params.List {
			for _, n := range f.Names {
				sc.params = append(sc.params, n.Name)
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.AssignStmt:
			recordAssign(sc, t)
		case *ast.CallExpr:
			sc.invoc = append(sc.invoc, invocation{name: calleeName(t.Fun), args: t.Args})
			if rc, ok := asRawCall(t); ok {
				sc.calls = append(sc.calls, rc)
			}
		}

		return true
	})

	return sc
}

func nestedFuncs(body *ast.BlockStmt) map[string]*fnScope {
	out := map[string]*fnScope{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		lit, ok := as.Rhs[0].(*ast.FuncLit)
		if !ok || lit.Body == nil {
			return true
		}
		out[id.Name] = walkFunc(id.Name, lit.Type, lit.Body)

		return true
	})

	return out
}

func recordAssign(sc *fnScope, as *ast.AssignStmt) {
	for i, lhs := range as.Lhs {
		if i >= len(as.Rhs) {
			continue
		}
		switch t := lhs.(type) {
		case *ast.Ident:
			sc.assign[t.Name] = append(sc.assign[t.Name], as.Rhs[i])
		case *ast.SelectorExpr:
			if identName(t.Sel) == pathIdent {
				sc.assign[".path"] = append(sc.assign[".path"], as.Rhs[i])
			}
		}
	}
}

func asRawCall(c *ast.CallExpr) (rawCall, bool) {
	if len(c.Args) < minESIArgs {
		return rawCall{}, false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if ok {
		method, known := esiVerb(identName(sel.Sel))
		if !known || !looksLikePath(c.Args[pathArg]) {
			return rawCall{}, false
		}

		return rawCall{methods: []string{method}, path: c.Args[pathArg], fun: c.Fun}, true
	}
	id, ok := c.Fun.(*ast.Ident)
	if !ok || !looksLikePath(c.Args[pathArg]) {
		return rawCall{}, false
	}

	return rawCall{path: c.Args[1], fun: id}, true
}

func looksLikePath(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(t.Value)

		return err == nil && strings.HasPrefix(s, "/")
	case *ast.CallExpr:
		return isPathCtor(t)
	case *ast.Ident:
		return t.Name == pathIdent
	case *ast.SelectorExpr:
		return identName(t.Sel) == pathIdent
	default:
		return false
	}
}

func isPathCtor(c *ast.CallExpr) bool {
	switch t := c.Fun.(type) {
	case *ast.Ident:
		return t.Name == "esiPath" || t.Name == "Path"
	case *ast.SelectorExpr:
		return identName(t.Sel) == "Path"
	default:
		return false
	}
}

func resolveFile(pkg *pkgIndex, f *ast.File, file string) []esiCall {
	var out []esiCall
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		sc := pkg.funcs[fd.Name.Name]
		if sc == nil {
			continue
		}
		out = append(out, resolveScope(pkg, sc, file)...)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(s, "/ui/") {
			return true
		}
		out = append(out, esiCall{Method: methodPOST, Path: normalizePath(s), File: file})

		return true
	})

	return out
}

func resolveScope(pkg *pkgIndex, sc *fnScope, file string) []esiCall {
	var out []esiCall
	for _, rc := range sc.calls {
		methods := rc.methods
		if len(methods) == 0 {
			methods = methodsOf(sc, rc.fun)
		}
		if len(methods) == 0 {
			continue
		}
		for _, p := range resolvePath(pkg, sc, rc.path) {
			for _, m := range methods {
				out = append(out, esiCall{Method: m, Path: p, File: file})
			}
		}
	}

	return out
}

func methodsOf(sc *fnScope, fun ast.Expr) []string {
	id, ok := fun.(*ast.Ident)
	if !ok {
		return nil
	}
	var out []string
	for _, rhs := range sc.assign[id.Name] {
		sel, ok := rhs.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if m, known := esiVerb(identName(sel.Sel)); known {
			out = appendUnique(out, m)
		}
	}

	return out
}

func resolvePath(pkg *pkgIndex, sc *fnScope, e ast.Expr) []string {
	if p, ok := pathOf(sc, e); ok {
		return []string{p}
	}
	switch t := e.(type) {
	case *ast.Ident:
		if got := pathsFromAssigns(sc, t.Name); len(got) > 0 {
			return got
		}
		idx := paramIndex(sc, t.Name)
		if idx < 0 {
			return nil
		}

		return callerPaths(pkg, sc.name, idx)
	case *ast.SelectorExpr:
		if identName(t.Sel) == pathIdent {
			return pathsFromAssigns(sc, ".path")
		}
	}

	return nil
}

func pathOf(sc *fnScope, e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(t.Value)
		if err != nil || !strings.HasPrefix(s, "/") {
			return "", false
		}

		return normalizePath(s), true
	case *ast.CallExpr:
		if !isPathCtor(t) {
			return "", false
		}

		return buildPath(t.Args), true
	case *ast.Ident:
		if sc == nil {
			return "", false
		}
		for _, rhs := range sc.assign[t.Name] {
			if p, ok := pathOf(sc, rhs); ok {
				return p, true
			}
		}
	}

	return "", false
}

func pathsFromAssigns(sc *fnScope, name string) []string {
	var out []string
	for _, rhs := range sc.assign[name] {
		if p, ok := pathOf(sc, rhs); ok {
			out = appendUnique(out, p)
		}
	}

	return out
}

func callerPaths(pkg *pkgIndex, fn string, arg int) []string {
	short := fn
	if i := strings.LastIndex(fn, "."); i >= 0 {
		short = fn[i+1:]
	}
	var out []string
	for _, sc := range pkg.funcs {
		for _, inv := range sc.invoc {
			if inv.name != fn && inv.name != short {
				continue
			}
			if arg >= len(inv.args) {
				continue
			}
			for _, p := range resolvePath(pkg, sc, inv.args[arg]) {
				out = appendUnique(out, p)
			}
		}
	}

	return out
}

func calleeName(fun ast.Expr) string {
	switch t := fun.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return identName(t.Sel)
	default:
		return ""
	}
}

func paramIndex(sc *fnScope, name string) int {
	for i, p := range sc.params {
		if p == name {
			return i
		}
	}

	return -1
}

func buildPath(args []ast.Expr) string {
	var parts []string
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok {
			parts = append(parts, "{}")

			continue
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			parts = append(parts, "{}")

			continue
		}
		parts = append(parts, s)
	}

	return normalizePath("/" + path.Join(parts...))
}

func identName(e ast.Expr) string {
	id, ok := e.(*ast.Ident)
	if !ok {
		return ""
	}

	return id.Name
}

func skipSource(p string) bool {
	if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".gen.go") {
		return true
	}

	return strings.Contains(filepath.ToSlash(p), "/mocks/")
}

func relInternal(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}

	return filepath.ToSlash(rel)
}

func dedupeCalls(in []esiCall) []esiCall {
	seen := map[string]esiCall{}
	for _, c := range in {
		if c.Path == "" {
			continue
		}
		seen[esiKey(c.Method, c.Path)+" "+c.File] = c
	}
	out := make([]esiCall, 0, len(seen))
	for _, c := range seen {
		out = append(out, c)
	}

	return out
}
