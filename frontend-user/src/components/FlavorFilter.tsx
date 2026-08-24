import { useMemo, useState } from 'react';
import type { FilterResult, FlavorTreeNode, MatchMode } from '@/api/types';
import { Badge } from '@/ui/Card';
import { cx } from '@/lib/cx';

/**
 * 风味多级联动筛选器。
 *
 * 「联动」的含义：选中一个节点后，其他节点上显示的数字变成
 * 「再加上这个条件还能剩几支豆」（后端的 facets）。这让用户能预见
 * 下一步点击的结果，避免点出一个空结果再退回来。
 *
 * facets 由后端在同一次请求里算出（NFR-01 的预算包含它），
 * 前端不做交集计算 —— 那需要把全部豆子的标签下载到浏览器。
 */
export function FlavorFilter({
  tree,
  selected,
  onChange,
  match,
  onMatchChange,
  result,
}: {
  tree: FlavorTreeNode[];
  selected: number[];
  onChange: (ids: number[]) => void;
  match: MatchMode;
  onMatchChange: (m: MatchMode) => void;
  result: FilterResult | null;
}) {
  const [expanded, setExpanded] = useState<Set<number>>(
    // 默认展开第一层，让用户一进来就看到可点的东西
    () => new Set(tree.map((n) => n.id)),
  );

  const facetMap = useMemo(() => {
    const m = new Map<number, number>();
    for (const f of result?.facets ?? []) m.set(f.node_id, f.remaining);
    return m;
  }, [result]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);

  const toggle = (id: number) => {
    onChange(selectedSet.has(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  };

  const renderNode = (node: FlavorTreeNode, depth: number) => {
    const isSel = selectedSet.has(node.id);
    const hasKids = node.children.length > 0;
    const open = expanded.has(node.id);
    const remaining = facetMap.get(node.id);

    // 加上这个条件会得到空结果时置灰。不隐藏 —— 隐藏会让分类树的形状
    // 随筛选跳变，用户会失去方位感。
    const wouldBeEmpty = remaining !== undefined && remaining === 0 && !isSel;

    return (
      <li key={node.id}>
        <div
          className="flex items-center gap-1"
          style={{ paddingLeft: depth * 14 }}
        >
          {hasKids ? (
            <button
              onClick={() =>
                setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(node.id)) next.delete(node.id);
                  else next.add(node.id);
                  return next;
                })
              }
              aria-label={open ? `折叠 ${node.name}` : `展开 ${node.name}`}
              className="w-5 h-5 shrink-0 grid place-items-center text-[var(--c-text-3)] hover:text-[var(--c-text)] cursor-pointer text-[10px]"
            >
              {open ? '▾' : '▸'}
            </button>
          ) : (
            <span className="w-5 shrink-0" />
          )}

          <button
            data-testid="flavor-filter-node"
            onClick={() => toggle(node.id)}
            disabled={wouldBeEmpty}
            aria-pressed={isSel}
            className={cx(
              'flex-1 flex items-center justify-between gap-2 px-2 py-1 rounded-[var(--r-sm)]',
              'text-left text-[13px] transition-colors duration-[120ms] cursor-pointer',
              isSel
                ? 'bg-[var(--c-brand-dim)] text-[var(--c-brand)] font-medium'
                : 'text-[var(--c-text-2)] hover:bg-[var(--c-surface-2)] hover:text-[var(--c-text)]',
              wouldBeEmpty && 'opacity-35 cursor-not-allowed hover:bg-transparent',
            )}
          >
            <span className="flex items-center gap-1.5 min-w-0">
              <span
                className="w-2 h-2 rounded-full shrink-0"
                style={{ background: node.color || 'var(--c-border-strong)' }}
              />
              <span className="truncate">{node.name}</span>
            </span>
            <span className="num text-[11px] text-[var(--c-text-3)] shrink-0">
              {/* 有筛选条件时显示联动剩余数，否则显示该子树的豆子总数 */}
              {remaining !== undefined ? remaining : node.aggregate_bean_count}
            </span>
          </button>
        </div>

        {hasKids && open && (
          <ul>{node.children.map((k) => renderNode(k, depth + 1))}</ul>
        )}
      </li>
    );
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex rounded-[var(--r-sm)] overflow-hidden border border-[var(--c-border)]">
          {(['ALL', 'ANY'] as MatchMode[]).map((m) => (
            <button
              key={m}
              onClick={() => onMatchChange(m)}
              className={cx(
                'px-2.5 py-1 text-[12px] cursor-pointer transition-colors',
                match === m
                  ? 'bg-[var(--c-brand)] text-[#1a1208] font-medium'
                  : 'text-[var(--c-text-3)] hover:bg-[var(--c-surface-2)]',
              )}
            >
              {m === 'ALL' ? '同时满足' : '任一满足'}
            </button>
          ))}
        </div>
        {selected.length > 0 && (
          <button
            onClick={() => onChange([])}
            className="text-[12px] text-[var(--c-text-3)] hover:text-[var(--c-text)] cursor-pointer"
          >
            清空 ({selected.length})
          </button>
        )}
      </div>

      {/* 有筛选条件被丢弃时必须说出来。静默忽略会让用户以为筛选生效了，
          实际看到的是一份没被过滤的列表。 */}
      {result && result.unknown_node_ids.length > 0 && (
        <p className="text-[12px] text-[var(--c-warn)]">
          有 {result.unknown_node_ids.length} 个筛选条件已失效（对应的风味分类被删了），
          已自动忽略。
        </p>
      )}

      {result && result.warning && (
        <p className="text-[12px] text-[var(--c-warn)]">{result.warning}</p>
      )}

      <ul className="flex flex-col gap-0.5 max-h-[420px] overflow-y-auto -mx-1 px-1">
        {tree.map((n) => renderNode(n, 0))}
      </ul>

      {result && (
        <div className="flex items-center justify-between gap-2 pt-2 border-t border-[var(--c-border)]">
          <span className="text-[12px] text-[var(--c-text-3)]">
            命中 <span className="num text-[var(--c-text)]">{result.matched_count}</span> /{' '}
            {result.total_beans} 支
          </span>
          {/* 把 NFR-01 的实测耗时显示出来。这不是给用户看的性能炫耀，
              而是让"筛选很慢"这件事一旦发生就立刻可见。 */}
          <Badge color="var(--c-text-3)" bg="var(--c-surface-2)">
            {(result.elapsed_micros / 1000).toFixed(2)} ms
          </Badge>
        </div>
      )}
    </div>
  );
}
