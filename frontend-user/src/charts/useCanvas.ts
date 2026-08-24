import { useCallback, useEffect, useRef, useState } from 'react';

export interface CanvasSize {
  /** CSS 像素尺寸，绘图代码只跟这套坐标打交道 */
  width: number;
  height: number;
  dpr: number;
}

/**
 * Canvas 的 DPR 与尺寸管理。
 *
 * 这里封装的是手写 Canvas 最常出错的两件事：
 *
 * 其一，devicePixelRatio。canvas 的 width/height **属性**是位图像素数，
 * CSS 的 width/height 是显示尺寸。两者相等时，在 dpr=2 的屏幕上
 * 每个位图像素被拉成 2×2 显示像素，所有线条都是模糊的。
 * 正确做法是属性设为 CSS 尺寸 × dpr，再 ctx.scale(dpr, dpr)，
 * 之后绘图代码继续用 CSS 坐标书写。
 *
 * 其二，用 ResizeObserver 而不是 window.resize。侧栏折叠时
 * window 尺寸没变，但画布容器变了 —— 监听 window 会漏掉这种情况。
 */
export function useCanvas(
  draw: (ctx: CanvasRenderingContext2D, size: CanvasSize) => void,
): {
  canvasRef: React.RefObject<HTMLCanvasElement>;
  containerRef: React.RefObject<HTMLDivElement>;
  size: CanvasSize;
  redraw: () => void;
} {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<CanvasSize>({ width: 0, height: 0, dpr: 1 });

  // draw 每次渲染都是新函数；放进 ref 让重绘用最新的闭包，
  // 又不会因为它变化而重建 ResizeObserver。
  const drawRef = useRef(draw);
  drawRef.current = draw;

  const paint = useCallback((s: CanvasSize) => {
    const canvas = canvasRef.current;
    if (!canvas || s.width <= 0 || s.height <= 0) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    canvas.width = Math.round(s.width * s.dpr);
    canvas.height = Math.round(s.height * s.dpr);
    canvas.style.width = `${s.width}px`;
    canvas.style.height = `${s.height}px`;

    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.scale(s.dpr, s.dpr);

    drawRef.current(ctx, s);
  }, []);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;

    const measure = () => {
      const rect = el.getBoundingClientRect();
      const next: CanvasSize = {
        width: Math.max(0, Math.floor(rect.width)),
        height: Math.max(0, Math.floor(rect.height)),
        dpr: window.devicePixelRatio || 1,
      };
      setSize((prev) =>
        prev.width === next.width && prev.height === next.height && prev.dpr === next.dpr
          ? prev
          : next,
      );
    };

    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // 尺寸或数据变化后重绘。draw 变化也要重绘，所以它进依赖 ——
  // 但用的是 drawRef.current，避免闭包过期。
  useEffect(() => {
    paint(size);
  }, [size, paint, draw]);

  const redraw = useCallback(() => paint(size), [paint, size]);

  return { canvasRef, containerRef, size, redraw };
}

/** 线性映射：数据坐标 → 像素坐标。 */
export function makeScale(
  domainMin: number,
  domainMax: number,
  rangeMin: number,
  rangeMax: number,
): (v: number) => number {
  const span = domainMax - domainMin;
  // 退化区间（后端给了 min==max）时固定映射到区间中点，
  // 否则会算出 Infinity 并把整张图画飞。
  if (span === 0) {
    const mid = (rangeMin + rangeMax) / 2;
    return () => mid;
  }
  const k = (rangeMax - rangeMin) / span;
  return (v: number) => rangeMin + (v - domainMin) * k;
}

/**
 * 描边用的半像素对齐。
 *
 * Canvas 的线宽以路径为中心向两侧扩展，1px 的线画在整数坐标上会
 * 跨越两个像素各占一半，渲染成 2px 宽的灰线。偏移 0.5 让它落在
 * 一个像素内，得到真正锐利的 1px。
 */
export function crisp(v: number): number {
  return Math.round(v) + 0.5;
}
