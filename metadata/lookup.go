package metadata

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/podhmo/commentof"
	"github.com/podhmo/commentof/collect"
	"github.com/podhmo/reflect-shape/metadata/internal/unsaferuntime"
	"golang.org/x/tools/go/packages"
)

// TODO: cache

// ErrNotFound is the error metadata is not found.
var ErrNotFound = fmt.Errorf("not found")

type Lookup struct {
	Fset     *token.FileSet
	accessor *unsaferuntime.Accessor

	IncludeGoTestFiles bool
	IncludeUnexported  bool
}

func NewLookup(fset *token.FileSet) *Lookup {
	return &Lookup{
		Fset:               fset,
		accessor:           unsaferuntime.New(),
		IncludeGoTestFiles: false,
		IncludeUnexported:  false,
	}
}

type Func struct {
	pc   uintptr
	Raw  *collect.Func
	Recv string
}

func (m *Func) Fullname() string {
	return runtime.FuncForPC(m.pc).Name()
}

func (m *Func) Name() string {
	return m.Raw.Name
}

func (m *Func) Doc() string {
	return strings.TrimSpace(m.Raw.Doc)
}

func (m *Func) Args() []string {
	names := make([]string, len(m.Raw.ParamNames))
	for i, id := range m.Raw.ParamNames {
		names[i] = m.Raw.Params[id].Name
	}
	return names
}

func (m *Func) Returns() []string {
	names := make([]string, len(m.Raw.ReturnNames))
	for i, id := range m.Raw.ReturnNames {
		names[i] = m.Raw.Returns[id].Name
	}
	return names
}

func (l *Lookup) LookupFromFunc(fn interface{}) (*Func, error) {
	pc := reflect.ValueOf(fn).Pointer()
	return l.LookupFromFuncForPC(pc)
}

func (l *Lookup) LookupFromFuncForPC(pc uintptr) (*Func, error) {
	rfunc := l.accessor.FuncForPC(pc)
	if rfunc == nil {
		return nil, fmt.Errorf("cannot find runtime.Func")
	}

	filename, _ := rfunc.FileLine(rfunc.Entry())
	f, err := parser.ParseFile(l.Fset, filename, nil, parser.ParseComments)
	if f == nil {
		return nil, err
	}

	// TODO: package cache
	p, err := commentof.File(l.Fset, f, commentof.WithIncludeUnexported(l.IncludeUnexported))
	if err != nil {
		return nil, err
	}

	// /<pkg name>.<function name>
	// /<pkg name>.<recv>.<method name>
	// /<pkg name>.<recv>.<method name>-fm

	parts := strings.Split(rfunc.Name(), "/")
	last := parts[len(parts)-1]
	pkgname, name, isFunc := strings.Cut(last, ".")
	_ = pkgname
	if !isFunc {
		return nil, fmt.Errorf("unexpected func: %v", rfunc.Name())
	}

	recv, name, isMethod := strings.Cut(name, ".")
	if isMethod {
		recv = strings.Trim(recv, "(*)")
	} else {
		name = recv
		recv = ""
	}
	// fmt.Printf("pkgname:%-15s\trecv:%-10s\tname:%s\n", pkgname, recv, name)

	if isMethod {
		ob, ok := p.Types[recv]
		if !ok {
			return nil, fmt.Errorf("lookup metadata of method %s, %w", rfunc.Name(), ErrNotFound)
		}
		result, ok := ob.Methods[name]
		if !ok {
			return nil, fmt.Errorf("lookup metadata of method %s, %w", rfunc.Name(), ErrNotFound)
		}
		return &Func{pc: pc, Raw: result, Recv: recv}, nil
	} else {
		result, ok := p.Functions[name]
		if !ok {
			return nil, fmt.Errorf("lookup metadata of function %s, %w", rfunc.Name(), ErrNotFound)
		}
		return &Func{pc: pc, Raw: result}, nil
	}
}

type Struct struct {
	Raw *collect.Object
}

func (s *Struct) Name() string {
	return s.Raw.Name
}

func (s *Struct) Doc() string {
	doc := s.Raw.Doc
	if doc == "" {
		doc = s.Raw.Comment
	}
	return strings.TrimSpace(doc)
}

func (s *Struct) FieldComments() map[string]string {
	comments := make(map[string]string, len(s.Raw.Fields))
	for _, f := range s.Raw.Fields {
		doc := f.Doc
		if doc == "" {
			doc = f.Comment
		}
		comments[f.Name] = strings.TrimSpace(doc)
	}
	return comments
}

func (l *Lookup) LookupFromStruct(ob interface{}) (*Struct, error) {
	rt := reflect.TypeOf(ob)
	return l.LookupFromStructForReflectType(rt)
}
func (l *Lookup) LookupFromStructForReflectType(rt reflect.Type) (*Struct, error) {
	obname := rt.Name()
	pkgpath := rt.PkgPath()

	if pkgpath == "main" {
		binfo, ok := debug.ReadBuildInfo()
		if !ok {
			log.Println("debug.ReadBuildInfo() is failed")
			return nil, ErrNotFound
		}
		pkgpath = binfo.Path
	}

	cfg := &packages.Config{
		Fset:  l.Fset,
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax,
		Tests: l.IncludeGoTestFiles, // TODO: support <name>_test package
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			// TODO: debug print
			const mode = parser.ParseComments //| parser.AllErrors
			return parser.ParseFile(fset, filename, src, mode)
		},
	}

	pkgs, err := packages.Load(cfg, pkgpath)
	if err != nil {
		return nil, fmt.Errorf("packages.Load() %w", err)
	}

	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			for _, err := range pkg.Errors {
				log.Printf("lookup package error (%s) %+v", pkg, err)
			}
			continue
		}

		if pkg.PkgPath != pkgpath {
			continue
		}
		tree := &ast.Package{Name: pkg.Name, Files: map[string]*ast.File{}}
		for _, f := range pkg.Syntax {
			filename := l.Fset.File(f.Pos()).Name()
			tree.Files[filename] = f
		}

		p, err := commentof.Package(l.Fset, tree, commentof.WithIncludeUnexported(l.IncludeUnexported))
		if err != nil {
			return nil, fmt.Errorf("collect: dir=%s, name=%s, %w", pkg.PkgPath, obname, err)
		}
		result, ok := p.Types[rt.Name()]
		if !ok {
			continue
		}
		return &Struct{Raw: result}, nil
	}
	return nil, fmt.Errorf("lookup metadata of %v is failed %w", rt, ErrNotFound)
}
