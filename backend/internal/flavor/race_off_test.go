//go:build !race

package flavor

// raceEnabled 标记当前二进制是否开着竞态检测器编译。
//
// 存在的理由见 race_on_test.go。
const raceEnabled = false
