import type { ReactNode } from 'react';
import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { cx } from '@/lib/cx';

type ToastKind = 'success' | 'error' | 'warn' | 'info';

interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
  detail?: string;
}

interface ToastApi {
  push: (kind: ToastKind, message: string, detail?: string) => void;
  success: (message: string) => void;
  warn: (message: string) => void;
  info: (message: string) => void;
  /**
   * 展示一个 ApiError。
   *
   * 注意：字段级校验错误不该只走 toast —— 用户需要知道是哪一格填错了。
   * 表单应该先取 err.fieldMap() 铺到输入框下方，
   * 只把非字段错误（NOT_FOUND / CONFLICT / 网络）交给这里。
   * 若确实有字段错误落到这里，会把字段名一并显示，至少不至于完全无从下手。
   */
  error: (err: unknown, fallback?: string) => void;
}

const ToastCtx = createContext<ToastApi | null>(null);

const STYLE: Record<ToastKind, { fg: string; bg: string; border: string }> = {
  success: { fg: 'var(--c-good)', bg: 'var(--c-good-dim)', border: 'var(--c-good-line)' },
  error: { fg: 'var(--c-bad)', bg: 'var(--c-bad-dim)', border: 'var(--c-bad-line)' },
  warn: { fg: 'var(--c-warn)', bg: 'var(--c-warn-dim)', border: 'var(--c-warn-line)' },
  info: { fg: 'var(--c-info)', bg: 'var(--c-info-dim)', border: 'var(--c-info-line)' },
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);

  const remove = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (kind: ToastKind, message: string, detail?: string) => {
      const id = ++seq.current;
      setToasts((prev) => [...prev, { id, kind, message, detail }]);
      // 错误停留更久：用户需要时间读完，而成功提示读一眼就够
      window.setTimeout(() => remove(id), kind === 'error' ? 7000 : 3500);
    },
    [remove],
  );

  const api = useMemo<ToastApi>(
    () => ({
      push,
      success: (m) => push('success', m),
      warn: (m) => push('warn', m),
      info: (m) => push('info', m),
      error: (err, fallback = '操作失败') => {
        if (err instanceof ApiError) {
          const fieldNote =
            err.fields.length > 0
              ? err.fields.map((f) => `${f.field}: ${f.reason}`).join('；')
              : undefined;
          push('error', err.message, fieldNote);
        } else if (err instanceof Error) {
          push('error', err.message);
        } else {
          push('error', fallback);
        }
      },
    }),
    [push],
  );

  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div
        className="fixed z-[100] bottom-4 right-4 flex flex-col gap-2 w-[min(380px,calc(100vw-2rem))]"
        role="status"
        aria-live="polite"
      >
        {toasts.map((t) => {
          const s = STYLE[t.kind];
          return (
            <div
              key={t.id}
              onClick={() => remove(t.id)}
              className={cx(
                'px-4 py-3 rounded-[var(--r-md)] border cursor-pointer',
                'shadow-[var(--sh-2)] backdrop-blur-sm',
              )}
              style={{
                background: `color-mix(in srgb, ${s.bg} 100%, var(--c-surface) 60%)`,
                borderColor: s.border,
              }}
            >
              <p className="text-[14px] font-medium" style={{ color: s.fg }}>
                {t.message}
              </p>
              {t.detail && (
                <p className="text-[13px] text-[var(--c-text-2)] mt-1 leading-snug">
                  {t.detail}
                </p>
              )}
            </div>
          );
        })}
      </div>
    </ToastCtx.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error('useToast 必须在 ToastProvider 内使用');
  return ctx;
}
