package goldcup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// 允许出现 float64 的文件。
//
// 边界很清楚：**计算**必须走有理数，**展示**可以走 float64。
// 前端画图需要的是能塞进 Canvas 坐标的数字，把 PPM 整数扔给它
// 只会让它自己去做除法 —— 那反而把精度契约转移给了一个更不该负责的地方。
// 因此"转成 float64 供前端绘图"这一步是正当的，但它只能出现在
// 结果装配与图表几何这类文件里，绝不能出现在公式层。
var floatAllowedFiles = map[string]string{
	"engine.go":   "结果装配：把定点数转成 float64 供前端绘图",
	"zone.go":     "落区偏移量的展示值",
	"estimate.go": "推算模式的置信度与区间边界的展示值",
	"curve.go":    "控制图与偏好曲线的几何坐标",
	"advice.go":   "建议里的调整幅度展示值",
	"profile.go":  "金杯区间边界的展示值",
}

// formulaFiles 是必须完全无浮点的文件。
var formulaFiles = []string{"formula.go"}

// TestFormulaLayerHasNoFloat 用 AST 扫描守住"公式层不出现浮点"这条约束。
//
// 为何要机器检查而不是靠 code review：这条约束的违反极其隐蔽 ——
// 有人为了图方便写一句 float64(dose)/float64(water)，代码能编译、
// 测试大概率也过（因为 float64 精度远超本项目需要的 7 位），
// 于是它会一直留在那里，直到某天一个恰好落在边界上的值让判定翻转。
// 那时再去回溯是哪一行引入的，成本高得多。
func TestFormulaLayerHasNoFloat(t *testing.T) {
	fset := token.NewFileSet()

	for _, name := range formulaFiles {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if node.Name == "float64" || node.Name == "float32" {
					t.Errorf("%s:%s 出现了 %s。公式层必须全程使用 math/big.Rat，"+
						"仅在返回前量化一次为定点整数",
						name, fset.Position(node.Pos()), node.Name)
				}
			case *ast.BasicLit:
				if node.Kind == token.FLOAT {
					t.Errorf("%s:%s 出现了浮点字面量 %s。请改用 "+
						"fixed.MustRatioPercent / fixed.ParseGrams 等精确构造",
						name, fset.Position(node.Pos()), node.Value)
				}
			}
			return true
		})
	}
}

// TestFloatUsageIsDocumented 确保本包里每个用到 float64 的文件都在
// floatAllowedFiles 里登记过用途。
//
// 这不是形式主义：登记表本身就是这条精度约束的文档。若某天新增了一个
// 文件并在里面用了 float64，测试会失败并强迫作者回答一个问题 ——
// 这里的 float64 是用于展示，还是不小心用于了计算？
func TestFloatUsageIsDocumented(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		name := fi.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析包目录失败: %v", err)
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			usesFloat := false
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok &&
					(id.Name == "float64" || id.Name == "float32") {
					usesFloat = true
					return false
				}
				return true
			})
			if !usesFloat {
				continue
			}

			base := path
			if i := strings.LastIndexByte(base, '/'); i >= 0 {
				base = base[i+1:]
			}
			if _, ok := floatAllowedFiles[base]; !ok {
				t.Errorf("%s 使用了 float64 但未在 floatAllowedFiles 中登记用途。"+
					"若它只用于给前端提供展示值，请补上登记；"+
					"若它参与了任何计算，请改用 math/big.Rat。", base)
			}
		}
	}
}
