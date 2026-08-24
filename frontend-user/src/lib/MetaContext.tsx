import type { ReactNode } from 'react';
import { createContext, useContext } from 'react';
import { metaApi } from '@/api/endpoints';
import type { MetaResponse } from '@/api/types';
import { useAsync } from './useAsync';
import { Button } from '@/ui/Button';

/**
 * /meta 的枚举在全站共享。
 *
 * 为什么不在前端硬编码这些枚举：烘焙度、注水手法、生命周期窗口这些值
 * 后端已经定义了一遍，硬编码就是定义第二遍。两份定义迟早会漂移，
 * 而漂移的表现是下拉框里选了个后端不认的值，报一个莫名的校验错。
 */
const MetaCtx = createContext<MetaResponse | null>(null);

export function MetaProvider({ children }: { children: ReactNode }) {
  const { data, loading, error, reload } = useAsync<MetaResponse>(
    (signal) => metaApi.load(signal),
    [],
  );

  if (loading) {
    return (
      <div className="min-h-screen grid place-items-center">
        <div className="flex flex-col items-center gap-3 text-[var(--c-text-3)]">
          <div
            className="w-7 h-7 rounded-full border-2 border-[var(--c-border-strong)] border-t-[var(--c-brand)]"
            style={{ animation: 'spin 700ms linear infinite' }}
          />
          <p className="text-[13px]">正在连接服务…</p>
        </div>
      </div>
    );
  }

  // meta 拿不到就没法渲染任何表单，这里必须硬失败而不是降级 ——
  // 用空枚举渲染出来的下拉框是个骗人的空壳。
  if (error || !data) {
    return (
      <div className="min-h-screen grid place-items-center px-6">
        <div className="max-w-md text-center flex flex-col gap-4">
          <h1 className="text-h1">连不上后端</h1>
          <p className="text-[14px] text-[var(--c-text-2)] leading-relaxed">
            界面需要先从 <code className="num">/api/v1/meta</code>{' '}
            读取烘焙度、注水手法等枚举才能渲染表单。 请确认后端容器已启动（
            <code className="num">docker compose up -d</code>）。
          </p>
          <div className="flex justify-center">
            <Button variant="primary" onClick={reload}>
              重试
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return <MetaCtx.Provider value={data}>{children}</MetaCtx.Provider>;
}

export function useMeta(): MetaResponse {
  const ctx = useContext(MetaCtx);
  if (!ctx) throw new Error('useMeta 必须在 MetaProvider 内使用');
  return ctx;
}
