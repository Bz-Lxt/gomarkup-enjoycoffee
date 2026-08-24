import { useMemo, useState } from 'react';
import { ApiError } from '@/api/client';
import { beansApi } from '@/api/endpoints';
import type { BeanPayload, BeanView, FlavorTreeNode } from '@/api/types';
import { useMeta } from '@/lib/MetaContext';
import { Button } from '@/ui/Button';
import { NumberField, SelectField, TextAreaField, TextField } from '@/ui/Field';
import { Modal } from '@/ui/Modal';
import { useToast } from '@/ui/Toast';
import { Badge } from '@/ui/Card';

/** 扁平化风味树，用于多选。层级用缩进表达。 */
function flatten(nodes: FlavorTreeNode[], depth = 0): { node: FlavorTreeNode; depth: number }[] {
  const out: { node: FlavorTreeNode; depth: number }[] = [];
  for (const n of nodes) {
    out.push({ node: n, depth });
    out.push(...flatten(n.children, depth + 1));
  }
  return out;
}

const EMPTY: BeanPayload = {
  name: '',
  roaster: '',
  is_blend: false,
  country: '',
  region: '',
  farm: '',
  altitude_m: 0,
  process: '',
  variety: '',
  roast_level: 'MEDIUM',
  roast_note: '',
  roasted_on: '',
  opened_on: '',
  initial_weight_g: '',
  remaining_g: '',
  notes: '',
  archived: false,
  flavor_node_ids: [],
};

function toPayload(b: BeanView): BeanPayload {
  return {
    name: b.name,
    roaster: b.roaster,
    is_blend: b.is_blend,
    country: b.country,
    region: b.region,
    farm: b.farm,
    altitude_m: b.altitude_m,
    process: b.process,
    variety: b.variety,
    roast_level: b.roast_level,
    roast_note: b.roast_note,
    roasted_on: b.roasted_on,
    opened_on: b.opened_on,
    // 数值回填用后端的数字转字符串。remaining_text 带单位，不能直接用。
    initial_weight_g: String(b.initial_weight_g),
    remaining_g: String(b.remaining_g),
    notes: b.notes,
    archived: b.archived,
    flavor_node_ids: b.flavors.map((f) => f.node_id),
  };
}

