import { useCallback, useRef, useState } from 'react';
import type { PourCurve, PourPoint } from '@/api/types';
import { chartPalette, monoFont, sansFont, withAlpha } from './palette';
import { crisp, makeScale, useCanvas } from './useCanvas';
import { EmptyState } from '@/ui/Card';

const PAD = { top: 16, right: 52, bottom: 38, left: 48 };

/** 坐标轴上限取整到一个好看的刻度，避免最高点贴着画布顶。 */
function niceMax(v: number, step: number): number {
  if (v <= 0) return step;
  return Math.ceil((v * 1.1) / step) * step;
}

function ticks(max: number, count: number): number[] {
  const out: number[] = [];
  for (let i = 0; i <= count; i++) out.push((max / count) * i);
  return out;
}

/**
 * 注水流速曲线。
 *
 * 左轴累计注水量（g）画阶梯折线，右轴瞬时流速（g/s）画柱。
 *
 * 为什么累计量画阶梯而不是平滑曲线：注水打点是离散事件。
 * 把两个打点之间连成斜线或平滑曲线，等于宣称我们知道那段时间里
 * 水是怎么进去的 —— 而我们不知道。阶梯至少诚实地表达
 * 「在这一刻示数跳到了这个值」。
 */
export function PourCurveChart({
  curve,
  height = 260,
  live = false,
}: {
  curve: PourCurve | null;
  height?: number;
  live?: boolean;
}) {
  const [hover, setHover] = useState<{ p: PourPoint; x: number; y: number } | null>(
    null,
  );
  const frame = useRef<{ sx: (v: number) => number; maxT: number } | null>(null);

  const points = curve?.points ?? [];

  const draw = useCallback(
    (ctx: CanvasRenderingContext2D, size: { width: number; height: number }) => {
      const c = chartPalette();
      const { width: W, height: H } = size;
      const plotW = W - PAD.left - PAD.right;
      const plotH = H - PAD.top - PAD.bottom;
      if (plotW <= 0 || plotH <= 0 || points.length === 0) return;

      const maxT = Math.max(30, niceMax(points[points.length - 1]!.offset_sec, 15));
      const maxG = niceMax(curve?.total_water_g ?? 0, 50);
      const maxFlow = niceMax(curve?.peak_flow_rate ?? 0, 2);

      const sx = makeScale(0, maxT, PAD.left, PAD.left + plotW);
      const sg = makeScale(0, maxG, PAD.top + plotH, PAD.top);
      const sf = makeScale(0, maxFlow, PAD.top + plotH, PAD.top);
      frame.current = { sx, maxT };

      ctx.fillStyle = c.surface;
      ctx.fillRect(PAD.left, PAD.top, plotW, plotH);

      // ---- 闷蒸区间底色 ----
      if (curve?.has_bloom && curve.bloom_seconds > 0) {
        ctx.fillStyle = withAlpha(c.info, 0.1);
        ctx.fillRect(sx(0), PAD.top, sx(curve.bloom_seconds) - sx(0), plotH);

        ctx.font = sansFont(10);
        ctx.fillStyle = c.info;
        ctx.textAlign = 'left';
        ctx.textBaseline = 'top';
        ctx.fillText('闷蒸', sx(0) + 4, PAD.top + 4);
      }

      // ---- 网格 ----
      ctx.strokeStyle = c.border;
      ctx.lineWidth = 1;
      ctx.font = monoFont(10);
      ctx.fillStyle = c.text3;

      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      for (const t of ticks(maxT, 6)) {
        const x = crisp(sx(t));
        ctx.beginPath();
        ctx.moveTo(x, PAD.top);
        ctx.lineTo(x, PAD.top + plotH);
        ctx.stroke();
        ctx.fillText(`${Math.round(t)}s`, x, PAD.top + plotH + 6);
      }

      for (const g of ticks(maxG, 4)) {
        const y = crisp(sg(g));
        ctx.beginPath();
        ctx.moveTo(PAD.left, y);
        ctx.lineTo(PAD.left + plotW, y);
        ctx.stroke();
        ctx.textAlign = 'right';
        ctx.textBaseline = 'middle';
        ctx.fillStyle = c.text3;
        ctx.fillText(String(Math.round(g)), PAD.left - 6, y);
      }

      // 右轴流速刻度
      ctx.textAlign = 'left';
      for (const f of ticks(maxFlow, 4)) {
        ctx.fillStyle = withAlpha(c.info, 0.85);
        ctx.fillText(f.toFixed(1), PAD.left + plotW + 6, crisp(sf(f)));
      }

      // ---- 流速柱 ----
      ctx.save();
      ctx.beginPath();
      ctx.rect(PAD.left, PAD.top, plotW, plotH);
      ctx.clip();

      for (const seg of curve?.segments ?? []) {
        if (seg.is_pause || seg.flow_rate <= 0) continue;
        const x0 = sx(seg.from_ms / 1000);
        const x1 = sx(seg.to_ms / 1000);
        const y = sf(seg.flow_rate);
        const w = Math.max(2, x1 - x0 - 1);
        ctx.fillStyle = withAlpha(c.info, 0.3);
        ctx.fillRect(x0, y, w, PAD.top + plotH - y);
      }

      // ---- 累计量阶梯折线 ----
      // 分段绘制：断水段用虚线，注水段用实线。
      // 一次性画完再改线型是做不到的，setLineDash 作用于整条路径。
      for (let i = 1; i < points.length; i++) {
        const prev = points[i - 1]!;
        const cur = points[i]!;
        ctx.beginPath();
        // 先横走到当前时间（示数不变），再竖跳到新示数
        ctx.moveTo(sx(prev.offset_sec), sg(prev.cumulative_g));
        ctx.lineTo(sx(cur.offset_sec), sg(prev.cumulative_g));
        ctx.lineTo(sx(cur.offset_sec), sg(cur.cumulative_g));
        ctx.strokeStyle = c.brand;
        ctx.lineWidth = 2;
        ctx.setLineDash(cur.is_pause ? [3, 3] : []);
        ctx.stroke();
        ctx.setLineDash([]);
      }

      // ---- 打点标记 ----
      for (const p of points) {
        const x = sx(p.offset_sec);
        const y = sg(p.cumulative_g);
        const isHover = hover?.p.offset_ms === p.offset_ms;
        ctx.beginPath();
        ctx.arc(x, y, isHover ? 5 : 3, 0, Math.PI * 2);
        ctx.fillStyle = p.is_pause ? c.text3 : c.brand;
        ctx.fill();
        ctx.strokeStyle = c.bg;
        ctx.lineWidth = 1.2;
        ctx.stroke();
      }
      ctx.restore();

      // ---- 轴标题 ----
      ctx.font = sansFont(11);
      ctx.fillStyle = c.brand;
      ctx.textAlign = 'left';
      ctx.textBaseline = 'top';
      ctx.fillText('累计注水 (g)', PAD.left, 2);

      ctx.fillStyle = withAlpha(c.info, 0.9);
      ctx.textAlign = 'right';
      ctx.fillText('流速 (g/s)', PAD.left + plotW + PAD.right - 4, 2);

      ctx.strokeStyle = c.borderStrong;
      ctx.lineWidth = 1;
      ctx.strokeRect(crisp(PAD.left), crisp(PAD.top), Math.round(plotW), Math.round(plotH));
    },
    [curve, points, hover],
  );

  const { canvasRef, containerRef } = useCanvas(draw);

  const onMove = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const f = frame.current;
      if (!f || points.length === 0) return;
      const rect = e.currentTarget.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;

      // 只按 X 距离命中：用户想看的是"这个时刻的读数"，
      // 要求同时对准 Y 会让提示很难触发。
      let best: { p: PourPoint; d: number } | null = null;
      for (const p of points) {
        const d = Math.abs(f.sx(p.offset_sec) - mx);
        if (!best || d < best.d) best = { p, d };
      }
      if (best && best.d <= 16) {
        const found = best;
        setHover((prev) =>
          prev?.p.offset_ms === found.p.offset_ms ? prev : { p: found.p, x: mx, y: my },
        );
      } else {
        setHover((prev) => (prev === null ? prev : null));
      }
    },
    [points],
  );

  if (!curve || points.length === 0) {
    return (
      <div style={{ height }} className="grid place-items-center">
        <EmptyState
          title={live ? '等待第一次打点' : '这次冲煮没有注水记录'}
          description={
            live
              ? '按下「打点」按钮记录注水节点，曲线会实时长出来。'
              : '注水曲线需要至少两个打点才能算出流速。'
          }
        />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div ref={containerRef} className="relative w-full" style={{ height }}>
        <canvas
          data-testid="pour-canvas"
          ref={canvasRef}
          onMouseMove={onMove}
          onMouseLeave={() => setHover(null)}
          className="cursor-crosshair"
          role="img"
          aria-label={`注水曲线，共 ${points.length} 个打点，总注水 ${curve.total_water_g} 克`}
        />
        {hover && (
          <div
            className="absolute pointer-events-none z-10 px-2.5 py-1.5 rounded-[var(--r-sm)] border border-[var(--c-border-strong)] bg-[var(--c-surface-2)] text-[12px] whitespace-nowrap shadow-[var(--sh-2)]"
            style={{ left: hover.x + 10, top: 8 }}
          >
            <p className="num text-[var(--c-text)]">
              {hover.p.time_label} · {hover.p.cumulative_g.toFixed(1)}g
            </p>
            <p className="num text-[var(--c-text-2)]">
              流速 {hover.p.flow_rate.toFixed(2)} g/s
            </p>
            <p className="text-[var(--c-text-3)]">
              {hover.p.tech_label}
              {hover.p.is_pause && ' · 断水'}
            </p>
          </div>
        )}
      </div>

      {curve.insights.length > 0 && (
        <ul className="flex flex-col gap-1 px-1">
          {curve.insights.map((s, i) => (
            <li key={i} className="text-[12px] text-[var(--c-text-3)] leading-relaxed">
              · {s}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
