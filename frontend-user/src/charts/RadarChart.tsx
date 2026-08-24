import { useCallback, useMemo, useState } from 'react';
import type { RadarSummary } from '@/api/types';
import { chartPalette, layerHex, monoFont, sansFont, withAlpha } from './palette';
import { useCanvas } from './useCanvas';

export interface RadarLayerInput {
  key: string | number;
  name: string;
  radar: RadarSummary;
  /** 缺省按索引取六色之一 */
  color?: string;
}

/**
 * 六维风味雷达。
 *
 * 支持 1–6 层半透明叠加。6 是硬上限：七层以上人眼已分不出哪条属于哪支豆，
 * 图表就从对比工具退化成装饰。后端会拒绝第 7 支，这里只是不去突破它。
 *
 * 轴的顺序、标签、满分都来自后端的 RadarSummary —— 满分下发的意义是
 * 将来若改成百分制，前端不需要改（DesignSpec §7.4）。
 */
export function RadarChart({
  layers,
  size = 320,
  showLegend = true,
}: {
  layers: RadarLayerInput[];
  size?: number;
  showLegend?: boolean;
}) {
  const [hidden, setHidden] = useState<Set<string | number>>(new Set());

  const visible = useMemo(
    () => layers.filter((l) => !hidden.has(l.key)),
    [layers, hidden],
  );

  // 轴定义取第一层的。所有层的轴顺序由后端保证一致
  // （domain.FlavorAxes() 是固定顺序），所以不必求交集。
  const axes = layers[0]?.radar.axes ?? [];
  const maxScore = layers[0]?.radar.max_score ?? 10;

  const draw = useCallback(
    (ctx: CanvasRenderingContext2D, s: { width: number; height: number }) => {
      const c = chartPalette();
      const n = axes.length;
      if (n < 3) return;

      const cx = s.width / 2;
      const cy = s.height / 2 + 4;
      // 留出标签的空间：标签画在多边形外侧，半径要收一些
      const R = Math.min(s.width, s.height) / 2 - 38;
      if (R <= 10) return;

      // 顶点朝上：-90° 起算
      const angle = (i: number) => (Math.PI * 2 * i) / n - Math.PI / 2;
      const pt = (i: number, r: number): [number, number] => [
        cx + Math.cos(angle(i)) * r,
        cy + Math.sin(angle(i)) * r,
      ];

      // ---- 网格：5 圈，对应 2/4/6/8/10 分 ----
      const rings = 5;
      for (let ring = 1; ring <= rings; ring++) {
        const r = (R * ring) / rings;
        ctx.beginPath();
        for (let i = 0; i < n; i++) {
          const [x, y] = pt(i, r);
          if (i === 0) ctx.moveTo(x, y);
          else ctx.lineTo(x, y);
        }
        ctx.closePath();
        ctx.strokeStyle = ring === rings ? c.borderStrong : c.border;
        ctx.lineWidth = 1;
        ctx.stroke();
      }

      // ---- 轴线 ----
      ctx.strokeStyle = c.border;
      for (let i = 0; i < n; i++) {
        const [x, y] = pt(i, R);
        ctx.beginPath();
        ctx.moveTo(cx, cy);
        ctx.lineTo(x, y);
        ctx.stroke();
      }

      // ---- 刻度数字（只标最外圈与中圈，多了会糊）----
      ctx.font = monoFont(9);
      ctx.fillStyle = c.text3;
      ctx.textAlign = 'right';
      ctx.textBaseline = 'middle';
      for (const ring of [rings, Math.ceil(rings / 2)]) {
        const r = (R * ring) / rings;
        ctx.fillText(String((maxScore * ring) / rings), cx - 4, cy - r);
      }

      // ---- 数据层 ----
      for (let li = 0; li < layers.length; li++) {
        const layer = layers[li]!;
        if (hidden.has(layer.key)) continue;

        const col = layer.color ?? layerHex(li);
        const vals = layer.radar.axes;

        ctx.beginPath();
        for (let i = 0; i < n; i++) {
          const v = vals[i]?.value ?? 0;
          const r = (R * Math.max(0, Math.min(v, maxScore))) / maxScore;
          const [x, y] = pt(i, r);
          if (i === 0) ctx.moveTo(x, y);
          else ctx.lineTo(x, y);
        }
        ctx.closePath();

        // 18% 填充：DesignSpec §2.4 的值，六层叠加后仍可分辨
        ctx.fillStyle = withAlpha(col, 0.18);
        ctx.fill();
        ctx.strokeStyle = col;
        ctx.lineWidth = 2;
        ctx.stroke();

        // 顶点小圆点。单层时才画 —— 多层叠加时这些点会连成一片噪点。
        if (visible.length === 1) {
          for (let i = 0; i < n; i++) {
            const v = vals[i]?.value ?? 0;
            const r = (R * Math.max(0, Math.min(v, maxScore))) / maxScore;
            const [x, y] = pt(i, r);
            ctx.beginPath();
            ctx.arc(x, y, 3, 0, Math.PI * 2);
            ctx.fillStyle = col;
            ctx.fill();
          }
        }
      }

      // ---- 轴标签 ----
      ctx.font = sansFont(12, 500);
      for (let i = 0; i < n; i++) {
        const [x, y] = pt(i, R + 18);
        const a = angle(i);
        // 按方位决定对齐方式，否则左右两侧的标签会压到图形上
        ctx.textAlign = Math.abs(Math.cos(a)) < 0.3 ? 'center' : Math.cos(a) > 0 ? 'left' : 'right';
        ctx.textBaseline = Math.abs(Math.sin(a)) < 0.3 ? 'middle' : Math.sin(a) > 0 ? 'top' : 'bottom';
        ctx.fillStyle = c.text2;
        ctx.fillText(axes[i]?.label ?? '', x, y);
      }
    },
    [axes, layers, hidden, visible.length, maxScore],
  );

  const { canvasRef, containerRef } = useCanvas(draw);

  const toggle = (key: string | number) => {
    setHidden((prev) => {
      const next = new Set(prev);
      // 不允许全部隐藏：空图表看起来像坏了
      if (next.has(key)) next.delete(key);
      else if (visible.length > 1) next.add(key);
      return next;
    });
  };

  return (
    <div className="flex flex-col items-center gap-3">
      <div ref={containerRef} style={{ width: '100%', height: size }}>
        <canvas
          data-testid="radar-canvas"
          ref={canvasRef}
          role="img"
          aria-label={`六维风味雷达，${layers.length} 组数据`}
        />
      </div>

      {showLegend && layers.length > 0 && (
        <ul className="flex items-center justify-center gap-x-4 gap-y-1.5 flex-wrap px-2">
          {layers.map((l, i) => {
            const col = l.color ?? layerHex(i);
            const off = hidden.has(l.key);
            const unscored = l.radar.sample_count === 0;
            return (
              <li key={l.key}>
                <button
                  onClick={() => toggle(l.key)}
                  aria-pressed={!off}
                  className="flex items-center gap-1.5 text-[12px] cursor-pointer transition-opacity"
                  style={{ opacity: off ? 0.35 : 1 }}
                >
                  <span
                    className="w-3 h-3 rounded-sm shrink-0"
                    style={{
                      background: off ? 'transparent' : withAlpha(col, 0.35),
                      border: `1.5px solid ${col}`,
                    }}
                  />
                  <span className={off ? 'line-through text-[var(--c-text-3)]' : 'text-[var(--c-text-2)]'}>
                    {l.name}
                  </span>
                  {/* 未评分的豆仍出现在图例里，只是标注出来 ——
                      从图例里消失会让用户以为自己没选中它。 */}
                  {unscored && (
                    <span className="text-[11px] text-[var(--c-text-3)]">未评分</span>
                  )}
                  {!unscored && (
                    <span className="num text-[11px] text-[var(--c-text-3)]">
                      {l.radar.total_score.toFixed(1)}
                    </span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