export function BeanForm({
  open,
  bean,
  tree,
  onClose,
  onSaved,
}: {
  open: boolean;
  /** null 表示新建 */
  bean: BeanView | null;
  tree: FlavorTreeNode[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const meta = useMeta();
  const toast = useToast();

  const [form, setForm] = useState<BeanPayload>(() =>
    bean ? toPayload(bean) : EMPTY,
  );
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [flavorOpen, setFlavorOpen] = useState(false);

  // bean 变化时重置表单。用 key 强制重挂载比在 effect 里同步更可靠，
  // 所以这里只在挂载时初始化，由调用方给 Modal 传 key。
  const flat = useMemo(() => flatten(tree), [tree]);
  const selectedNames = useMemo(() => {
    const ids = new Set(form.flavor_node_ids);
    return flat.filter((f) => ids.has(f.node.id)).map((f) => f.node);
  }, [flat, form.flavor_node_ids]);

  const set = <K extends keyof BeanPayload>(k: K, v: BeanPayload[K]) => {
    setForm((prev) => ({ ...prev, [k]: v }));
    // 用户开始改这一格就清掉它的错误提示，不然改完了红字还挂着
    setFieldErrors((prev) => {
      if (!(k in prev)) return prev;
      const next = { ...prev };
      delete next[k as string];
      return next;
    });
  };

  const submit = async () => {
    setSaving(true);
    setFieldErrors({});
    try {
      if (bean) await beansApi.update(bean.id, form);
      else await beansApi.create(form);
      toast.success(bean ? '已保存' : '豆子已入库');
      onSaved();
      onClose();
    } catch (e) {
      // 字段错误铺到对应输入框下方；只有非字段错误才走 toast。
      // 后端会一次返回全部字段错误，用户能一轮改完。
      if (e instanceof ApiError && e.fields.length > 0) {
        setFieldErrors(e.fieldMap());
        toast.warn(`有 ${e.fields.length} 处需要修改`);
      } else {
        toast.error(e);
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={bean ? `编辑「${bean.name}」` : '新增咖啡豆'}
      width={620}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>
            取消
          </Button>
          <Button variant="primary" onClick={submit} loading={saving}>
            {bean ? '保存' : '入库'}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <TextField
            label="豆名"
            required
            value={form.name}
            error={fieldErrors.name}
            placeholder="如：耶加雪菲 科契尔"
            onChange={(e) => set('name', e.target.value)}
          />
          <TextField
            label="烘焙商"
            value={form.roaster}
            error={fieldErrors.roaster}
            onChange={(e) => set('roaster', e.target.value)}
          />
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <TextField
            label="国家"
            value={form.country}
            error={fieldErrors.country}
            placeholder="埃塞俄比亚"
            onChange={(e) => set('country', e.target.value)}
          />
          <TextField
            label="产区"
            value={form.region}
            error={fieldErrors.region}
            onChange={(e) => set('region', e.target.value)}
          />
          <TextField
            label="庄园 / 处理厂"
            value={form.farm}
            error={fieldErrors.farm}
            onChange={(e) => set('farm', e.target.value)}
          />
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <SelectField
            label="处理法"
            value={form.process}
            error={fieldErrors.process}
            placeholder="不指定"
            options={meta.process_methods.map((p) => ({ value: p, label: p }))}
            onChange={(e) => set('process', e.target.value)}
          />
          <TextField
            label="品种"
            value={form.variety}
            error={fieldErrors.variety}
            placeholder="Heirloom"
            onChange={(e) => set('variety', e.target.value)}
          />
          <NumberField
            label="海拔"
            suffix="m"
            value={form.altitude_m || ''}
            error={fieldErrors.altitude_m}
            onChange={(e) => set('altitude_m', Number(e.target.value) || 0)}
          />
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <SelectField
            label="烘焙度"
            required
            value={form.roast_level}
            error={fieldErrors.roast_level}
            options={meta.roast_levels.map((r) => ({ value: r.value, label: r.label }))}
            onChange={(e) => set('roast_level', e.target.value)}
            hint="决定养豆期与最佳风味期的长度"
          />
          <TextField
            label="烘焙日期"
            type="date"
            value={form.roasted_on}
            error={fieldErrors.roasted_on}
            onChange={(e) => set('roasted_on', e.target.value)}
            hint="不填就算不出新鲜度"
          />
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <TextField
            label="开封日期"
            type="date"
            value={form.opened_on}
            error={fieldErrors.opened_on}
            onChange={(e) => set('opened_on', e.target.value)}
            hint="开封会提前衰退日"
          />
          <NumberField
            label="入库重量"
            required
            suffix="g"
            decimals={3}
            value={form.initial_weight_g}
            error={fieldErrors.initial_weight_g}
            placeholder="250"
            onChange={(e) => set('initial_weight_g', e.target.value)}
          />
          <NumberField
            label="当前余量"
            suffix="g"
            decimals={3}
            value={form.remaining_g}
            error={fieldErrors.remaining_g}
            placeholder="留空按入库重量"
            onChange={(e) => set('remaining_g', e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-2">
          <span className="text-[13px] text-[var(--c-text-2)] font-medium">
            风味标记
          </span>
          <div className="flex flex-wrap gap-1.5 items-center">
            {selectedNames.map((n) => (
              <Badge key={n.id} color={n.color || 'var(--c-text-2)'} bg="var(--c-surface-2)">
                {n.name}
              </Badge>
            ))}
            <Button size="sm" onClick={() => setFlavorOpen((v) => !v)}>
              {flavorOpen ? '收起' : selectedNames.length > 0 ? '修改' : '选择风味'}
            </Button>
          </div>

          {flavorOpen && (
            <ul className="max-h-52 overflow-y-auto border border-[var(--c-border)] rounded-[var(--r-sm)] p-2 flex flex-col gap-0.5">
              {flat.map(({ node, depth }) => {
                const on = form.flavor_node_ids.includes(node.id);
                return (
                  <li key={node.id} style={{ paddingLeft: depth * 14 }}>
                    <label className="flex items-center gap-2 px-1.5 py-1 rounded-[var(--r-sm)] hover:bg-[var(--c-surface-2)] cursor-pointer text-[13px]">
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() =>
                          set(
                            'flavor_node_ids',
                            on
                              ? form.flavor_node_ids.filter((x) => x !== node.id)
                              : [...form.flavor_node_ids, node.id],
                          )
                        }
                        className="accent-[var(--c-brand)] cursor-pointer"
                      />
                      <span
                        className="w-2 h-2 rounded-full shrink-0"
                        style={{ background: node.color || 'var(--c-border-strong)' }}
                      />
                      <span className={on ? 'text-[var(--c-text)]' : 'text-[var(--c-text-2)]'}>
                        {node.name}
                      </span>
                    </label>
                  </li>
                );
              })}
            </ul>
          )}
          {fieldErrors.flavor_node_ids && (
            <p className="text-[13px] text-[var(--c-bad)]">
              {fieldErrors.flavor_node_ids}
            </p>
          )}
        </div>

        <TextAreaField
          label="备注"
          value={form.notes}
          error={fieldErrors.notes}
          placeholder="杯测描述、购买渠道、冲煮心得…"
          onChange={(e) => set('notes', e.target.value)}
        />
      </div>
    </Modal>
  );
}

/** 扣减余量。单独一个小弹层，因为它是高频操作，走完整编辑表单太重。 */
export function ConsumeDialog({
  bean,
  onClose,
  onDone,
}: {
  bean: BeanView | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const toast = useToast();
  const [amount, setAmount] = useState('15');
  const [err, setErr] = useState<string | undefined>();
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    if (!bean) return;
    setSaving(true);
    setErr(undefined);
    try {
      await beansApi.consume(bean.id, amount);
      toast.success(`已扣减 ${amount}g`);
      onDone();
      onClose();
    } catch (e) {
      if (e instanceof ApiError && e.fields.length > 0) {
        setErr(e.fieldMap().amount_g ?? e.message);
      } else {
        toast.error(e);
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={Boolean(bean)}
      onClose={onClose}
      title={`扣减「${bean?.name ?? ''}」`}
      width={380}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>
            取消
          </Button>
          <Button variant="primary" onClick={submit} loading={saving}>
            扣减
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-[13px] text-[var(--c-text-3)]">
          当前余量 <span className="num text-[var(--c-text)]">{bean?.remaining_text}</span>
        </p>
        <NumberField
          label="扣减量"
          suffix="g"
          decimals={3}
          value={amount}
          error={err}
          onChange={(e) => setAmount(e.target.value)}
        />
        <div className="flex gap-2">
          {['15', '18', '20', '30'].map((v) => (
            <Button key={v} size="sm" onClick={() => setAmount(v)}>
              {v}g
            </Button>
          ))}
        </div>
      </div>
    </Modal>
  );
}

export { flatten as flattenFlavorTree };
