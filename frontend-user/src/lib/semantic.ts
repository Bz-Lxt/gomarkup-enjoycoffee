import type { ColorHint } from '@/api/types';

/**
 * 后端的 color_hint 到 CSS 变量的唯一映射表。
 *
 * 前端不做二次判断 —— 「萃取率 21% 算不算过萃」这类判断在后端，
 * 因为它依赖用户可配置的金杯标准。前端若自己算一遍，
 * 用户改了设置后两边就会给出不同的颜色。
 */
export interface SemanticColor {
  /** 文字与描边色 */
  fg: string;
  /** 区块底色（同色 14% 透明） */
  bg: string;
  /** 图表描边（同色 55% 透明） */
  line: string;
}

const HINTS: Record<ColorHint, SemanticColor> = {
  green: { fg: 'var(--c-good)', bg: 'var(--c-good-dim)', line: 'var(--c-good-line)' },
  amber: { fg: 'var(--c-warn)', bg: 'var(--c-warn-dim)', line: 'var(--c-warn-line)' },
  red: { fg: 'var(--c-bad)', bg: 'var(--c-bad-dim)', line: 'var(--c-bad-line)' },
  blue: { fg: 'var(--c-info)', bg: 'var(--c-info-dim)', line: 'var(--c-info-line)' },
  neutral: {
    fg: 'var(--c-text-2)',
    bg: 'var(--c-surface-3)',
    line: 'var(--c-border-strong)',
  },
};

export function semantic(hint: ColorHint | string | undefined): SemanticColor {
  if (hint && hint in HINTS) return HINTS[hint as ColorHint];
  return HINTS.neutral;
}

/** 雷达墙的六色，超过 6 层时回绕（后端会先拒，这里只是兜底）。 */
const LAYER_COLORS = [
  'var(--c-layer-1)',
  'var(--c-layer-2)',
  'var(--c-layer-3)',
  'var(--c-layer-4)',
  'var(--c-layer-5)',
  'var(--c-layer-6)',
] as const;

export function layerColor(index: number): string {
  return LAYER_COLORS[index % LAYER_COLORS.length]!;
}

/**
 * 图表里需要真实色值而不是 CSS 变量：Canvas 的 fillStyle 不解析 var()。
 * 从计算样式里读出来，这样改 tokens.css 图表颜色会跟着变。
 */
export function resolveCssColor(varExpr: string, el: Element = document.body): string {
  const name = varExpr.trim().match(/^var\((--[\w-]+)\)$/)?.[1];
  if (!name) return varExpr;
  const v = getComputedStyle(el).getPropertyValue(name).trim();
  return v || varExpr;
}

/** 给 Canvas 用的半透明版本。tokens 里的 -dim 是 14%，图表填充要 18%。 */
export function withAlpha(hexOrRgb: string, alpha: number): string {
  const hex = hexOrRgb.trim();
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (m) {
    const n = parseInt(m[1]!, 16);
    return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
  }
  const rgb = /^rgba?\(([^)]+)\)$/.exec(hex);
  if (rgb) {
    const parts = rgb[1]!.split(',').map((s) => s.trim());
    return `rgba(${parts[0]}, ${parts[1]}, ${parts[2]}, ${alpha})`;
  }
  return hex;
}
