import { useCallback, useEffect, useRef, useState } from 'react';

export interface AsyncState<T> {
  data: T | null;
  loading: boolean;
  error: unknown;
  reload: () => void;
}

/**
 * 数据加载的最小封装。
 *
 * 不引 React Query：这个应用只有五个页面，需要的就是"加载 / 出错 / 重试"
 * 三个状态加一个手动刷新。引入一套缓存框架带来的心智负担超过它省下的代码。
 *
 * 关键行为是竞态防护：切换筛选条件时会连发多个请求，
 * 后发的请求可能先返回，若不丢弃过期响应，界面会显示上一次的筛选结果。
 */
export function useAsync<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: unknown[],
): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [nonce, setNonce] = useState(0);

  // 每次请求自增，只有最新一次的结果才允许写入 state
  const seq = useRef(0);

  useEffect(() => {
    const my = ++seq.current;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    fn(ctrl.signal)
      .then((res) => {
        if (my !== seq.current) return;
        setData(res);
        setLoading(false);
      })
      .catch((e) => {
        if (my !== seq.current) return;
        // 主动取消不是错误，不该渲染成红条
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(e);
        setLoading(false);
      });

    return () => ctrl.abort();
    // fn 每次渲染都是新引用，故意不进依赖；调用方通过 deps 声明真实依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  return { data, loading, error, reload };
}
