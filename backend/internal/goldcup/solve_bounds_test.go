package goldcup

import (
	"strings"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// 三条反解路径必须共享同一条物理上界。
//
// 这个测试是补丁：SolveDose 曾经漏掉上界检查，请求 95% 萃取率时它会
// 一本正经地回答"称取 3.42g 粉"。漏得掉的原因是它的输出方向和另两条相反 ——
// 目标越离谱，粉量越小，于是 maxDose 那道上限完全拦不住。
// 逐条枚举而不是只测一条，就是为了让下一个新增的反解方向也被迫对齐。
func TestAllSolvePathsRejectImpossibleYield(t *testing.T) {
	dose := g(t, "20")
	bev := g(t, "260")
	tds := fixed.MustRatioPercent("1.25")

	// 31% 刚过物理天花板，95% 是用户填错单位时的典型值
	for _, pct := range []string{"30.0001", "31", "50", "95", "100"} {
		y := fixed.MustRatioPercent(pct)

		t.Run("dose@"+pct, func(t *testing.T) {
			got, err := SolveDose(y, tds, bev)
			if err == nil {
				t.Fatalf("目标萃取率 %s%% 不可达，却反解出粉量 %s —— "+
					"一个不可能的目标被当成可执行配方返回了", pct, got.Grams())
			}
			assertYieldRangeError(t, err)
		})

		t.Run("tds@"+pct, func(t *testing.T) {
			if _, err := SolveTDS(y, dose, bev); err == nil {
				t.Fatalf("目标萃取率 %s%% 不可达，SolveTDS 却成功了", pct)
			}
		})

		t.Run("beverage@"+pct, func(t *testing.T) {
			if _, err := SolveBeverage(y, tds, dose); err == nil {
				t.Fatalf("目标萃取率 %s%% 不可达，SolveBeverage 却成功了", pct)
			}
		})
	}
}

// 上界之内必须照常工作，否则上面那个测试可以靠"全部拒绝"作弊通过。
func TestSolvePathsAcceptAttainableYield(t *testing.T) {
	dose := g(t, "20")
	bev := g(t, "260")
	tds := fixed.MustRatioPercent("1.25")

	for _, pct := range []string{"14", "18", "20", "22", "26", "30"} {
		y := fixed.MustRatioPercent(pct)

		if _, err := SolveDose(y, tds, bev); err != nil {
			t.Errorf("萃取率 %s%% 在物理上可达，SolveDose 却拒绝了: %v", pct, err)
		}
		if _, err := SolveTDS(y, dose, bev); err != nil {
			t.Errorf("萃取率 %s%% 在物理上可达，SolveTDS 却拒绝了: %v", pct, err)
		}
		if _, err := SolveBeverage(y, tds, dose); err != nil {
			t.Errorf("萃取率 %s%% 在物理上可达，SolveBeverage 却拒绝了: %v", pct, err)
		}
	}
}

// 30% 这个边界值本身要能通过：它是可溶物总量的上限，属于闭区间。
func TestThirtyPercentIsInclusive(t *testing.T) {
	y := fixed.MustRatioPercent("30")
	if _, err := SolveDose(y, fixed.MustRatioPercent("1.25"), g(t, "260")); err != nil {
		t.Errorf("30%% 是闭区间上界，应被接受: %v", err)
	}
}

func assertYieldRangeError(t *testing.T, err error) {
	t.Helper()
	// 报错要说清是哪条约束，否则用户只知道"失败了"，
	// 不知道自己是把 1.25 填成了 125
	if !strings.Contains(err.Error(), "30") {
		t.Errorf("报错应说明 30%% 这条物理上限，实际: %v", err)
	}
}
