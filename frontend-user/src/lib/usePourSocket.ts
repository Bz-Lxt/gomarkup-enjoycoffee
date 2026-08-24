import { useCallback, useEffect, useRef, useState } from 'react';
import { WS_BASE } from '@/api/client';
import type { PourCurve, PourSourceMode, WsInbound, WsOutbound } from '@/api/types';

export type SocketStatus = 'connecting' | 'open' | 'closed' | 'error';

export interface PourSocket {
  status: SocketStatus;
  curve: PourCurve | null;
  mode: PourSourceMode | null;
  simRunning: boolean;
  /** 服务端时刻减本地时刻，用于把秒表对齐到服务端 */
  clockSkewMs: number;
  mark: (offsetMs: number, cumulativeG: string, technique: string) => void;
  startSim: () => void;
  stopSim: () => void;
  lastError: string | null;
}

/**
 * 注水实时通道。
 *
 * 两个关键设计：
 *
 * **幂等键。** 每个打点带一个客户端生成的 key。断线重连后待发队列里的点
 * 会重推，服务端靠 key 去重。没有它就只有两个选择：重推（可能重复计数）
 * 或不重推（丢点）。冲煮过程中丢一个点，那份数据就永远补不回来了。
 *
 * **待发队列。** 连接断开时打点不报错也不丢弃，先入队，重连后补发。
 * 用户在厨房里按下按钮的那一刻，不该因为 WiFi 抖了一下就白按。
 */
export function usePourSocket(brewID: number | null): PourSocket {
  const [status, setStatus] = useState<SocketStatus>('closed');
  const [curve, setCurve] = useState<PourCurve | null>(null);
  const [mode, setMode] = useState<PourSourceMode | null>(null);
  const [simRunning, setSimRunning] = useState(false);
  const [clockSkewMs, setSkew] = useState(0);
  const [lastError, setLastError] = useState<string | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const pending = useRef<WsOutbound[]>([]);
  const retry = useRef(0);
  const retryTimer = useRef<number | null>(null);
  const closedByUs = useRef(false);
  const keySeq = useRef(0);

  const flush = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const queue = pending.current;
    pending.current = [];
    for (const msg of queue) ws.send(JSON.stringify(msg));
  }, []);

  const send = useCallback(
    (msg: WsOutbound) => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify(msg));
      } else {
        // 排队而不是丢弃或报错。重连后由 flush 补发，
        // 服务端按 key 去重，重复补发是安全的。
        pending.current.push(msg);
      }
    },
    [],
  );

  useEffect(() => {
    if (brewID === null) {
      setStatus('closed');
      return;
    }

    closedByUs.current = false;
    let disposed = false;

    const connect = () => {
      if (disposed) return;
      setStatus('connecting');

      const ws = new WebSocket(`${WS_BASE}/api/v1/ws/brews/${brewID}/pour`);
      wsRef.current = ws;

      ws.onopen = () => {
        if (disposed) return;
        retry.current = 0;
        setStatus('open');
        setLastError(null);
        // 先要一次全量曲线：断线期间可能有别的设备（或模拟器）打了点。
        ws.send(JSON.stringify({ type: 'sync' } satisfies WsOutbound));
        flush();
      };

      ws.onmessage = (ev) => {
        if (disposed) return;
        let msg: WsInbound;
        try {
          msg = JSON.parse(ev.data as string) as WsInbound;
        } catch {
          return;
        }

        switch (msg.type) {
          case 'hello':
            if (msg.mode) setMode(msg.mode);
            if (msg.server_time_ms) setSkew(msg.server_time_ms - Date.now());
            if (typeof msg.sim_running === 'boolean') setSimRunning(msg.sim_running);
            break;
          case 'curve':
            if (msg.curve) setCurve(msg.curve);
            break;
          case 'sim_state':
            setSimRunning(Boolean(msg.sim_running));
            break;
          case 'error':
            setLastError(msg.message ?? '实时通道返回了一个错误');
            break;
          case 'pong':
            break;
        }
      };

      ws.onerror = () => {
        if (disposed) return;
        setStatus('error');
      };

      ws.onclose = () => {
        if (disposed || closedByUs.current) return;
        setStatus('closed');
        // 指数退避但封顶 8 秒：冲煮全程只有几分钟，
        // 退避到几十秒等于放弃了这次冲煮的后半段。
        const delay = Math.min(8000, 500 * 2 ** retry.current);
        retry.current += 1;
        retryTimer.current = window.setTimeout(connect, delay);
      };
    };

    connect();

    // 心跳。服务端有读超时，静默太久会被踢掉；
    // 而冲煮过程中用户可能几十秒不打点（闷蒸），必须靠 ping 撑住连接。
    const heartbeat = window.setInterval(() => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'ping' } satisfies WsOutbound));
      }
    }, 20000);

    return () => {
      disposed = true;
      closedByUs.current = true;
      window.clearInterval(heartbeat);
      if (retryTimer.current !== null) window.clearTimeout(retryTimer.current);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [brewID, flush]);

  const mark = useCallback(
    (offsetMs: number, cumulativeG: string, technique: string) => {
      keySeq.current += 1;
      send({
        type: 'mark',
        offset_ms: offsetMs,
        cumulative_g: cumulativeG,
        technique,
        // 键里带 brewID 与序号：同一次冲煮内唯一即可，
        // 不需要全局唯一，所以不必引 uuid 依赖。
        key: `c${brewID}-${keySeq.current}-${offsetMs}`,
      });
    },
    [send, brewID],
  );

  const startSim = useCallback(() => send({ type: 'sim_start' }), [send]);
  const stopSim = useCallback(() => send({ type: 'sim_stop' }), [send]);

  return {
    status,
    curve,
    mode,
    simRunning,
    clockSkewMs,
    mark,
    startSim,
    stopSim,
    lastError,
  };
}
