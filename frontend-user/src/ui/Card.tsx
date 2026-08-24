import type { ReactNode } from 'react';
import { cx } from '@/lib/cx';

export function Card({
  title,
  subtitle,
  actions,
  children,
  className,
  padded = true,
  /** 推算态：虚线边框 + 蓝色底（DesignSpec §5.6） */
  advisory = false,
  testID,
}: {
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  padded?: boolean;
  advisory?: boolean;
  testID?: string;
}) {
  return (
    <section
      data-testid={testID}
      className={cx(
        'bg-[var(--c-surface)] rounded-[var(--r-lg)]',
        advisory
          ? 'border border-dashed border-[var(--c-info-line)]'
          : 'border border-[var(--c-border)]',
        className,
      )}
    >
      {(title || actions) && (
        <header
          className={cx(
            'flex items-start justify-between gap-4',
            padded ? 'px-5 pt-5' : 'px-5 py-4',
            !padded && 'border-b border-[var(--c-border)]',
          )}
        >
          <div className="min-w-0">
            {title && <h2 className="text-h2 text-[var(--c-text)] truncate">{title}</h2>}
            {subtitle && (
              <p className="text-[13px] text-[var(--c-text-3)] mt-0.5">{subtitle}</p>
            )}
          </div>
          {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
        </header>
      )}
      <div className={cx(padded && 'p-5', padded && (title || actions) && 'pt-4')}>
        {children}
      </div>
    </section>
  );
}

/** 药丸形状态徽章。语义色的 -dim 底 + 同色文字。 */
export function Badge({
  children,
  color = 'var(--c-text-2)',
  bg = 'var(--c-surface-3)',
  dashed = false,
  className,
  testID,
}: {
  children: ReactNode;
  color?: string;
  bg?: string;
  dashed?: boolean;
  className?: string;
  testID?: string;
}) {
  return (
    <span
      data-testid={testID}
      className={cx(
        'inline-flex items-center gap-1 px-2 py-0.5 rounded-[var(--r-full)]',
        'text-[12px] leading-[1.5] font-medium whitespace-nowrap',
        dashed && 'border border-dashed',
        className,
      )}
      style={{ color, background: bg, borderColor: dashed ? color : undefined }}
    >
      {children}
    </span>
  );
}

/**
 * 推算角标。任何 advisory:true 的数据展示都要挂上它。
 *
 * 让用户误以为推算值是实测值，是这个工具能犯的最严重的错误 ——
 * 他会照着一组推测出来的参数反复冲（裁定 C-01 / DesignSpec §5.6）。
 */
export function AdvisoryTag({ className }: { className?: string }) {
  return (
    <Badge
      color="var(--c-info)"
      bg="var(--c-info-dim)"
      dashed
      className={className}
      testID="advisory-tag"
    >
      推算
    </Badge>
  );
}

/**
 * 空态。必须给出下一步动作，而不是一句"暂无数据"（DesignSpec §5.7）。
 */
export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center text-center py-12 px-6 gap-3">
      {icon && <div className="text-[var(--c-text-3)] mb-1">{icon}</div>}
      <p className="text-h3 text-[var(--c-text)]">{title}</p>
      {description && (
        <p className="text-[13px] text-[var(--c-text-3)] max-w-sm leading-relaxed">
          {description}
        </p>
      )}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

/** 骨架屏。保持最终布局的形状，避免数据到达时页面整体跳动。 */
export function Skeleton({
  className,
  h = 16,
  w,
}: {
  className?: string;
  h?: number | string;
  w?: number | string;
}) {
  return (
    <div
      className={cx('skeleton', className)}
      style={{ height: h, width: w ?? '100%' }}
      aria-hidden="true"
    />
  );
}

/** 键值行，详情页里成对信息的统一排版。 */
export function KV({
  k,
  v,
  mono = false,
}: {
  k: ReactNode;
  v: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5 border-b border-[var(--c-border)] last:border-0">
      <span className="text-[13px] text-[var(--c-text-3)] shrink-0">{k}</span>
      <span
        className={cx('text-[14px] text-[var(--c-text)] text-right', mono && 'num')}
      >
        {v}
      </span>
    </div>
  );
}

/**
 * 大号数值读数。
 *
 * 强制 .num（等宽 + tabular-nums）：变宽的数字在连续变化时会左右跳动，
 * 盯着看会晕。这是秒表类界面最常见的失误（DesignSpec §3.2）。
 * value 直接传后端的 *_text，不要在前端 toFixed —— 小数位必须固定。
 */
export function Readout({
  label,
  value,
  unit,
  color,
  size = 'display',
  advisory = false,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  color?: string;
  size?: 'display' | 'h1' | 'h2';
  advisory?: boolean;
}) {
  const sizeCls =
    size === 'display' ? 'text-display' : size === 'h1' ? 'text-h1' : 'text-h2';
  return (
    <div className="flex flex-col gap-1 min-w-0">
      <div className="flex items-center gap-1.5">
        <span className="text-[12px] text-[var(--c-text-3)] uppercase tracking-wide">
          {label}
        </span>
        {advisory && <AdvisoryTag />}
      </div>
      <div className="flex items-baseline gap-1">
        <span
          className={cx('num font-semibold', sizeCls)}
          style={{ color: color ?? 'var(--c-text)' }}
        >
          {value}
        </span>
        {unit && (
          <span className="text-[14px] text-[var(--c-text-3)]">{unit}</span>
        )}
      </div>
    </div>
  );
}
