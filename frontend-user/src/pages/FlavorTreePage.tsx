import { useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import { flavorsApi } from '@/api/endpoints';
import type { FlavorTreeNode, MatchMode } from '@/api/types';
import { useAsync } from '@/lib/useAsync';
import { cx } from '@/lib/cx';
import { PageHeader } from '@/ui/AppShell';
import { Button } from '@/ui/Button';
import { Badge, Card, EmptyState, KV, Skeleton } from '@/ui/Card';
import { SelectField, TextField } from '@/ui/Field';
import { ConfirmDialog, Modal } from '@/ui/Modal';
import { useToast } from '@/ui/Toast';
import { FlavorFilter } from '@/components/FlavorFilter';

function flatten(
  nodes: FlavorTreeNode[],
  depth = 0,
): { node: FlavorTreeNode; depth: number }[] {
  const out: { node: FlavorTreeNode; depth: number }[] = [];
  for (const n of nodes) {
    out.push({ node: n, depth });
    out.push(...flatten(n.children, depth + 1));
  }
  return out;
}

export default function FlavorTreePage() {
  const toast = useToast();
  const [reloadKey, setReloadKey] = useState(0);
  const treeState = useAsync(() => flavorsApi.tree(), [reloadKey]);
  const reload = () => setReloadKey((k) => k + 1);

  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [editing, setEditing] = useState<FlavorTreeNode | null>(null);
  const [addingUnder, setAddingUnder] = useState<FlavorTreeNode | null | 'root'>(null);
  const [moving, setMoving] = useState<FlavorTreeNode | null>(null);
  const [deleting, setDeleting] = useState<FlavorTreeNode | null>(null);
  const [deleteCascade, setDeleteCascade] = useState(true);
  const [busy, setBusy] = useState(false);

  // 筛选面板独立于管理面板：一边整理分类，一边看这个分类下有哪些豆
  const [nodeIDs, setNodeIDs] = useState<number[]>([]);
  const [match, setMatch] = useState<MatchMode>('ALL');
  const filterState = useAsync(
    (signal) =>
      nodeIDs.length > 0
        ? flavorsApi.filter({ flavor_ids: nodeIDs, match, facets: true }, signal)
        : Promise.resolve(null),
    [nodeIDs.join(','), match],
  );

  const tree = treeState.data?.tree ?? [];
  const stats = treeState.data?.stats;
  const flat = useMemo(() => flatten(tree), [tree]);

  const doDelete = async () => {
    if (!deleting) return;
    setBusy(true);
    try {
      const res = await flavorsApi.deleteNode(
        deleting.id,
        deleteCascade ? 'CASCADE' : 'PROMOTE',
      );
      toast.success(res.message);
      setDeleting(null);
      reload();
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  const renderNode = (node: FlavorTreeNode, depth: number) => {
    const hasKids = node.children.length > 0;
    const open = expanded.has(node.id);
    return (
      <li key={node.id}>
        <div
          data-testid="tree-node"
          className="group flex items-center gap-1 py-1 rounded-[var(--r-sm)] hover:bg-[var(--c-surface-2)]"
          style={{ paddingLeft: depth * 16 + 4 }}
        >
          {hasKids ? (
            <button
              data-testid="tree-toggle"
              onClick={() =>
                setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(node.id)) next.delete(node.id);
                  else next.add(node.id);
                  return next;
                })
              }
              aria-label={open ? `折叠 ${node.name}` : `展开 ${node.name}`}
              className="w-5 h-5 shrink-0 grid place-items-center text-[var(--c-text-3)] hover:text-[var(--c-text)] cursor-pointer text-[10px]"
            >
              {open ? '▾' : '▸'}
            </button>
          ) : (
            <span className="w-5 shrink-0" />
          )}

          <span
            className="w-2.5 h-2.5 rounded-full shrink-0"
            style={{ background: node.color || 'var(--c-border-strong)' }}
          />
          <span className="text-[13px] text-[var(--c-text)] truncate">{node.name}</span>

          {node.builtin && (
            <Badge color="var(--c-text-3)" bg="var(--c-surface-3)">
              内置
            </Badge>
          )}

          <span className="num text-[11px] text-[var(--c-text-3)] ml-1">
            {node.aggregate_bean_count}
          </span>

          {/* 操作按钮 hover 才显现：常驻会让树看起来像一张表格 */}
          <span className="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity pr-1">
            <Button size="sm" variant="ghost" onClick={() => setAddingUnder(node)}>
              + 子类
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(node)}>
              改名
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setMoving(node)}>
              移动
            </Button>
            <Button
              size="sm"
              variant="ghost"
              data-testid="tree-delete"
              className="!text-[var(--c-bad)]"
              onClick={() => {
                setDeleting(node);
                setDeleteCascade(true);
              }}
            >
              删除
            </Button>
          </span>
        </div>
        {hasKids && open && <ul>{node.children.map((k) => renderNode(k, depth + 1))}</ul>}
      </li>
    );
  };

  return (
    <>
      <PageHeader
        title="风味树"
        subtitle="无限级分类。层级越深，筛选越精确"
        actions={
          <Button variant="primary" onClick={() => setAddingUnder('root')}>
            + 新增一级分类
          </Button>
        }
      />

      {stats?.depth_warning && (
        <div className="mb-4 p-3 rounded-[var(--r-md)] bg-[var(--c-warn-dim)] border border-[var(--c-warn-line)]">
          <p className="text-[13px] text-[var(--c-warn)] leading-relaxed">
            {stats.depth_warning}
          </p>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_300px] gap-6 items-start">
        <Card title="分类管理" padded>
          {treeState.loading ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 10 }).map((_, i) => (
                <Skeleton key={i} h={26} />
              ))}
            </div>
          ) : tree.length === 0 ? (
            <EmptyState
              title="还没有风味分类"
              description="风味树是给豆子打标签的分类体系。可以从「果调」「花香」「坚果」这样的一级分类开始。"
              action={
                <Button variant="primary" onClick={() => setAddingUnder('root')}>
                  新增一级分类
                </Button>
              }
            />
          ) : (
            <ul className="flex flex-col">{tree.map((n) => renderNode(n, 0))}</ul>
          )}
        </Card>

        <div className="flex flex-col gap-4 lg:sticky lg:top-6">
          <Card title="试筛选" padded>
            {treeState.data ? (
              <FlavorFilter
                tree={tree}
                selected={nodeIDs}
                onChange={setNodeIDs}
                match={match}
                onMatchChange={setMatch}
                result={filterState.data ?? null}
              />
            ) : null}
          </Card>

          {stats && (
            <Card title="索引状态" padded>
              <div className="flex flex-col">
                <KV k="节点数" v={stats.node_count} mono />
                <KV k="层数" v={stats.depth_levels} mono />
                <KV k="一级分类" v={stats.root_count} mono />
                <KV k="已标记豆子" v={stats.bean_count} mono />
                <KV k="位图内存" v={`${stats.approx_memory_kb} KB`} mono />
                <KV k="构建于" v={stats.built_at} mono />
              </div>
              <p className="text-[12px] text-[var(--c-text-3)] mt-3 leading-relaxed">
                每个节点预存一份「本节点及全部后代所标记豆子」的位图。
                筛选是几次位运算，耗时与树深无关 —— 这是「无限级」能保持
                10ms 以内响应的原因。
              </p>
            </Card>
          )}
        </div>
      </div>

      {addingUnder !== null && (
        <NodeForm
          parent={addingUnder === 'root' ? null : addingUnder}
          onClose={() => setAddingUnder(null)}
          onSaved={() => {
            reload();
            if (addingUnder !== 'root' && addingUnder) {
              setExpanded((p) => new Set(p).add(addingUnder.id));
            }
          }}
        />
      )}

      {editing && (
        <RenameForm
          node={editing}
          onClose={() => setEditing(null)}
          onSaved={reload}
        />
      )}

      {moving && (
        <MoveForm
          node={moving}
          candidates={flat}
          onClose={() => setMoving(null)}
          onSaved={reload}
        />
      )}

      <ConfirmDialog
        open={Boolean(deleting)}
        onClose={() => setDeleting(null)}
        onConfirm={doDelete}
        loading={busy}
        confirmText={deleteCascade ? '连子类一起删' : '只删这一个'}
        title={`删除「${deleting?.name ?? ''}」`}
        impact={
          deleting && (
            <div className="flex flex-col gap-3">
              {/* 影响范围要说具体数字，不是笼统的"确定吗"（DesignSpec §8） */}
              {deleting.descendant_count > 0 ? (
                <>
                  <p className="text-[var(--c-warn)]">
                    这个分类下还有 {deleting.descendant_count} 个子分类。
                  </p>
                  <div className="flex flex-col gap-2">
                    <label className="flex items-start gap-2 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={deleteCascade}
                        onChange={(e) => setDeleteCascade(e.target.checked)}
                        className="mt-1 accent-[var(--c-bad)] cursor-pointer"
                      />
                      <span className="text-[13px]">
                        连同 {deleting.descendant_count} 个子分类一起删除。
                        <br />
                        <span className="text-[var(--c-text-3)]">
                          不勾选则子分类会被提升到上一级，不会丢失。
                        </span>
                      </span>
                    </label>
                  </div>
                </>
              ) : (
                <p>这是一个叶子分类，没有子分类。</p>
              )}

              {deleting.direct_bean_count > 0 && (
                <p className="text-[var(--c-warn)]">
                  有 {deleting.direct_bean_count} 支豆标记了这个风味，
                  删除后这些标记会被解除（豆子本身不受影响）。
                </p>
              )}
            </div>
          )
        }
      />
    </>
  );
}

