#!/usr/bin/env python3
"""合并单元测试与端到端插桩两份覆盖率剖面，输出按包汇总的真实覆盖率。

为什么需要合并：`go test -cover` 只能看到测试进程内执行到的代码，而
internal/api 与 internal/store 的绝大部分分支是被容器外的 Playwright
用例打过来的 HTTP 请求触发的。单看任何一份都会低估：单测看不见 HTTP
处理器，插桩看不见纯函数的边界用例。NFR-06 要求的是"这行代码到底有没有
被执行过"，所以取两者的并集才是诚实的答案。

用法（在 backend/ 目录下先生成两份剖面）：
    go test -coverprofile=/tmp/unit.txt ./...
    go tool covdata textfmt -i=../tests/covdata -o=/tmp/e2e.txt
    python3 ../tests/merge_coverage.py /tmp/unit.txt /tmp/e2e.txt
"""

from __future__ import annotations

import re
import sys
from collections import defaultdict

LINE = re.compile(r"^(?P<file>.+):(?P<span>\d+\.\d+,\d+\.\d+) (?P<stmts>\d+) (?P<count>\d+)$")


def load(path: str) -> dict[tuple[str, str], tuple[int, int]]:
    """读入一份剖面，返回 {(文件, 区间): (语句数, 执行次数)}。"""
    blocks: dict[tuple[str, str], tuple[int, int]] = {}
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw or raw.startswith("mode:"):
                continue
            m = LINE.match(raw)
            if not m:
                continue
            key = (m["file"], m["span"])
            stmts, count = int(m["stmts"]), int(m["count"])
            # 同一份剖面里同一区间可能出现多次（多个测试二进制），取和
            prev = blocks.get(key)
            if prev:
                stmts, count = prev[0], prev[1] + count
            blocks[key] = (stmts, count)
    return blocks


def package_of(file_path: str) -> str:
    """从剖面里的完整导入路径截出包名。"""
    return file_path.rsplit("/", 1)[0]


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__)
        return 2

    unit, e2e = load(sys.argv[1]), load(sys.argv[2])

    # 并集：任一份里被执行过就算覆盖
    per_pkg: dict[str, list[int]] = defaultdict(lambda: [0, 0, 0, 0])
    for key in set(unit) | set(e2e):
        u = unit.get(key, (0, 0))
        e = e2e.get(key, (0, 0))
        stmts = max(u[0], e[0])
        pkg = package_of(key[0])
        row = per_pkg[pkg]
        row[0] += stmts                                  # 总语句
        row[1] += stmts if (u[1] or e[1]) else 0         # 合并后覆盖
        row[2] += stmts if u[1] else 0                   # 仅单测
        row[3] += stmts if e[1] else 0                   # 仅端到端

    def pct(part: int, whole: int) -> str:
        return f"{100.0 * part / whole:.1f}%" if whole else "—"

    rows = sorted(per_pkg.items())
    width = max(len(p) for p, _ in rows)
    print(f"{'包':<{width}}  {'语句':>6} {'合并':>7} {'单测':>7} {'端到端':>7}")
    print("-" * (width + 32))
    tot = [0, 0, 0, 0]
    for pkg, (s, m, u, e) in rows:
        print(f"{pkg:<{width}}  {s:>6} {pct(m, s):>7} {pct(u, s):>7} {pct(e, s):>7}")
        for i, v in enumerate((s, m, u, e)):
            tot[i] += v
    print("-" * (width + 32))
    print(
        f"{'合计':<{width}}  {tot[0]:>6} {pct(tot[1], tot[0]):>7} "
        f"{pct(tot[2], tot[0]):>7} {pct(tot[3], tot[0]):>7}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
