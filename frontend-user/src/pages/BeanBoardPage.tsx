import { useMemo, useState } from 'react';
import { beansApi, flavorsApi } from '@/api/endpoints';
import type { BeanView, FilterResult, MatchMode } from '@/api/types';
import { useAsync } from '@/lib/useAsync';
import { useMeta } from '@/lib/MetaContext';
import { semantic } from '@/lib/semantic';
import { PageHeader } from '@/ui/AppShell';
import { Button } from '@/ui/Button';
import { Badge, Card, EmptyState, Skeleton } from '@/ui/Card';
import { ConfirmDialog } from '@/ui/Modal';
import { useToast } from '@/ui/Toast';
import { TextField } from '@/ui/Field';
import { BeanCard } from '@/components/BeanCard';
import { BeanForm, ConsumeDialog } from '@/components/BeanForm';
import { FlavorFilter } from '@/components/FlavorFilter';

export default function BeanBoardPage() {
  const toast = useToast();
  const meta = useMeta();

  const [q, setQ] = useState('');
  const [stage, setStage] = useState('');
  const [nodeIDs, setNodeIDs] = useState<number[]>([]);
  const [match, setMatch] = useState<MatchMode>('ALL');
  const [reloadKey, setReloadKey] = useState(0);

  const [editing, setEditing] = useState<BeanView | null>(null);
  const [creating, setCreating] = useState(false);
  const [consuming, setConsuming] = useState<BeanView | null>(null);
  const [deleting, setDeleting] = useState<BeanView | null>(null);
  const [deletingBusy, setDeletingBusy] = useState(false);

  const treeState = useAsync(() => flavorsApi.tree(), []);

  const listState = useAsync(
    (signal) =>
      beansApi.list(
        {
          keyword: q || undefined,
          stages: stage ? [stage] : undefined,
          flavor_ids: nodeIDs.length > 0 ? nodeIDs : undefined,
          flavor_match: match,
          page_size: 200,
        },
        signal,
      ),
    [q, stage, nodeIDs.join(','), match, reloadKey],
  );

  const reload = () => setReloadKey((k) => k + 1);

  const beans = listState.data?.items ?? [];
  const filterResult: FilterResult | null = listState.data?.flavor_filter ?? null;

  // 按新鲜度阶段分组。分组顺序取 /meta 里枚举的声明顺序 ——
  // 阶段的先后是业务语义（排气 → 最佳 → 临期 → 衰退），不是字母序，
  // 而这个语义顺序后端已经定好了。
  const grouped = useMemo(() => {
    const order = meta.freshness_stages.map((s) => s.value);
    const map = new Map<string, BeanView[]>();
    for (const b of beans) {
      const k = b.freshness.stage;
      const arr = map.get(k);
      if (arr) arr.push(b);
      else map.set(k, [b]);
    }
    return order
      .filter((k) => map.has(k))
      .map((k) => ({ stage: k, items: map.get(k)! }));
  }, [beans, meta.freshness_stages]);

  const doDelete = async () => {
    if (!deleting) return;
    setDeletingBusy(true);
    try {
      await beansApi.remove(deleting.id);
      toast.success(`「${deleting.name}」已删除`);
      setDeleting(null);
      reload();
    } catch (e) {
      toast.error(e);
    } finally {
      setDeletingBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        title="豆库看板"
        subtitle={
          listState.data
            ? `共 ${listState.data.total} 支豆`
            : '按新鲜度阶段分组，颜色即状态'
        }
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            + 入库新豆
          </Button>
        }
      />

      <div className="grid grid-cols-1 lg:grid-cols-[260px_1fr] gap-6 items-start">
        <div className="flex flex-col gap-4 lg:sticky lg:top-6">
          <Card title="搜索与筛选" padded>
            <div className="flex flex-col gap-4">
              <TextField
                placeholder="豆名、烘焙商、产地…"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />

              <div className="flex flex-wrap gap-1.5">
                <button
                  onClick={() => setStage('')}
                  className="cursor-pointer"
                  aria-pressed={stage === ''}
                >
                  <Badge
                    color={stage === '' ? 'var(--c-brand)' : 'var(--c-text-3)'}
                    bg={stage === '' ? 'var(--c-brand-dim)' : 'var(--c-surface-2)'}
                  >
                    全部
                  </Badge>
                </button>
                {/* 阶段枚举与配色都来自 /meta，不在前端硬编码 ——
                    后端已经定义了一遍，抄第二遍迟早会漂移。 */}
                {meta.freshness_stages.map((s) => {
                  const on = stage === s.value;
                  const c = semantic(s.color_hint);
                  return (
                    <button
                      key={s.value}
                      onClick={() => setStage(on ? '' : s.value)}
                      className="cursor-pointer"
                      aria-pressed={on}
                    >
                      <Badge
                        color={on ? c.fg : 'var(--c-text-3)'}
                        bg={on ? c.bg : 'var(--c-surface-2)'}
                      >
                        {s.label}
                      </Badge>
                    </button>
                  );
                })}
              </div>
            </div>
          </Card>

          <Card title="风味筛选" padded>
            {treeState.loading ? (
              <div className="flex flex-col gap-2">
                {Array.from({ length: 6 }).map((_, i) => (
                  <Skeleton key={i} h={24} />
                ))}
              </div>
            ) : treeState.data ? (
              <FlavorFilter
                tree={treeState.data.tree}
                selected={nodeIDs}
                onChange={setNodeIDs}
                match={match}
                onMatchChange={setMatch}
                result={filterResult}
              />
            ) : (
              <p className="text-[13px] text-[var(--c-text-3)]">风味树加载失败</p>
            )}
          </Card>
        </div>

        <div className="min-w-0">
          {listState.loading ? (
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              {Array.from({ length: 6 }).map((_, i) => (
                <Card key={i}>
                  <div className="flex flex-col gap-3">
                    <Skeleton h={20} w="60%" />
                    <Skeleton h={10} />
                    <Skeleton h={14} w="80%" />
                    <Skeleton h={14} w="70%" />
                  </div>
                </Card>
              ))}
            </div>
          ) : listState.error ? (
            <Card>
              <EmptyState
                title="加载失败"
                description={
                  listState.error instanceof Error
                    ? listState.error.message
                    : '未知错误'
                }
                action={<Button onClick={listState.reload}>重试</Button>}
              />
            </Card>
          ) : beans.length === 0 ? (
            <Card>
              <EmptyState
                title={
                  nodeIDs.length > 0 || q || stage
                    ? '没有符合条件的豆子'
                    : '豆库还是空的'
                }
                description={
                  nodeIDs.length > 0 || q || stage
                    ? '放宽筛选条件试试，或者换个关键词。'
                    : '先加一支豆，才能开始记录冲煮参数与风味。'
                }
                action={
                  nodeIDs.length > 0 || q || stage ? (
                    <Button
                      onClick={() => {
                        setQ('');
                        setStage('');
                        setNodeIDs([]);
                      }}
                    >
                      清空筛选
                    </Button>
                  ) : (
                    <Button variant="primary" onClick={() => setCreating(true)}>
                      先加一支豆
                    </Button>
                  )
                }
              />
            </Card>
          ) : (
            <div className="flex flex-col gap-8">
              {grouped.map((g) => {
                const first = g.items[0]!;
                const s = semantic(first.freshness.color_hint);
                return (
                  <section key={g.stage}>
                    <div className="flex items-center gap-2 mb-3">
                      <h2 className="text-h3" style={{ color: s.fg }}>
                        {first.freshness.stage_label}
                      </h2>
                      <span className="num text-[12px] text-[var(--c-text-3)]">
                        {g.items.length} 支
                      </span>
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                      {g.items.map((b) => (
                        <BeanCard
                          key={b.id}
                          bean={b}
                          onEdit={setEditing}
                          onConsume={setConsuming}
                          onDelete={setDeleting}
                        />
                      ))}
                    </div>
                  </section>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {/* key 让表单在切换目标豆时彻底重挂载，避免残留上一支豆的输入 */}
      {creating && treeState.data && (
        <BeanForm
          key="create"
          open
          bean={null}
          tree={treeState.data.tree}
          onClose={() => setCreating(false)}
          onSaved={reload}
        />
      )}
      {editing && treeState.data && (
        <BeanForm
          key={`edit-${editing.id}`}
          open
          bean={editing}
          tree={treeState.data.tree}
          onClose={() => setEditing(null)}
          onSaved={reload}
        />
      )}

      <ConsumeDialog
        bean={consuming}
        onClose={() => setConsuming(null)}
        onDone={reload}
      />

      <ConfirmDialog
        open={Boolean(deleting)}
        onClose={() => setDeleting(null)}
        onConfirm={doDelete}
        loading={deletingBusy}
        title={`删除「${deleting?.name ?? ''}」`}
        impact={
          <>
            <p>这支豆会被移出豆库。</p>
            {deleting && deleting.brew_count > 0 && (
              <p className="mt-2 text-[var(--c-warn)]">
                它的 {deleting.brew_count} 条冲煮记录与对应的风味评分会一并删除，
                金杯控制图上的这些数据点也会消失。
              </p>
            )}
            <p className="mt-2 text-[var(--c-text-3)]">此操作无法撤销。</p>
          </>
        }
      />
    </>
  );
}
