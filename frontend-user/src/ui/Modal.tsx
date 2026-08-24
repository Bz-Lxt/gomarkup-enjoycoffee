import type { ReactNode } from 'react';
import { useEffect, useRef } from 'react';
import { Button } from './Button';
import { cx } from '@/lib/cx';

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  width = 480,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
}) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);

    // 锁滚动，否则背景会在弹层里滚动，用户以为界面失灵了
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    // 把焦点移进弹层：键盘用户否则会停在背景的按钮上，
    // Tab 一路走出弹层却看不到焦点在哪。
    panelRef.current?.focus();

    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      role="dialog"
      aria-modal="true"
    >
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-[2px]"
        onClick={onClose}
        aria-hidden="true"
      />
      <div
        ref={panelRef}
        tabIndex={-1}
        style={{ maxWidth: width }}
        className={cx(
          'relative w-full bg-[var(--c-surface)] border border-[var(--c-border)]',
          'rounded-[var(--r-xl)] shadow-[var(--sh-2)] outline-none',
          'max-h-[calc(100vh-2rem)] flex flex-col',
        )}
      >
        <header className="px-5 py-4 border-b border-[var(--c-border)]">
          <h2 className="text-h2">{title}</h2>
        </header>
        <div className="px-5 py-4 overflow-y-auto flex-1">{children}</div>
        {footer && (
          <footer className="px-5 py-4 border-t border-[var(--c-border)] flex justify-end gap-2">
            {footer}
          </footer>
        )}
      </div>
    </div>
  );
}

/**
 * 破坏性操作的二次确认。
 *
 * impact 是必填而非可选：DesignSpec §8 要求确认弹层说明影响范围
 * （"将同时删除 12 个子节点"），而不是笼统的"确定吗"。
 * 把它做成必填参数，写代码时就绕不过去。
 */
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  impact,
  confirmText = '确认删除',
  loading = false,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  impact: ReactNode;
  confirmText?: string;
  loading?: boolean;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      width={420}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={loading}>
            取消
          </Button>
          <Button variant="danger" onClick={onConfirm} loading={loading}>
            {confirmText}
          </Button>
        </>
      }
    >
      <div
        data-testid="impact-scope"
        className="text-[14px] text-[var(--c-text-2)] leading-relaxed"
      >
        {impact}
      </div>
    </Modal>
  );
}
