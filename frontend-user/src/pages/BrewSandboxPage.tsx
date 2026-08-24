import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { beansApi, brewsApi, goldcupApi } from '@/api/endpoints';
import type {
  BrewMethod,
  BrewPayload,
  GoldCupResult,
  PreferenceCurve,
} from '@/api/types';
import { useAsync } from '@/lib/useAsync';
import { useMeta } from '@/lib/MetaContext';
import { formatClock, useStopwatch } from '@/lib/useStopwatch';
import { usePourSocket } from '@/lib/usePourSocket';
import { PageHeader } from '@/ui/AppShell';
import { Button } from '@/ui/Button';
import { Badge, Card, EmptyState, Readout, Skeleton } from '@/ui/Card';
import { NumberField, SelectField, TextAreaField, TextField } from '@/ui/Field';
import { useToast } from '@/ui/Toast';
import { GoldCupChart } from '@/charts/GoldCupChart';
import { PourCurveChart } from '@/charts/PourCurveChart';
import { GoldCupReadout } from '@/components/GoldCupReadout';

const DEFAULTS: Partial<BrewPayload> = {
  method: 'FILTER',
  dose_g: '18',
  total_water_g: '288',
  beverage_g: '',
  tds_percent: '',
  water_temp_c: 92,
  grind_micron: 0,
  agitation_count: 0,
  pre_infusion_sec: 0,
  pressure_bar_x10: 0,
  contact_seconds: 0,
  title: '',
  notes: '',
};

