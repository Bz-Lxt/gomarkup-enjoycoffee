import { useState } from 'react';
import type { Freshness } from '@/api/types';
import { semantic } from '@/lib/semantic';
import { Badge } from './Card';

/**
 * 豆子新鲜度生命周期进度条。
 *
 * 几何量全部来自后端：每段的 width_percent、当前位置的 progress_percent、
 * 每段的 color_hint 都是后端算好的。前端不算百分比，理由是烘焙度决定的
 * 窗口长度、开封氧化对衰退日的压缩、GMT+8 民用日的边界——
 * 这些规则已经在后端有测试覆盖，在前端算第二遍必然出现两套结果不一致。
 */
export function LifecycleBar({ freshness }: { freshness: Freshness }) {
  const [hover, setHover] = useState<number | null>(null);
  const f = freshness;

  const segments = f.segments ?? [];
  if (segments.length === 0) return null;

  const cursor = Math.min(100, Math.max(0, f.progress_percent));

  return (
    <div className="flex flex-col gap-2" data-testid="lifecycle-bar">
      <div className="relative pt-6">
        {/* 游标气泡：显示当前是烘焙后第几天 */}
        <div
          className="absolute top-0 -translate-x-1/2 transition-[left] duration-300"
          style={{ left: `${cursor}%` }}
        >
          <span className="num inline-block px-1.5 py-0.5 rounded-[var(--r-sm)] bg-[var(--c-surface-3)] text-[11px] text-[var(--c-text)] whitespace-nowrap">
            第 {f.roast_age_days} 天
          </span>
        </div>

        <div className="relative h-[10px] rounded-[var(--r-full)] overflow-hidden flex gap-[1px] bg-[var(--c-bg)]">
          {segments.map((seg, i) => {
            const c = semantic(seg.color_hint);
            const active = seg.stage === f.stage;
            return (
              <div
                key={`${seg.stage}-${i}`}
                // 各段宽度之和应为 100。暴露出来供 E2E 校验
                // "几何来自后端"这条约束真的成立。
                data-width-percent={seg.width_percent}
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover(null)}
                title={`${seg.label}：第 ${seg.start_day}–${seg.end_day} 天`}
                style={{
                  width: `${seg.width_percent}%`,
                  background: active ? c.fg : c.line,
                  opacity: hover === null || hover === i ? 1 : 0.5,
                }}
                className="h-full transition-opacity duration-[120ms]"
              />
            );
          })}
        </div>

        {/* 白色游标线压在色段之上 */}
        <div
          className="absolute top-6 h-[10px] w-[2px] bg-white rounded-full pointer-events-none transition-[left] duration-300"
          style={{ left: `calc(${cursor}% - 1px)` }}
          aria-hidden="true"
        />
      </div>

      <div className="flex items-center justify-between gap-2 flex-wrap">
        <Badge color={semantic(f.color_hint).fg} bg={semantic(f.color_hint).bg}>
          {f.stage_label}
        </Badge>
        <span className="text-[12px] text-[var(--c-text-3)]">
          {f.days_until_next_stage > 0 && f.next_stage_label
            ? `还有 ${f.days_until_next_stage} 天进入「${f.next_stage_label}」`
            : f.advice}
        </span>
      </div>

      {/* 开封压缩了衰退日时明确说出来 —— 否则用户会疑惑为什么
          同样的烘焙日期，这支豆比另一支先过期。 */}
      {f.limited_by === 'OPENING' && (
        <p className="text-[12px] text-[var(--c-info)]">
          已开封，氧化把衰退日提前到 {f.effective_decline_on}
        </p>
      )}
    </div>
  );
}

/**
 * 未填烘焙日期时的引导。
 *
 * 不画一条空的进度条：空进度条会被读成"这支豆还很新鲜"，
 * 而实际情况是我们不知道（DesignSpec §5.4）。
 */
export function LifecycleUnknown({ onFill }: { onFill?: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3 py-2">
      <span className="text-[13px] text-[var(--c-text-3)]">
        没有烘焙日期，算不出新鲜度
      </span>
      {onFill && (
        <button
          onClick={onFill}
          className="text-[13px] text-[var(--c-brand)] hover:text-[var(--c-brand-hover)] underline underline-offset-2 cursor-pointer"
        >
          补填烘焙日期
        </button>
      )}
    </div>
  );
}
