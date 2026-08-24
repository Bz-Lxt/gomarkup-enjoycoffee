import type { ReactNode } from 'react';
import { useState } from 'react';
import { NavLink } from 'react-router-dom';
import { cx } from '@/lib/cx';

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
}

const Icon = ({ d }: { d: string }) => (
  <svg
    viewBox="0 0 24 24"
    width="20"
    height="20"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    className="shrink-0"
  >
    <path d={d} />
  </svg>
);

const NAV: NavItem[] = [
  {
    to: '/',
    label: '豆库看板',
    icon: <Icon d="M4 7h16M4 12h16M4 17h10M4 4v16" />,
  },
  {
    to: '/brew',
    label: '萃取沙盘',
    icon: <Icon d="M6 8h11a3 3 0 0 1 0 6h-1v1a4 4 0 0 1-4 4H10a4 4 0 0 1-4-4V8zM9 5V3M13 5V3" />,
  },
  {
    to: '/radar',
    label: '风味雷达墙',
    icon: <Icon d="M12 3l8 5v8l-8 5-8-5V8l8-5zM12 8l4 2.5v4L12 17l-4-2.5v-4L12 8z" />,
  },
  {
    to: '/flavors',
    label: '风味树',
    icon: <Icon d="M6 4h5M6 4v16M6 12h5M6 20h5M14 2v4h6V2h-6zM14 10v4h6v-4h-6zM14 18v4h6v-4h-6z" />,
  },
  {
    to: '/settings',
    label: '设置',
    icon: <Icon d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2 2 2 0 1 1-4 0 1.7 1.7 0 0 0-2.9-1.2l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.7 1.7 0 0 0 3 15a2 2 0 1 1 0-4 1.7 1.7 0 0 0 1.4-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.7 1.7 0 0 0 10 4a2 2 0 1 1 4 0 1.7 1.7 0 0 0 2.9 1.4l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1A1.7 1.7 0 0 0 21 11a2 2 0 1 1 0 4h-1.6z" />,
  },
];

export function AppShell({ children }: { children: ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <div className="min-h-screen flex flex-col lg:flex-row">
      {/* 侧栏：lg 以下收成底部标签栏。萃取沙盘在手机上使用是主场景之一 ——
          厨房里不会摆一台笔记本（DesignSpec §6）。 */}
      <aside
        style={{ width: collapsed ? 'var(--nav-w-collapsed)' : 'var(--nav-w)' }}
        className={cx(
          'hidden lg:flex flex-col shrink-0 border-r border-[var(--c-border)]',
          'bg-[var(--c-surface)] transition-[width] duration-200 sticky top-0 h-screen',
        )}
      >
        <div className="h-16 flex items-center gap-2.5 px-4 border-b border-[var(--c-border)]">
          <svg viewBox="0 0 32 32" width="26" height="26" aria-hidden="true">
            <path
              d="M8 11h13a4 4 0 0 1 0 8h-1v1a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4v-9z"
              fill="none"
              stroke="var(--c-brand)"
              strokeWidth="2"
              strokeLinejoin="round"
            />
            <path
              d="M12 8c0-1 1-1.5 1-2.5M16 8c0-1 1-1.5 1-2.5"
              fill="none"
              stroke="var(--c-text-2)"
              strokeWidth="1.5"
              strokeLinecap="round"
            />
          </svg>
          {!collapsed && (
            <span className="text-[15px] font-semibold tracking-tight whitespace-nowrap">
              Mini 咖啡大师
            </span>
          )}
        </div>

        <nav className="flex-1 p-2 flex flex-col gap-1">
          {NAV.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.to === '/'}
              title={collapsed ? it.label : undefined}
              className={({ isActive }) =>
                cx(
                  'flex items-center gap-3 h-10 px-3 rounded-[var(--r-md)]',
                  'text-[14px] transition-colors duration-[120ms]',
                  collapsed && 'justify-center px-0',
                  isActive
                    ? 'bg-[var(--c-brand-dim)] text-[var(--c-brand)] font-medium'
                    : 'text-[var(--c-text-2)] hover:bg-[var(--c-surface-2)] hover:text-[var(--c-text)]',
                )
              }
            >
              {it.icon}
              {!collapsed && <span className="whitespace-nowrap">{it.label}</span>}
            </NavLink>
          ))}
        </nav>

        <button
          onClick={() => setCollapsed((v) => !v)}
          aria-label={collapsed ? '展开侧栏' : '折叠侧栏'}
          className="h-11 border-t border-[var(--c-border)] text-[var(--c-text-3)] hover:text-[var(--c-text)] hover:bg-[var(--c-surface-2)] transition-colors cursor-pointer"
        >
          {collapsed ? '»' : '«'}
        </button>
      </aside>

      <main className="flex-1 min-w-0 pb-20 lg:pb-0">
        <div
          style={{ maxWidth: 'var(--content-max)' }}
          className="mx-auto px-4 py-6 lg:px-8 lg:py-8"
        >
          {children}
        </div>
      </main>

      {/* 移动端底部标签栏 */}
      <nav className="lg:hidden fixed bottom-0 left-0 right-0 z-40 flex border-t border-[var(--c-border)] bg-[var(--c-surface)] pb-[env(safe-area-inset-bottom)]">
        {NAV.map((it) => (
          <NavLink
            key={it.to}
            to={it.to}
            end={it.to === '/'}
            className={({ isActive }) =>
              cx(
                'flex-1 flex flex-col items-center justify-center gap-0.5 py-2',
                'text-[11px] transition-colors',
                isActive ? 'text-[var(--c-brand)]' : 'text-[var(--c-text-3)]',
              )
            }
          >
            {it.icon}
            <span>{it.label}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  );
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex items-start justify-between gap-4 mb-6 flex-wrap">
      <div className="min-w-0">
        <h1 className="text-h1">{title}</h1>
        {subtitle && (
          <p className="text-[14px] text-[var(--c-text-3)] mt-1">{subtitle}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
    </header>
  );
}
