// Package migrations 以独立包的形式承载 SQL 迁移脚本。
//
// 为何单独成包：go:embed 只能嵌入包目录及其子目录下的文件，不允许 ".." 路径。
// 若让 store 包去嵌入 ../../migrations 会直接编译失败。把 SQL 与一个极小的
// embed 声明放在一起，既满足工具链约束，也让"迁移脚本"这件事有了明确的归属地。
package migrations

import (
	"embed"
	"io/fs"
	"sort"
)

//go:embed *.sql
var files embed.FS

// Script 是一个迁移脚本。
type Script struct {
	// Version 是脚本文件名，同时作为迁移版本号。
	Version string
	// Body 是 SQL 全文。
	Body string
}

// All 按版本升序返回全部迁移脚本。
//
// 文件名以四位数字前缀开头（0001_init.sql），因此字典序等价于版本序。
// 这个约定必须维持：一旦出现 10 个以上迁移且有人写成 "10_xxx.sql"，
// 字典序就会把它排到 "2_xxx.sql" 前面。四位补零是廉价的保险。
func All() ([]Script, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	out := make([]Script, 0, len(names))
	for _, n := range names {
		body, err := files.ReadFile(n)
		if err != nil {
			return nil, err
		}
		out = append(out, Script{Version: n, Body: string(body)})
	}
	return out, nil
}
