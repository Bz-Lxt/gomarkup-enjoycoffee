import { useCallback, useEffect, useRef, useState } from 'react';

export interface Stopwatch {
  elapsedMs: number;
  running: boolean;
  start: () => void;
  stop: () => void;
  reset: () => void;
}

/**
 * 冲煮秒表。
 *
 * 计时基准是 performance.now() 的差值，不是累加 setInterval 的回调次数。
 * 累加法在标签页失焦时会被浏览器降频到每秒一次甚至暂停，
 * 手机锁屏再解锁后计时会少掉一大截 —— 而这恰好是"闷蒸时把手机放下"
 * 这个常见动作。差值法只在渲染时读一次当前时刻，不受回调频率影响。
 *
 * 刷新频率取 100ms 而不是 requestAnimationFrame 的 60fps：
 * 显示精度只到十分之一秒，每秒重渲染 60 次纯属浪费，
 * 而且会让整棵组件树跟着抖。
 */
export function useStopwatch(): Stopwatch {
  const [elapsedMs, setElapsed] = useState(0);
  const [running, setRunning] = useState(false);

  // 计时起点（performance.now() 坐标系），暂停时置 null
  const startedAt = useRef<number | null>(null);
  // 之前若干次运行段累计的时长，支持暂停后继续
  const accumulated = useRef(0);

  useEffect(() => {
    if (!running) return;
    const tick = () => {
      if (startedAt.current === null) return;
      setElapsed(accumulated.current + (performance.now() - startedAt.current));
    };
    tick();
    const id = window.setInterval(tick, 100);
    return () => window.clearInterval(id);
  }, [running]);

  const start = useCallback(() => {
    if (startedAt.current !== null) return;
    startedAt.current = performance.now();
    setRunning(true);
  }, []);

  const stop = useCallback(() => {
    if (startedAt.current === null) return;
    accumulated.current += performance.now() - startedAt.current;
    startedAt.current = null;
    setRunning(false);
    setElapsed(accumulated.current);
  }, []);

  const reset = useCallback(() => {
    startedAt.current = null;
    accumulated.current = 0;
    setRunning(false);
    setElapsed(0);
  }, []);

  return { elapsedMs, running, start, stop, reset };
}

/** 把毫秒格式成 m:ss.d。小数位固定，避免读数宽度跳动。 */
export function formatClock(ms: number): string {
  const total = Math.max(0, ms);
  const m = Math.floor(total / 60000);
  const s = Math.floor((total % 60000) / 1000);
  const d = Math.floor((total % 1000) / 100);
  return `${m}:${String(s).padStart(2, '0')}.${d}`;
}