export default function BrewSandboxPage() {
  const meta = useMeta();
  const toast = useToast();
  const [params, setParams] = useSearchParams();

  const beanIDParam = params.get('bean');
  const [beanID, setBeanID] = useState<number | null>(
    beanIDParam ? Number(beanIDParam) : null,
  );

  const [form, setForm] = useState<Partial<BrewPayload>>(DEFAULTS);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<GoldCupResult | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [saving, setSaving] = useState(false);

  /** 已落库的冲煮 id。有它才能开实时通道 —— 打点必须挂在一条记录上。 */
  const [brewID, setBrewID] = useState<number | null>(null);
  const [scaleReading, setScaleReading] = useState('');

  const clock = useStopwatch();
  const socket = usePourSocket(brewID);

  const beansState = useAsync(
    (signal) => beansApi.list({ page_size: 200 }, signal),
    [],
  );
  const chartState = useAsync(
    () => goldcupApi.chart({ method: form.method, bean_id: beanID ?? undefined }),
    [form.method, beanID, brewID],
  );

  const beans = beansState.data?.items ?? [];
  const selectedBean = useMemo(
    () => beans.find((b) => b.id === beanID) ?? null,
    [beans, beanID],
  );

  const isEspresso = form.method === 'ESPRESSO';

  const set = <K extends keyof BrewPayload>(k: K, v: BrewPayload[K]) => {
    setForm((prev) => ({ ...prev, [k]: v }));
    setFieldErrors((prev) => {
      if (!(k in prev)) return prev;
      const next = { ...prev };
      delete next[k as string];
      return next;
    });
  };

  // ---- 秒表运行中离开页面要拦一下：那份计时数据丢了找不回来 ----
  useEffect(() => {
    if (!clock.running) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // 现代浏览器忽略自定义文案，但必须设置 returnValue 才会弹确认框
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [clock.running]);

  // ---- 试算：输入停下来 400ms 后打一次，不落库 ----
  const previewTimer = useRef<number | null>(null);
  useEffect(() => {
    if (!beanID || !form.dose_g) {
      setPreview(null);
      return;
    }
    if (previewTimer.current !== null) window.clearTimeout(previewTimer.current);
    previewTimer.current = window.setTimeout(async () => {
      setPreviewing(true);
      try {
        const res = await brewsApi.preview({ ...form, bean_id: beanID });
        setPreview(res);
        setFieldErrors({});
      } catch (e) {
        // 试算失败不弹 toast：用户还在打字，半个数字必然算不出来。
        // 字段错误静默铺到输入框下方即可。
        if (e instanceof ApiError && e.fields.length > 0) {
          setFieldErrors(e.fieldMap());
        }
        setPreview(null);
      } finally {
        setPreviewing(false);
      }
    }, 400);
    return () => {
      if (previewTimer.current !== null) window.clearTimeout(previewTimer.current);
    };
  }, [form, beanID]);

  const save = useCallback(async () => {
    if (!beanID) {
      toast.warn('先选一支豆');
      return;
    }
    setSaving(true);
    setFieldErrors({});
    try {
      const created = await brewsApi.create({
        ...form,
        bean_id: beanID,
        contact_seconds: Math.round(clock.elapsedMs / 1000) || form.contact_seconds || 0,
      });
      setBrewID(created.id);
      toast.success('已记录这次冲煮');
      chartState.reload();
    } catch (e) {
      if (e instanceof ApiError && e.fields.length > 0) {
        setFieldErrors(e.fieldMap());
        toast.warn(`有 ${e.fields.length} 处需要修改`);
      } else {
        toast.error(e);
      }
    } finally {
      setSaving(false);
    }
  }, [beanID, form, clock.elapsedMs, toast, chartState]);

  const doMark = useCallback(() => {
    if (brewID === null) {
      toast.warn('先保存这次冲煮，才能开始打点');
      return;
    }
    if (!clock.running) clock.start();
    // 累计示数留空时按上一次的值提交：用户只想标一个时间节点（如断水）
    const reading = scaleReading.trim() || String(socket.curve?.total_water_g ?? 0);
    socket.mark(Math.round(clock.elapsedMs), reading, 'CENTER_CIRCLE');
  }, [brewID, clock, scaleReading, socket, toast]);

  const reset = () => {
    clock.reset();
    setBrewID(null);
    setPreview(null);
    setScaleReading('');
    setForm(DEFAULTS);
    setFieldErrors({});
  };

  return (
    <>
      <PageHeader
        title="萃取沙盘"
        subtitle="边冲边看落区，参数改动即时反馈"
        actions={
          <>
            <Badge
              color={
                socket.status === 'open'
                  ? 'var(--c-good)'
                  : socket.status === 'connecting'
                    ? 'var(--c-warn)'
                    : 'var(--c-text-3)'
              }
              bg="var(--c-surface-2)"
            >
              {brewID === null
                ? '未开始'
                : socket.status === 'open'
                  ? '实时通道已连接'
                  : socket.status === 'connecting'
                    ? '连接中…'
                    : '通道断开，打点会排队补发'}
            </Badge>
            <Button onClick={reset}>重新开始</Button>
          </>
        }
      />

      <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_380px] gap-6 items-start">
        {/* ---- 左栏：计时 + 曲线 + 控制图 ---- */}
        <div className="flex flex-col gap-6 min-w-0">
          <Card>
            <div className="flex items-end justify-between gap-4 flex-wrap mb-4">
              <Readout label="计时" value={formatClock(clock.elapsedMs)} />
              <Readout
                label="累计注水"
                value={(socket.curve?.total_water_g ?? 0).toFixed(1)}
                unit="g"
                size="h1"
              />
              <Readout
                label="平均流速"
                value={(socket.curve?.avg_flow_rate ?? 0).toFixed(2)}
                unit="g/s"
                size="h1"
              />
            </div>

            <div className="flex flex-col gap-3">
              <div className="flex gap-3 items-end">
                <NumberField
                  label="电子秤示数（累计）"
                  suffix="g"
                  decimals={3}
                  value={scaleReading}
                  onChange={(e) => setScaleReading(e.target.value)}
                  hint="留空则只标记时间节点"
                  className="flex-1"
                />
                <Button
                  size="md"
                  variant={clock.running ? 'secondary' : 'primary'}
                  onClick={() => (clock.running ? clock.stop() : clock.start())}
                >
                  {clock.running ? '暂停' : clock.elapsedMs > 0 ? '继续' : '开始计时'}
                </Button>
              </div>

              {/* 72px 整行按钮：用户在盯着水流、只用余光瞥屏幕时
                  也要能准确按到（DesignSpec §1）。 */}
              <Button size="xl" variant="primary" onClick={doMark}>
                打 点
              </Button>

              {socket.mode === 'simulator' && brewID !== null && (
                <div className="flex items-center gap-2">
                  <Button
                    size="sm"
                    onClick={socket.simRunning ? socket.stopSim : socket.startSim}
                  >
                    {socket.simRunning ? '停止模拟注水' : '启动模拟注水'}
                  </Button>
                  <span className="text-[12px] text-[var(--c-text-3)]">
                    模拟器代替智能秤推送流速数据，用于演示与测试
                  </span>
                </div>
              )}

              {socket.lastError && (
                <p className="text-[13px] text-[var(--c-bad)]">{socket.lastError}</p>
              )}
            </div>
          </Card>

          <Card title="注水流速曲线">
            <PourCurveChart curve={socket.curve} live={brewID !== null} />
          </Card>

          <Card
            title={chartState.data?.title ?? '金杯控制图'}
            subtitle={
              selectedBean ? `${selectedBean.name} 的历史落点` : '全部冲煮记录'
            }
          >
            {chartState.loading ? (
              <Skeleton h={420} />
            ) : chartState.data ? (
              <>
                <GoldCupChart data={chartState.data} />
                {chartState.data.preference_curve && (
                  <PreferenceNote curve={chartState.data.preference_curve} />
                )}
              </>
            ) : (
              <EmptyState
                title="控制图加载失败"
                action={<Button onClick={chartState.reload}>重试</Button>}
              />
            )}
          </Card>
        </div>

        {/* ---- 右栏：参数表单 + 实时评估 ---- */}
        <div className="flex flex-col gap-6 min-w-0">
          <Card title="冲煮参数">
            <div className="flex flex-col gap-4">
              <SelectField
                label="咖啡豆"
                required
                value={beanID ? String(beanID) : ''}
                placeholder={beansState.loading ? '加载中…' : '选择一支豆'}
                options={beans.map((b) => ({
                  value: String(b.id),
                  label: `${b.name}（余 ${b.remaining_text}）`,
                }))}
                onChange={(e) => {
                  const v = e.target.value ? Number(e.target.value) : null;
                  setBeanID(v);
                  if (v) setParams({ bean: String(v) }, { replace: true });
                }}
                error={fieldErrors.bean_id}
              />

              <SelectField
                label="冲煮法"
                value={form.method ?? 'FILTER'}
                options={meta.brew_methods.map((m) => ({
                  value: m.value,
                  label: m.label,
                }))}
                onChange={(e) => set('method', e.target.value as BrewMethod)}
                hint="金杯区间按冲煮法分档，手冲与意式标准不同"
              />

              <div className="grid grid-cols-2 gap-4">
                <NumberField
                  label="粉量"
                  data-testid="input-dose"
                  required
                  suffix="g"
                  decimals={3}
                  value={form.dose_g ?? ''}
                  error={fieldErrors.dose_g}
                  onChange={(e) => set('dose_g', e.target.value)}
                />
                <NumberField
                  label={isEspresso ? '液重（出液）' : '液重（接出）'}
                  data-testid="input-beverage"
                  suffix="g"
                  decimals={3}
                  value={form.beverage_g ?? ''}
                  error={fieldErrors.beverage_g}
                  onChange={(e) => set('beverage_g', e.target.value)}
                  hint={isEspresso ? undefined : '留空按持水系数推算'}
                />
              </div>

              {!isEspresso && (
                <NumberField
                  label="总注水量"
                  data-testid="input-water"
                  suffix="g"
                  decimals={3}
                  value={form.total_water_g ?? ''}
                  error={fieldErrors.total_water_g}
                  onChange={(e) => set('total_water_g', e.target.value)}
                />
              )}

              <NumberField
                label="浓度 TDS"
                data-testid="input-tds"
                suffix="%"
                decimals={4}
                value={form.tds_percent ?? ''}
                error={fieldErrors.tds_percent}
                onChange={(e) => set('tds_percent', e.target.value)}
                hint="没有折光仪就留空，系统会转为推算模式"
              />

              <div className="grid grid-cols-2 gap-4">
                <TextField
                  label="磨豆机"
                  value={form.grinder ?? ''}
                  error={fieldErrors.grinder}
                  onChange={(e) => set('grinder', e.target.value)}
                />
                <TextField
                  label="研磨刻度"
                  value={form.grind_setting ?? ''}
                  error={fieldErrors.grind_setting}
                  onChange={(e) => set('grind_setting', e.target.value)}
                />
              </div>

              <div className="grid grid-cols-2 gap-4">
                <NumberField
                  label="水温"
                  suffix="℃"
                  value={form.water_temp_c ?? ''}
                  error={fieldErrors.water_temp_c}
                  onChange={(e) => set('water_temp_c', Number(e.target.value) || 0)}
                />
                {isEspresso ? (
                  <NumberField
                    label="压力"
                    suffix="bar"
                    decimals={1}
                    value={
                      form.pressure_bar_x10 ? String(form.pressure_bar_x10 / 10) : ''
                    }
                    error={fieldErrors.pressure_bar_x10}
                    onChange={(e) =>
                      set(
                        'pressure_bar_x10',
                        Math.round((Number(e.target.value) || 0) * 10),
                      )
                    }
                  />
                ) : (
                  <TextField
                    label="滤杯"
                    value={form.dripper ?? ''}
                    error={fieldErrors.dripper}
                    onChange={(e) => set('dripper', e.target.value)}
                  />
                )}
              </div>

              <TextAreaField
                label="备注"
                rows={2}
                value={form.notes ?? ''}
                error={fieldErrors.notes}
                onChange={(e) => set('notes', e.target.value)}
              />

              <Button
                variant="primary"
                size="lg"
                block
                loading={saving}
                onClick={save}
                disabled={brewID !== null}
              >
                {brewID !== null ? '已记录，可继续打点' : '记录这次冲煮'}
              </Button>
            </div>
          </Card>

          {previewing && !preview && <Skeleton h={200} />}

          {preview ? (
            <GoldCupReadout result={preview} />
          ) : (
            !previewing && (
              <Card>
                <EmptyState
                  title="填入粉量与液重"
                  description="填够参数后这里会实时显示萃取率、浓度与落区诊断。没有 TDS 也能算 —— 系统会明确标注为推算值。"
                />
              </Card>
            )
          )}
        </div>
      </div>
    </>
  );
}

function PreferenceNote({ curve }: { curve: PreferenceCurve }) {
  // 样本不足时展示后端给的理由，不画一条编造的曲线（DesignSpec §5.7）
  if (!curve.available) {
    return (
      <p className="mt-3 text-[12px] text-[var(--c-text-3)] leading-relaxed">
        建议曲线暂不可用：{curve.reason}
      </p>
    );
  }
  return (
    <div className="mt-3 p-3 rounded-[var(--r-md)] bg-[var(--c-brand-dim)]">
      <p className="text-[13px] text-[var(--c-brand)] font-medium">
        {curve.peak_label}
      </p>
      <p className="text-[12px] text-[var(--c-text-2)] mt-1 leading-relaxed">
        {curve.insight}
      </p>
      {curve.basis.length > 0 && (
        <ul className="mt-1.5">
          {curve.basis.map((b, i) => (
            <li key={i} className="text-[12px] text-[var(--c-text-3)]">
              · {b}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
