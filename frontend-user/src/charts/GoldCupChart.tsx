import { useCallback, useMemo, useRef, useState } from 'react';
import type { ChartPoint, GoldCupChart as ChartData } from '@/api/types';
import { chartPalette, hintColor, monoFont, sansFont, withAlpha } from './palette';
import { crisp, makeScale, useCanvas } from './useCanvas';

const PAD = { top: 18, right: 20, bottom: 42, left: 54 };

/**
 * 金杯控制图。
 *
 * 横轴萃取率、纵轴浓度（TDS），九宫格落区 + 等比例双曲线族 + 历史冲煮散点。
 *
 * 所有几何量来自后端：坐标轴范围与刻度（axis_x / axis_y）、
 * 九宫格的矩形边界（zones）、等比例线的两个端点（iso_ratios）。
 * 前端只做「数据坐标 → 像素坐标」的线性变换。
 *
 * 这么分工的原因是金杯区间是用户可配置的（V-07）：一旦前端自己算区间边界，
 * 用户改了设置就会看到图上的格子和诊断文字对不上。
 */
export function GoldCupChart({
  data,
  onPointClick,
  height = 420,
}: {
  data: ChartData;
  onPointClick?: (p: ChartPoint) => void;
  height?: number;
}) {
  const [hover, setHover] = useState<{ p: ChartPoint; x: number; y: number } | null>(
    null,
  );

  // 命中检测要用绘制时的同一套坐标变换。存在 ref 而不是 state：
  // 它只被鼠标事件读取，放进 state 会让每次重绘都多触发一轮渲染。
  const frame = useRef<{
    sx: (v: number) => number;
    sy: (v: number) => number;
  } | null>(null);

  const draw = useCallback(
    (ctx: CanvasRenderingContext2D, size: { width: number; height: number }) => {
      const c = chartPalette();
      const { width: W, height: H } = size;
      const plotW = W - PAD.left - PAD.right;
      const plotH = H - PAD.top - PAD.bottom;
      if (plotW <= 0 || plotH <= 0) return;

      const ax = data.axis_x;
      const ay = data.axis_y;
      // Y 轴翻转：像素坐标向下增长，浓度向上增长
      const sx = makeScale(ax.min, ax.max, PAD.left, PAD.left + plotW);
      const sy = makeScale(ay.min, ay.max, PAD.top + plotH, PAD.top);
      frame.current = { sx, sy };

      ctx.fillStyle = c.surface;
      ctx.fillRect(PAD.left, PAD.top, plotW, plotH);

      // ---- 九宫格落区 ----
      for (const z of data.zones) {
        const x0 = sx(z.x_min);
        const x1 = sx(z.x_max);
        const y0 = sy(z.y_max);
        const y1 = sy(z.y_min);
        const col = hintColor(z.severity_hue);

        ctx.fillStyle = withAlpha(col, z.in_gold_cup ? 0.16 : 0.07);
        ctx.fillRect(x0, y0, x1 - x0, y1 - y0);

        // 金杯格额外描边，让它在九个格子里一眼可辨
        if (z.in_gold_cup) {
          ctx.strokeStyle = col;
          ctx.lineWidth = 1;
          ctx.strokeRect(crisp(x0), crisp(y0), Math.round(x1 - x0), Math.round(y1 - y0));
        }
      }

      // ---- 网格线与刻度 ----
      ctx.strokeStyle = c.border;
      ctx.lineWidth = 1;
      ctx.font = monoFont(11);
      ctx.fillStyle = c.text3;

      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      for (const t of ax.ticks) {
        const x = crisp(sx(t));
        ctx.beginPath();
        ctx.moveTo(x, PAD.top);
        ctx.lineTo(x, PAD.top + plotH);
        ctx.stroke();
        ctx.fillText(t.toFixed(1), x, PAD.top + plotH + 8);
      }

      ctx.textAlign = 'right';
      ctx.textBaseline = 'middle';
      for (const t of ay.ticks) {
        const y = crisp(sy(t));
        ctx.beginPath();
        ctx.moveTo(PAD.left, y);
        ctx.lineTo(PAD.left + plotW, y);
        ctx.stroke();
        ctx.fillText(t.toFixed(2), PAD.left - 8, y);
      }

      // ---- 等比例线 ----
      // 后端给的是两个端点。在 TDS-萃取率 平面上等粉液比是一条直线
      // （TDS = 萃取率 × 粉量 / 液重，粉液比固定时斜率固定），
      // 所以两点足够，不需要采样曲线。
      ctx.save();
      ctx.beginPath();
      ctx.rect(PAD.left, PAD.top, plotW, plotH);
      ctx.clip();

      for (const iso of data.iso_ratios) {
        ctx.beginPath();
        ctx.moveTo(sx(iso.x1), sy(iso.y1));
        ctx.lineTo(sx(iso.x2), sy(iso.y2));
        ctx.strokeStyle = iso.emphasize ? c.brand : withAlpha(c.text3, 0.55);
        ctx.lineWidth = iso.emphasize ? 1.6 : 1;
        ctx.setLineDash(iso.emphasize ? [] : [4, 4]);
        ctx.stroke();
        ctx.setLineDash([]);

        // 标签贴在线的右上端，超出画布时不画
        const lx = sx(iso.x2);
        const ly = sy(iso.y2);
        if (lx < PAD.left + plotW - 4 && ly > PAD.top + 10) {
          ctx.font = monoFont(10);
          ctx.fillStyle = iso.emphasize ? c.brand : c.text3;
          ctx.textAlign = 'right';
          ctx.textBaseline = 'bottom';
          ctx.fillText(iso.label, lx - 3, ly - 3);
        }
      }
      ctx.restore();

      // ---- 历史冲煮散点 ----
      ctx.save();
      ctx.beginPath();
      ctx.rect(PAD.left - 6, PAD.top - 6, plotW + 12, plotH + 12);
      ctx.clip();

      for (const p of data.points) {
        const x = sx(p.yield_percent);
        const y = sy(p.tds_percent);
        const col = p.in_gold_cup ? c.good : c.warn;
        const isHover = hover?.p.brew_id === p.brew_id;
        const r = isHover ? 7 : 5;

        if (p.advisory) {
          // 推算点画成空心虚线圆（DesignSpec §5.6）。
          // 实心圆会让用户以为这是测出来的数据。
          ctx.beginPath();
          ctx.setLineDash([2, 2]);
          ctx.arc(x, y, r, 0, Math.PI * 2);
          ctx.strokeStyle = c.info;
          ctx.lineWidth = 1.6;
          ctx.stroke();
          ctx.setLineDash([]);
        } else {
          ctx.beginPath();
          ctx.arc(x, y, r, 0, Math.PI * 2);
          ctx.fillStyle = col;
          ctx.fill();
          ctx.strokeStyle = c.bg;
          ctx.lineWidth = 1.5;
          ctx.stroke();
        }

        if (isHover) {
          ctx.beginPath();
          ctx.arc(x, y, r + 4, 0, Math.PI * 2);
          ctx.strokeStyle = withAlpha(p.advisory ? c.info : col, 0.5);
          ctx.lineWidth = 2;
          ctx.stroke();
        }
      }
      ctx.restore();

      // ---- 轴标题 ----
      ctx.fillStyle = c.text2;
      ctx.font = sansFont(12);
      ctx.textAlign = 'center';
      ctx.textBaseline = 'bottom';
      ctx.fillText(
        `${ax.label}${ax.unit ? ` (${ax.unit})` : ''}`,
        PAD.left + plotW / 2,
        H - 6,
      );

      ctx.save();
      ctx.translate(14, PAD.top + plotH / 2);
      ctx.rotate(-Math.PI / 2);
      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      ctx.fillText(`${ay.label}${ay.unit ? ` (${ay.unit})` : ''}`, 0, 0);
      ctx.restore();

      // 外框
      ctx.strokeStyle = c.borderStrong;
      ctx.lineWidth = 1;
      ctx.strokeRect(crisp(PAD.left), crisp(PAD.top), Math.round(plotW), Math.round(plotH));
    },
    [data, hover],
  );

  const { canvasRef, containerRef } = useCanvas(draw);

  const onMove = useCallback(
    (e: React.MouseEvent<HTMLCanvasElement>) => {
      const f = frame.current;
      if (!f || data.points.length === 0) return;
      const rect = e.currentTarget.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;

      let best: { p: ChartPoint; d: number } | null = null;
      for (const p of data.points) {
        const dx = f.sx(p.yield_percent) - mx;
        const dy = f.sy(p.tds_percent) - my;
        const d = dx * dx + dy * dy;
        if (!best || d < best.d) best = { p, d };
      }
      // 12px 命中半径：太大会在点稀疏时误吸附到很远的点
      if (best && best.d <= 144) {
        const found = best;
        setHover((prev) =>
          prev?.p.brew_id === found.p.brew_id && prev.x === mx && prev.y === my
            ? prev
            : { p: found.p, x: mx, y: my },
        );
      } else {
        setHover((prev) => (prev === null ? prev : null));
      }
    },
    [data.points],
  );

  const onClick = useCallback(() => {
    if (hover && onPointClick) onPointClick(hover.p);
  }, [hover, onPointClick]);

  const advisoryCount = useMemo(
    () => data.points.filter((p) => p.advisory).length,
    [data.points],
  );

  return (
    <div className="flex flex-col gap-2">
      <div ref={containerRef} className="relative w-full" style={{ height }}>
        <canvas
          data-testid="goldcup-canvas"
          ref={canvasRef}
          onMouseMove={onMove}
          onMouseLeave={() => setHover(null)}
          onClick={onClick}
          className={onPointClick && hover ? 'cursor-pointer' : 'cursor-crosshair'}
          role="img"
          aria-label={`${data.title}，共 ${data.points.length} 次冲煮记录`}
        />

        {/* 提示层用 HTML 而不是画在 canvas 里：文字排版、换行、
            中文字距交给 DOM 更可靠，也不必手算文本宽度去画背景框。 */}
        {hover && (
          <div
            className="absolute pointer-events-none z-10 px-3 py-2 rounded-[var(--r-md)] border shadow-[var(--sh-2)] bg-[var(--c-surface-2)] text-[12px] leading-relaxed whitespace-nowrap"
            style={{
              left: Math.min(hover.x + 12, 9999),
              top: hover.y + 12,
              borderColor: hover.p.advisory ? 'var(--c-info-line)' : 'var(--c-border-strong)',
              borderStyle: hover.p.advisory ? 'dashed' : 'solid',
              // 靠右时向左翻转，避免提示框被裁掉
              transform: 'translateX(0)',
            }}
          >
            <p className="text-[var(--c-text)] font-medium">{hover.p.label}</p>
            <p className="num text-[var(--c-text-2)]">
              萃取率 {hover.p.yield_percent.toFixed(2)}% · TDS{' '}
              {hover.p.tds_percent.toFixed(2)}%
            </p>
            <p className="num text-[var(--c-text-2)]">粉液比 {hover.p.brew_ratio_text}</p>
            <p style={{ color: hover.p.in_gold_cup ? 'var(--c-good)' : 'var(--c-warn)' }}>
              {hover.p.zone_label}
            </p>
            {hover.p.has_score && (
              <p className="num text-[var(--c-text-3)]">
                评分 {hover.p.total_score.toFixed(1)}
              </p>
            )}
            {hover.p.advisory && (
              <p className="text-[var(--c-info)]">推算值，非实测浓度</p>
            )}
          </div>
        )}
      </div>

      {/* 图例。颜色不作为唯一信息载体 —— 金杯格除了绿色还有文字说明。 */}
      <div className="flex items-center gap-4 flex-wrap text-[12px] text-[var(--c-text-3)] px-1">
        <span className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-full bg-[var(--c-good)]" />
          落在金杯区
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2.5 h-2.5 rounded-full bg-[var(--c-warn)]" />
          偏离金杯区
        </span>
        {advisoryCount > 0 && (
          <span className="flex items-center gap-1.5">
            <span className="w-2.5 h-2.5 rounded-full border border-dashed border-[var(--c-info)]" />
            推算浓度（{advisoryCount} 次未测 TDS）
          </span>
        )}
        <span className="flex items-center gap-1.5">
          <span className="w-4 border-t border-[var(--c-brand)]" />
          等粉液比线
        </span>
      </div>
    </div>
  );
}
