import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { cx } from '@/lib/cx';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';
type Size = 'sm' | 'md' | 'lg' | 'xl';

const VARIANT: Record<Variant, string> = {
  primary:
    'bg-[var(--c-brand)] text-[#1a1208] font-semibold hover:bg-[var(--c-brand-hover)]',
  secondary:
    'bg-[var(--c-surface-2)] text-[var(--c-text)] border border-[var(--c-border)] ' +
    'hover:bg-[var(--c-surface-3)] hover:border-[var(--c-border-strong)]',
  ghost: 'bg-transparent text-[var(--c-text-2)] hover:bg-[var(--c-surface-2)] hover:text-[var(--c-text)]',
  danger:
    'bg-transparent text-[var(--c-bad)] border border-[var(--c-bad-line)] ' +
    'hover:bg-[var(--c-bad)] hover:text-white hover:border-[var(--c-bad)]',
};

const SIZE: Record<Size, string> = {
  sm: 'h-[30px] px-3 text-[13px] rounded-[var(--r-sm)] gap-1.5',
  md: 'h-[38px] px-4 text-[14px] rounded-[var(--r-md)] gap-2',
  lg: 'h-[46px] px-5 text-[15px] rounded-[var(--r-md)] gap-2',
  // xl 只给萃取沙盘的打点按钮用：用户在盯着水流、只用余光瞥屏幕时
  // 也要能准确按到（DesignSpec §1）。
  xl: 'h-[72px] px-6 text-[19px] font-semibold rounded-[var(--r-lg)] gap-3 w-full',
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
  block?: boolean;
}

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  icon,
  block = false,
  disabled,
  className,
  children,
  ...rest
}: ButtonProps) {
  const isDisabled = disabled || loading;

  return (
    <button
      {...rest}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={cx(
        'relative inline-flex items-center justify-center whitespace-nowrap',
        'transition-colors duration-[120ms] cursor-pointer select-none',
        'active:translate-y-[1px]',
        'disabled:opacity-40 disabled:cursor-not-allowed disabled:active:translate-y-0',
        VARIANT[variant],
        SIZE[size],
        block && 'w-full',
        className,
      )}
    >
      {/* loading 时保持按钮宽度不变：换成 spinner 会让按钮点击后缩一下，
          用户会以为自己点错了（DesignSpec §5.1）。所以内容留在原位、
          降低不透明度，spinner 绝对定位叠在上面。 */}
      <span
        className={cx(
          'inline-flex items-center justify-center gap-[inherit]',
          loading && 'opacity-0',
        )}
      >
        {icon}
        {children}
      </span>
      {loading && (
        <span className="absolute inset-0 grid place-items-center">
          <Spinner />
        </span>
      )}
    </button>
  );
}

export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        border: '2px solid currentColor',
        borderTopColor: 'transparent',
        borderRadius: '50%',
        display: 'inline-block',
        animation: 'spin 700ms linear infinite',
      }}
    />
  );
}

/** 图标按钮必须有 aria-label（DesignSpec §8），所以类型上强制它。 */
export function IconButton({
  label,
  icon,
  className,
  ...rest
}: Omit<ButtonProps, 'children'> & { label: string }) {
  return (
    <Button
      {...rest}
      aria-label={label}
      title={label}
      className={cx('!px-0 aspect-square', className)}
    >
      {icon}
    </Button>
  );
}