// ---------------------------------------------------------------- 表单

function NodeForm({
  parent,
  onClose,
  onSaved,
}: {
  parent: FlavorTreeNode | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState('');
  const [color, setColor] = useState('#C98A3E');
  const [err, setErr] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setErr({});
    try {
      await flavorsApi.createNode({ parent_id: parent?.id ?? null, name, color });
      toast.success(`「${name}」已创建`);
      onSaved();
      onClose();
    } catch (e) {
      if (e instanceof ApiError && e.fields.length > 0) setErr(e.fieldMap());
      else toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      width={400}
      title={parent ? `在「${parent.name}」下新增子类` : '新增一级分类'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" onClick={submit} loading={busy} disabled={!name.trim()}>
            创建
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <TextField
          label="分类名"
          required
          autoFocus
          value={name}
          error={err.name}
          placeholder="如：柠檬"
          onChange={(e) => setName(e.target.value)}
        />
        <ColorPicker value={color} onChange={setColor} error={err.color} />
      </div>
    </Modal>
  );
}

function RenameForm({
  node,
  onClose,
  onSaved,
}: {
  node: FlavorTreeNode;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState(node.name);
  const [color, setColor] = useState(node.color || '#C98A3E');
  const [err, setErr] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setErr({});
    try {
      await flavorsApi.updateNode(node.id, { name, color });
      toast.success('已保存');
      onSaved();
      onClose();
    } catch (e) {
      if (e instanceof ApiError && e.fields.length > 0) setErr(e.fieldMap());
      else toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      width={400}
      title={`编辑「${node.name}」`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" onClick={submit} loading={busy}>
            保存
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <TextField
          label="分类名"
          required
          autoFocus
          value={name}
          error={err.name}
          onChange={(e) => setName(e.target.value)}
        />
        <ColorPicker value={color} onChange={setColor} error={err.color} />
        <p className="text-[12px] text-[var(--c-text-3)]">
          路径：{node.path}
        </p>
      </div>
    </Modal>
  );
}

function MoveForm({
  node,
  candidates,
  onClose,
  onSaved,
}: {
  node: FlavorTreeNode;
  candidates: { node: FlavorTreeNode; depth: number }[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [target, setTarget] = useState<string>('');
  const [busy, setBusy] = useState(false);

  // 不能移到自己或自己的后代下 —— 那会形成环。
  // 后端也会拒，但在下拉里就排除掉比让用户点了才报错要好。
  const descendantIDs = useMemo(() => {
    const ids = new Set<number>();
    const walk = (n: FlavorTreeNode) => {
      ids.add(n.id);
      n.children.forEach(walk);
    };
    walk(node);
    return ids;
  }, [node]);

  const options = candidates
    .filter((c) => !descendantIDs.has(c.node.id))
    .map((c) => ({
      value: String(c.node.id),
      label: `${'　'.repeat(c.depth)}${c.node.name}`,
    }));

  const submit = async () => {
    setBusy(true);
    try {
      await flavorsApi.moveNode(node.id, {
        parent_id: target ? Number(target) : null,
        to_root: !target,
      });
      toast.success(`「${node.name}」已移动`);
      onSaved();
      onClose();
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      width={420}
      title={`移动「${node.name}」`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" onClick={submit} loading={busy}>
            移动
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <SelectField
          label="移动到"
          value={target}
          placeholder="提升为一级分类"
          options={options}
          onChange={(e) => setTarget(e.target.value)}
          hint="自己与自己的子分类已从列表中排除，避免形成环"
        />
        {node.descendant_count > 0 && (
          <p className="text-[13px] text-[var(--c-text-2)]">
            它的 {node.descendant_count} 个子分类会跟着一起移动。
          </p>
        )}
      </div>
    </Modal>
  );
}

const PRESET_COLORS = [
  '#C98A3E',
  '#4A8FB5',
  '#4FA96B',
  '#B5628F',
  '#C4543C',
  '#8B8FC9',
  '#D99A2B',
  '#7D6E5F',
];

function ColorPicker({
  value,
  onChange,
  error,
}: {
  value: string;
  onChange: (v: string) => void;
  error?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[13px] text-[var(--c-text-2)] font-medium">标记颜色</span>
      <div className="flex gap-2 flex-wrap">
        {PRESET_COLORS.map((c) => (
          <button
            key={c}
            onClick={() => onChange(c)}
            aria-label={`选择颜色 ${c}`}
            aria-pressed={value.toUpperCase() === c}
            className={cx(
              'w-7 h-7 rounded-[var(--r-sm)] cursor-pointer transition-transform',
              value.toUpperCase() === c
                ? 'ring-2 ring-offset-2 ring-offset-[var(--c-surface)] ring-white scale-105'
                : 'hover:scale-105',
            )}
            style={{ background: c }}
          />
        ))}
      </div>
      {error && <p className="text-[13px] text-[var(--c-bad)]">{error}</p>}
    </div>
  );
}
