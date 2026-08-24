package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 这组测试用 AST 比对「handler 实际读了哪些查询参数」与
// 「router.go 给它声明了哪些」。
//
// 为什么值得一个 AST 测试：查询参数名是字符串，编译器不管，
// 而写错的后果是静默返回未筛选的全量数据（本项目已经漏过四处）。
// StrictQuery 把这类错误变成 400，但前提是允许清单本身别过期 ——
// 有人给 handler 加一个 httpx.QueryBool(r, "only_decaf") 却忘了改清单，
// 那这个新参数就永远是 400，功能等于没接上。这个测试就是盯这件事的。

const paginationKeys = "page,page_size"

func TestEveryQueryParamIsDeclaredInItsAllowlist(t *testing.T) {
	fset := token.NewFileSet()
	pkg := parseAPIPackage(t, fset)

	lists := collectAllowlists(pkg)
	routes := collectStrictRoutes(pkg)
	reads := collectQueryReads(pkg)

	if len(reads) == 0 {
		t.Fatal("没有从 handler 里解析到任何查询参数读取点，说明这个测试的 AST 匹配已经失效")
	}

	for handler, keys := range reads {
		listName, wrapped := routes[handler]
		if !wrapped {
			t.Errorf("handler %s 读取了查询参数 %v，但路由没套 httpx.StrictQuery。\n"+
				"未被保护的查询参数写错时会被静默忽略，请在 router.go 里包上并声明允许清单。",
				handler, sortedKeys(keys))
			continue
		}
		allowed, ok := lists[listName]
		if !ok {
			t.Errorf("handler %s 引用的允许清单 %s 没有在 router.go 里定义", handler, listName)
			continue
		}
		for _, k := range sortedKeys(keys) {
			if _, ok := allowed[k]; !ok {
				t.Errorf("handler %s 读取了查询参数 %q，但 %s 没有声明它。\n"+
					"结果是这个参数永远返回 400，功能接不上。请把它加进 %s。",
					handler, k, listName, listName)
			}
		}
	}
}

func TestAllowlistsHaveNoUnreadEntries(t *testing.T) {
	fset := token.NewFileSet()
	pkg := parseAPIPackage(t, fset)

	lists := collectAllowlists(pkg)
	routes := collectStrictRoutes(pkg)
	reads := collectQueryReads(pkg)

	// 反向检查：清单里声明了但 handler 从不读的参数。
	// 这类残留比缺失更隐蔽 —— 调用方照着清单传参，后端收下却什么也不做，
	// 表现为"筛选没生效"，和参数名写错的症状一模一样。
	for handler, listName := range routes {
		allowed, ok := lists[listName]
		if !ok {
			continue
		}
		read := reads[handler]
		for k := range allowed {
			if _, ok := read[k]; !ok {
				t.Errorf("允许清单 %s 声明了 %q，但 handler %s 从不读取它。\n"+
					"调用方会以为这个参数有效，实际被收下后忽略 —— 请删掉它，或在 handler 里真的用上。",
					listName, k, handler)
			}
		}
	}
}

// ---------------------------------------------------------------- AST 辅助

func parseAPIPackage(t *testing.T, fset *token.FileSet) map[string]*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("枚举包内源文件失败: %v", err)
	}
	out := make(map[string]*ast.File, len(paths))
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", p, err)
		}
		out[p] = f
	}
	if len(out) == 0 {
		t.Fatal("包内没有找到源文件")
	}
	return out
}

// collectAllowlists 解析 router.go 里的 []string 允许清单变量，
// 支持 append(base, extra...) 这种拼接写法。
func collectAllowlists(pkg map[string]*ast.File) map[string]map[string]struct{} {
	raw := make(map[string][]ast.Expr)
	for _, f := range pkg {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						raw[name.Name] = []ast.Expr{vs.Values[i]}
					}
				}
			}
		}
	}

	out := make(map[string]map[string]struct{}, len(raw))
	for name := range raw {
		out[name] = make(map[string]struct{})
	}
	// 两轮展开足够覆盖 append(pageParams, ...) 这一层嵌套
	for pass := 0; pass < 2; pass++ {
		for name, exprs := range raw {
			for _, e := range exprs {
				flattenStrings(e, raw, out, out[name])
			}
		}
	}
	return out
}

func flattenStrings(
	e ast.Expr,
	raw map[string][]ast.Expr,
	resolved map[string]map[string]struct{},
	into map[string]struct{},
) {
	switch v := e.(type) {
	case *ast.CompositeLit:
		for _, elt := range v.Elts {
			if s, ok := stringLit(elt); ok {
				into[s] = struct{}{}
			}
		}
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "append" {
			for _, arg := range v.Args {
				flattenStrings(arg, raw, resolved, into)
			}
		}
	case *ast.Ident:
		for k := range resolved[v.Name] {
			into[k] = struct{}{}
		}
	}
}

// collectStrictRoutes 找出 httpx.StrictQuery(listVar, h.handler) 的配对。
func collectStrictRoutes(pkg map[string]*ast.File) map[string]string {
	out := make(map[string]string)
	for _, f := range pkg {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSelector(call.Fun, "httpx", "StrictQuery") || len(call.Args) != 2 {
				return true
			}
			listID, ok := call.Args[0].(*ast.Ident)
			if !ok {
				return true
			}
			sel, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			out[sel.Sel.Name] = listID.Name
			return true
		})
	}
	return out
}

// collectQueryReads 找出每个 handler 函数体里读到的查询参数名。
func collectQueryReads(pkg map[string]*ast.File) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	for _, f := range pkg {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue
			}
			keys := make(map[string]struct{})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgID, ok := sel.X.(*ast.Ident)
				if !ok || pkgID.Name != "httpx" {
					return true
				}

				// 分页走 ParsePagination，它内部读 page/page_size
				if sel.Sel.Name == "ParsePagination" {
					for _, k := range strings.Split(paginationKeys, ",") {
						keys[k] = struct{}{}
					}
					return true
				}
				if !strings.HasPrefix(sel.Sel.Name, "Query") {
					return true
				}
				// 签名统一为 Query*(r, name, ...)，参数名在第二位
				if len(call.Args) >= 2 {
					if s, ok := stringLit(call.Args[1]); ok {
						keys[s] = struct{}{}
					}
				}
				return true
			})
			if len(keys) > 0 {
				out[fn.Name.Name] = keys
			}
		}
	}
	return out
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
