import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react';
import { useId } from 'react';
import { cx } from '@/lib/cx';

const CONTROL =
  'w-full h-[38px] px-3 bg-[var(--c-surface-2)] text-[var(--c-text)] ' +
  'border border-[var(--c-border)] rounded-[var(--r-sm)] ' +
  'placeholder:text-[var(--c-text-3)] transition-colors duration-[120ms] ' +
  'hover:border-[var(--c-border-strong)] ' +
  'focus:border-[var(--c-brand)] focus:outline-none ' +
  'disabled:opacity-50 disabled:cursor-not-allowed';

const ERROR_RING = 'border-[var(--c-bad)] hover:border-[var(--c-bad)]';

interface LabelShellProps {
  label?: string;
  hint?: string;
  error?: string;
  required?: boolean;
  htmlFor?: string;
  children: ReactNode;
}

function LabelShell({ label, hint, error, required, htmlFor, children }: LabelShellProps) {
  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label
          htmlFor={htmlFor}
          className="text-[13px] text-[var(--c-text-2)] font-medium"
        >
          {label}
          {required && <span className="text-[var(--c-bad)] ml-1">*</span>}
        </label>
      )}
      {children}
      {/* 字段错误显示在对应输入框下方，而不是汇总成一个 toast ——
          后端一次返回全部字段错误，用户需要知道是哪一格填错了。 */}
      {error ? (
        <p
          data-testid="field-error"
          role="alert"
          className="text-[13px] text-[var(--c-bad)] leading-snug"
        >
          {error}
        </p>
      ) : hint ? (
        <p className="text-[13px] text-[var(--c-text-3)] leading-snug">{hint}</p>
      ) : null}
    </div>
  );
}

export interface TextFieldProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label?: string;
  hint?: string;
  error?: string;
  suffix?: string;
}

export function TextField({
  label,
  hint,
  error,
  suffix,
  className,
  required,
  ...rest
}: TextFieldProps) {
  const id = useId();
  return (
    <LabelShell
      label={label}
      hint={hint}
      error={error}
      required={required}
      htmlFor={id}
    >
      <div className="relative">
        <input
          {...rest}
          id={id}
          required={required}
          aria-invalid={error ? true : undefined}
          className={cx(CONTROL, error && ERROR_RING, suffix && 'pr-10', className)}
        />
        {suffix && (
          <span
            aria-hidden="true"
            className="absolute right-3 top-1/2 -translate-y-1/2 text-[13px] text-[var(--c-text-3)] pointer-events-none"
          >
            {suffix}
          </span>
        )}
      </div>
    </LabelShell>
  );
}

/**
 * 数值输入。刻意用 type="text" + inputMode="decimal" 而非 type="number"。
 *
 * 三个理由（DesignSpec §5.2）：
 *   1. type="number" 在部分安卓上带出的键盘没有小数点
 *   2. 滚轮会在页面滚动时意外改值
 *   3. 它把值当数字处理，与本项目「数值走字符串」的契约冲突 ——
 *      浏览器可能把 "18.50" 规范化成 "18.5"，也可能在科学计数法上出现意外
 *
 * 值始终是字符串，原样发给后端，由后端的定点数解析做唯一裁定。
 * 前端不做 parseFloat，那一步会引入本项目全力避免的浮点误差。
 */
export interface NumberFieldProps extends Omit<TextFieldProps, 'type' | 'inputMode'> {
  /** 允许的小数位数，仅用于即时提示；最终裁定权在后端。 */
  decimals?: number;
}

export function NumberField({ decimals, hint, ...rest }: NumberFieldProps) {
  const decimalHint =
    decimals !== undefined && !hint ? `最多 ${decimals} 位小数` : hint;
  return (
    <TextField
      {...rest}
      type="text"
      inputMode="decimal"
      autoComplete="off"
      hint={decimalHint}
      className={cx('num', rest.className)}
    />
  );
}

export interface SelectFieldProps
  extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'id'> {
  label?: string;
  hint?: string;
  error?: string;
  options: { value: string; label: string }[];
  placeholder?: string;
}

export function SelectField({
  label,
  hint,
  error,
  options,
  placeholder,
  className,
  required,
  ...rest
}: SelectFieldProps) {
  const id = useId();
  return (
    <LabelShell
      label={label}
      hint={hint}
      error={error}
      required={required}
      htmlFor={id}
    >
      <select
        {...rest}
        id={id}
        required={required}
        aria-invalid={error ? true : undefined}
        className={cx(CONTROL, 'cursor-pointer', error && ERROR_RING, className)}
      >
        {placeholder && <option value="">{placeholder}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </LabelShell>
  );
}

export interface TextAreaFieldProps
  extends Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'id'> {
  label?: string;
  hint?: string;
  error?: string;
}

export function TextAreaField({
  label,
  hint,
  error,
  className,
  required,
  rows = 3,
  ...rest
}: TextAreaFieldProps) {
  const id = useId();
  return (
    <LabelShell
      label={label}
      hint={hint}
      error={error}
      required={required}
      htmlFor={id}
    >
      <textarea
        {...rest}
        id={id}
        rows={rows}
        required={required}
        aria-invalid={error ? true : undefined}
        className={cx(CONTROL, 'h-auto py-2 resize-y leading-relaxed', error && ERROR_RING, className)}
      />
    </LabelShell>
  );
}

/** 0.5 分步进的滑块，用于六维评分。值是 ×10 整数，与 API 契约一致。 */
export function ScoreSlider({
  label,
  valueX10,
  onChangeX10,
  color,
}: {
  label: string;
  valueX10: number;
  onChangeX10: (v: number) => void;
  color?: string;
}) {
  const id = useId();
  return (
    <div className="flex items-center gap-3">
      <label
        htmlFor={id}
        className="w-12 shrink-0 text-[13px] text-[var(--c-text-2)]"
      >
        {label}
      </label>
      <input
        id={id}
        type="range"
        min={0}
        max={100}
        // 步进 5 = 0.5 分。让"滑块能选到的值"和"后端能存的值"是同一个集合，
        // 不会出现 7.3 分这种滑块选不到、却能通过 API 塞进来的值。
        step={5}
        value={valueX10}
        onChange={(e) => onChangeX10(Number(e.target.value))}
        className="flex-1 accent-[var(--c-brand)] cursor-pointer"
        style={color ? { accentColor: color } : undefined}
        aria-valuetext={`${(valueX10 / 10).toFixed(1)} 分`}
      />
      <span className="num w-10 text-right text-[14px] text-[var(--c-text)]">
        {(valueX10 / 10).toFixed(1)}
      </span>
    </div>
  );
}
