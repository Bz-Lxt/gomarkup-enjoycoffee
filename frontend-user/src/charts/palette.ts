import { layerColor, resolveCssColor, semantic, withAlpha } from '@/lib/semantic';
import type { ColorHint } from '@/api/types';

/**
 * Canvas 的 fillStyle / strokeStyle 不解析 CSS 的 var()，
 * 必须先从计算样式里把 var(--c-good) 解析成 #4FA96B。
 *
 * 每次重绘都解析一遍是浪费（getComputedStyle 会触发样式计算），
 * 所以缓存住。tokens 是静态的，唯一会变的是主题切换 ——
 * 本项目只有深色一套主题，所以缓存不会失效。
 */
const cache = new Map<string, string>();

function resolve(varExpr: string): string {
  const hit = cache.get(varExpr);
  if (hit) return hit;
  const v = resolveCssColor(varExpr);
  cache.set(varExpr, v);
  return v;
}

export interface ChartPalette {
  bg: string;
  surface: string;
  surface2: string;
  border: string;
  borderStrong: string;
  text: string;
  text2: string;
  text3: string;
  brand: string;
  good: string;
  warn: string;
  bad: string;
  info: string;
}

export function chartPalette(): ChartPalette {
  return {
    bg: resolve('var(--c-bg)'),
    surface: resolve('var(--c-surface)'),
    surface2: resolve('var(--c-surface-2)'),
    border: resolve('var(--c-border)'),
    borderStrong: resolve('var(--c-border-strong)'),
    text: resolve('var(--c-text)'),
    text2: resolve('var(--c-text-2)'),
    text3: resolve('var(--c-text-3)'),
    brand: resolve('var(--c-brand)'),
    good: resolve('var(--c-good)'),
    warn: resolve('var(--c-warn)'),
    bad: resolve('var(--c-bad)'),
    info: resolve('var(--c-info)'),
  };
}

/** 后端 color_hint → 图表可用的实色。 */
export function hintColor(hint: ColorHint | string | undefined): string {
  return resolve(semantic(hint).fg);
}

/** 雷达墙第 i 层的实色。 */
export function layerHex(i: number): string {
  return resolve(layerColor(i));
}

export { withAlpha };

/** 画布上的等宽数字字体，与 .num 工具类保持一致。 */
export function monoFont(px: number, weight = 400): string {
  return `${weight} ${px}px "JetBrains Mono", "SF Mono", Menlo, ui-monospace, monospace`;
}

export function sansFont(px: number, weight = 400): string {
  return `${weight} ${px}px Inter, "PingFang SC", "Microsoft YaHei", system-ui, sans-serif`;
}
