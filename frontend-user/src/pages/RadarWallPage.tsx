import { useMemo, useState } from 'react';
import { beansApi, scoresApi } from '@/api/endpoints';
import { useAsync } from '@/lib/useAsync';
import { layerColor } from '@/lib/semantic';
import { PageHeader } from '@/ui/AppShell';
import { Button } from '@/ui/Button';
import { Badge, Card, EmptyState, KV, Skeleton } from '@/ui/Card';
import { RadarChart } from '@/charts/RadarChart';
import { cx } from '@/lib/cx';

/** 叠加上限。超过 6 层半透明多边形人眼无法分辨，后端也会拒。 */
const MAX_LAYERS = 6;

export default function RadarWallPage() {
  const [picked, setPicked] = useState<number[]>([]);

  // page_size 顶到后端上限：这是个选择器，翻页会让用户找不到某支豆子
  const beansState = useAsync(
    (signal) => beansApi.list({ page_size: 200 }, signal),
    [],
  );
  const beans = beansState.data?.items ?? [];

  const wallState = useAsync(
    () => (picked.length > 0 ? scoresApi.wall(picked) : Promise.resolve(null)),
    [picked.join(',')],
  );

  const layers = useMemo(
    () =>
      (wallState.data?.layers ?? []).map((l, i) => ({
        key: l.bean_id,
        name: l.name,
        radar: l.radar,
        color: layerColor(i),
      })),
    [wallState.data],
  );

  const atLimit = picked.length >= MAX_LAYERS;

  const toggle = (id: number) => {
    setPicked((prev) =>
      prev.includes(id)
        ? prev.filter((x) => x !== id)
        : prev.length >= MAX_LAYERS
          ? prev
          : [...prev, id],
    );
  };

  return (
    <>
      <PageHeader
        title="风味雷达墙"
        subtitle={`最多同时对比 ${MAX_LAYERS} 支豆`}
        actions={
          picked.length > 0 && (
            <Button onClick={() => setPicked([])}>清空选择</Button>
          )
        }
      />

      <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-6 items-start">
        <Card title="选择要对比的豆子" className="lg:sticky lg:top-6">
          {beansState.loading ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} h={32} />
              ))}
            </div>
          ) : beans.length === 0 ? (
            <EmptyState
              title="豆库是空的"
              description="雷达墙对比的是豆子的风味评分，先去豆库加几支豆。"
            />
          ) : (
            <>
              {atLimit && (
                <p
                  data-testid="radar-limit-warning"
                  className="text-[12px] text-[var(--c-warn)] mb-2 leading-relaxed"
                >
                  已到 {MAX_LAYERS} 支上限。再叠加半透明图层就分辨不出哪条属于哪支豆了，
                  请先取消一支。
                </p>
              )}
              <ul className="flex flex-col gap-1 max-h-[520px] overflow-y-auto -mx-1 px-1">
                {beans.map((b) => {
                  const idx = picked.indexOf(b.id);
                  const on = idx >= 0;
                  const scored = (b.radar?.sample_count ?? 0) > 0;
                  return (
                    <li key={b.id}>
                      <button
                        data-testid="radar-bean-option"
                        onClick={() => toggle(b.id)}
                        disabled={!on && atLimit}
                        aria-pressed={on}
                        className={cx(
                          'w-full flex items-center gap-2 px-2 py-1.5 rounded-[var(--r-sm)]',
                          'text-left text-[13px] cursor-pointer transition-colors',
                          on
                            ? 'bg-[var(--c-surface-3)] text-[var(--c-text)]'
                            : 'text-[var(--c-text-2)] hover:bg-[var(--c-surface-2)]',
                          !on && atLimit && 'opacity-35 cursor-not-allowed',
                        )}
                      >
                        <span
                          className="w-3 h-3 rounded-sm shrink-0"
                          style={{
                            background: on ? layerColor(idx) : 'transparent',
                            border: `1.5px solid ${on ? layerColor(idx) : 'var(--c-border-strong)'}`,
                          }}
                        />
                        <span className="flex-1 truncate">{b.name}</span>
                        {/* 未评分的豆仍然可选：选进去会画成塌到中心的点，
                            这本身就是有用的信息（"这支豆还没打过分"）。 */}
                        {!scored && (
                          <span className="text-[11px] text-[var(--c-text-3)] shrink-0">
                            未评分
                          </span>
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </>
          )}
        </Card>

        <div className="min-w-0 flex flex-col gap-6">
          <Card title="六维叠加对比">
            {picked.length === 0 ? (
              <EmptyState
                title="还没选豆子"
                description="从左边挑 2–6 支豆，它们的六维风味会叠在同一张雷达图上对比。"
              />
            ) : wallState.loading ? (
              <Skeleton h={380} />
            ) : layers.length === 0 ? (
              <EmptyState
                title="这些豆还没有风味评分"
                description="雷达图的数据来自每次冲煮后的六维打分。去萃取沙盘记录一次冲煮，尝一口，打个分。"
              />
            ) : (
              <RadarChart layers={layers} size={420} />
            )}
          </Card>

          {layers.length > 0 && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {layers.map((l, i) => {
                const wall = wallState.data?.layers[i];
                return (
                  <Card key={l.key}>
                    <div className="flex items-center gap-2 mb-3">
                      <span
                        className="w-3 h-3 rounded-sm shrink-0"
                        style={{ background: l.color }}
                      />
                      <h3 className="text-h3 truncate">{l.name}</h3>
                    </div>
                    {wall && (
                      <p className="text-[12px] text-[var(--c-text-3)] mb-2">
                        {wall.origin || '产地未填'} · {wall.roast_level_label}
                      </p>
                    )}
                    <div className="flex flex-col">
                      {l.radar.axes.map((a) => (
                        <KV key={a.axis} k={a.label} v={a.value_text} mono />
                      ))}
                      <KV
                        k="总分"
                        v={`${l.radar.total_score.toFixed(1)} / ${(l.radar.max_score * 6).toFixed(0)}`}
                        mono
                      />
                    </div>
                    <div className="mt-3 flex items-center gap-2 flex-wrap">
                      <Badge color="var(--c-text-3)" bg="var(--c-surface-2)">
                        {l.radar.sample_count} 次评分
                      </Badge>
                      <Badge color="var(--c-text-3)" bg="var(--c-surface-2)">
                        {l.radar.weighting}
                      </Badge>
                    </div>
                    <p className="mt-2 text-[13px] text-[var(--c-text-2)] leading-relaxed">
                      {l.radar.balance}
                    </p>
                  </Card>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
