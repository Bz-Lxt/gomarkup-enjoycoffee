import { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import { goldcupApi } from '@/api/endpoints';
import type { ProfilePayload, ProfileView } from '@/api/types';
import { useAsync } from '@/lib/useAsync';
import { useMeta } from '@/lib/MetaContext';
import { PageHeader } from '@/ui/AppShell';
import { Button } from '@/ui/Button';
import { Badge, Card, EmptyState, KV, Skeleton } from '@/ui/Card';
import { NumberField } from '@/ui/Field';
import { useToast } from '@/ui/Toast';

export default function SettingsPage() {
  const [reloadKey, setReloadKey] = useState(0);
  const state = useAsync(() => goldcupApi.profiles(), [reloadKey]);
  const reload = () => setReloadKey((k) => k + 1);

  return (
    <>
      <PageHeader
        title="设置"
        subtitle="金杯标准与持水系数。改动即时影响全站的落区判定与推算"
      />

      {state.loading ? (
        <div className="flex flex-col gap-6">
          <Skeleton h={320} />
          <Skeleton h={320} />
        </div>
      ) : state.error || !state.data ? (
        <Card>
          <EmptyState
            title="配置加载失败"
            action={<Button onClick={reload}>重试</Button>}
          />
        </Card>
      ) : (
        <div className="flex flex-col gap-6">
          {state.data.profiles.map((p) => (
            <ProfileEditor key={p.method} profile={p} onSaved={reload} />
          ))}

          <ZoneLegend zones={state.data.zones} />
        </div>
      )}
    </>
  );
}

function ProfileEditor({
  profile,
  onSaved,
}: {
  profile: ProfileView;
  onSaved: () => void;
}) {
  const toast = useToast();

  // 数值全部以字符串编辑，原样提交。不 parseFloat ——
  // 本项目的精度契约是「数值走字符串，由后端的定点数解析做唯一裁定」。
  const toForm = (p: ProfileView): ProfilePayload => ({
    yield_min_percent: String(p.yield_min_percent),
    yield_max_percent: String(p.yield_max_percent),
    strength_min_percent: String(p.strength_min_percent),
    strength_max_percent: String(p.strength_max_percent),
    ratio_min: String(p.ratio_min),
    ratio_max: String(p.ratio_max),
    lrr: String(p.lrr),
  });

  const [form, setForm] = useState<ProfilePayload>(() => toForm(profile));
  const [err, setErr] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [resetting, setResetting] = useState(false);

  // 保存或重置后 profile 会换成新对象，表单跟着同步
  useEffect(() => {
    setForm(toForm(profile));
    setErr({});
  }, [profile]);

  const set = (k: keyof ProfilePayload, v: string) => {
    setForm((prev) => ({ ...prev, [k]: v }));
    setErr((prev) => {
      if (!(k in prev)) return prev;
      const next = { ...prev };
      delete next[k];
      return next;
    });
  };

  const save = async () => {
    setBusy(true);
    setErr({});
    try {
      await goldcupApi.saveProfile(profile.method, form);
      toast.success(`${profile.label}标准已保存`);
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields.length > 0) {
        setErr(e.fieldMap());
        toast.warn(`有 ${e.fields.length} 处需要修改`);
      } else {
        toast.error(e);
      }
    } finally {
      setBusy(false);
    }
  };

  const doReset = async () => {
    setResetting(true);
    try {
      await goldcupApi.resetProfile(profile.method);
      toast.success(`${profile.label}已恢复 SCA 默认值`);
      onSaved();
    } catch (e) {
      toast.error(e);
    } finally {
      setResetting(false);
    }
  };

  return (
    <Card
      title={`${profile.label}出品标准`}
      subtitle={
        profile.uses_lrr
          ? '持水系数用于在没有称液重时推算出液量'
          : '意式不经过粉层持水推算，液重直接称量'
      }
      actions={
        <>
          <Button onClick={doReset} loading={resetting}>
            恢复 SCA 默认
          </Button>
          <Button variant="primary" onClick={save} loading={busy}>
            保存
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-4">
        <FieldPair
          label="萃取率区间"
          unit="%"
          decimals={4}
          minValue={form.yield_min_percent}
          maxValue={form.yield_max_percent}
          minError={err.yield_min_percent}
          maxError={err.yield_max_percent}
          onMin={(v) => set('yield_min_percent', v)}
          onMax={(v) => set('yield_max_percent', v)}
          hint="SCA 默认 18–22%。低于下限偏酸生涩，高于上限偏苦涩"
        />

        <FieldPair
          label="浓度区间"
          unit="%"
          decimals={4}
          minValue={form.strength_min_percent}
          maxValue={form.strength_max_percent}
          minError={err.strength_min_percent}
          maxError={err.strength_max_percent}
          onMin={(v) => set('strength_min_percent', v)}
          onMax={(v) => set('strength_max_percent', v)}
          hint={
            profile.method === 'FILTER'
              ? 'SCA 手冲默认 1.15–1.35%'
              : '意式默认区间显著更高'
          }
        />

        <FieldPair
          label="粉液比区间"
          unit="×"
          decimals={6}
          minValue={form.ratio_min}
          maxValue={form.ratio_max}
          minError={err.ratio_min}
          maxError={err.ratio_max}
          onMin={(v) => set('ratio_min', v)}
          onMax={(v) => set('ratio_max', v)}
          hint="控制图上等比例线的绘制范围"
        />

        {profile.uses_lrr && (
          <div className="flex flex-col gap-1.5">
            <NumberField
              label="持水系数 LRR"
              data-testid="input-lrr"
              suffix="×"
              decimals={6}
              value={form.lrr}
              error={err.lrr}
              onChange={(e) => set('lrr', e.target.value)}
            />
            <p className="text-[12px] text-[var(--c-text-3)] leading-relaxed">
              每 1g 咖啡粉在滤杯里留下多少克水。默认 2.0 是常见经验值，
              但它随滤杯、粉粗细、注水手法变化。
              校准方法：称一次总注水量与实际接出的液重，
              两者之差除以粉量就是你的实测 LRR。
            </p>
          </div>
        )}
      </div>

      <div className="mt-5 pt-4 border-t border-[var(--c-border)]">
        <p className="text-[13px] text-[var(--c-text-2)] mb-2">当前生效值</p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-6">
          <KV
            k="萃取率"
            v={`${profile.yield_min_text} – ${profile.yield_max_text}`}
            mono
          />
          <KV
            k="浓度"
            v={`${profile.strength_min_text} – ${profile.strength_max_text}`}
            mono
          />
          <KV
            k="粉液比"
            v={`${profile.ratio_min_text} – ${profile.ratio_max_text}`}
            mono
          />
          {profile.uses_lrr && <KV k="LRR" v={profile.lrr_text} mono />}
        </div>
      </div>
    </Card>
  );
}

function FieldPair({
  label,
  unit,
  decimals,
  minValue,
  maxValue,
  minError,
  maxError,
  onMin,
  onMax,
  hint,
}: {
  label: string;
  unit: string;
  decimals: number;
  minValue: string;
  maxValue: string;
  minError?: string;
  maxError?: string;
  onMin: (v: string) => void;
  onMax: (v: string) => void;
  hint?: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[13px] text-[var(--c-text-2)] font-medium">{label}</span>
      <div className="flex items-start gap-2">
        <NumberField
          suffix={unit}
          decimals={decimals}
          value={minValue}
          error={minError}
          onChange={(e) => onMin(e.target.value)}
          hint=""
        />
        <span className="text-[var(--c-text-3)] pt-2">–</span>
        <NumberField
          suffix={unit}
          decimals={decimals}
          value={maxValue}
          error={maxError}
          onChange={(e) => onMax(e.target.value)}
          hint=""
        />
      </div>
      {hint && (
        <p className="text-[12px] text-[var(--c-text-3)] leading-relaxed">{hint}</p>
      )}
    </div>
  );
}

/** 九宫格落区的诊断说明。让用户知道图上每个格子代表什么。 */
function ZoneLegend({
  zones,
}: {
  zones: { code: string; label: string; diagnosis: string; in_gold_cup: boolean; severity_hue: string }[];
}) {
  const meta = useMeta();
  return (
    <Card
      title="九宫格落区说明"
      subtitle={`共 ${zones.length} 个区，其中金杯区 ${zones.filter((z) => z.in_gold_cup).length} 个`}
    >
      <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
        {zones.map((z) => (
          <div
            key={z.code}
            className="p-3 rounded-[var(--r-md)] border"
            style={{
              borderColor: z.in_gold_cup ? 'var(--c-good-line)' : 'var(--c-border)',
              background: z.in_gold_cup ? 'var(--c-good-dim)' : 'var(--c-surface-2)',
            }}
          >
            <div className="flex items-center gap-2 mb-1">
              <span className="text-[13px] font-medium text-[var(--c-text)]">
                {z.label}
              </span>
              {z.in_gold_cup && (
                <Badge color="var(--c-good)" bg="transparent">
                  金杯
                </Badge>
              )}
            </div>
            <p className="text-[12px] text-[var(--c-text-3)] leading-relaxed">
              {z.diagnosis}
            </p>
          </div>
        ))}
      </div>

      <p className="text-[12px] text-[var(--c-text-3)] mt-4 leading-relaxed">
        注水数据源当前为 <span className="num text-[var(--c-text-2)]">{meta.pour_source_mode}</span>
        。manual = 仅手动打点；simulator = 内置流速模拟器；device = 接入真实智能秤。
        切换方式见 README §7。
      </p>
    </Card>
  );
}
