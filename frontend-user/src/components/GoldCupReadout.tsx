import type { GoldCupResult } from '@/api/types';
import { AdvisoryTag, Badge, Card, KV, Readout } from '@/ui/Card';

/**
 * 金杯评估读数面板。
 *
 * 推算模式（advisory）的处理是这个组件存在的主要理由：
 * 虚线边框 + 推算角标 + 置信区间 + 免责说明，四件套缺一不可。
 * 让用户误以为推算值是实测值，他会照着一组推测出来的参数反复冲
 * （裁定 C-01 / DesignSpec §5.6）。
 */
export function GoldCupReadout({ result }: { result: GoldCupResult }) {
  const zoneColor = result.zone.in_gold_cup ? 'var(--c-good)' : 'var(--c-warn)';
  const est = result.estimation;

  return (
    <Card advisory={result.advisory} testID="goldcup-readout">
      <div className="flex items-start justify-between gap-3 mb-4">
        <div className="flex items-center gap-2 flex-wrap">
          <Badge
            color={zoneColor}
            bg={result.zone.in_gold_cup ? 'var(--c-good-dim)' : 'var(--c-warn-dim)'}
          >
            {result.zone.label}
          </Badge>
          {/* 颜色不作为唯一信息载体：落在金杯区时另加文字徽章 */}
          {result.zone.in_gold_cup && (
            <Badge color="var(--c-good)" bg="var(--c-good-dim)">
              金杯
            </Badge>
          )}
          {result.advisory && <AdvisoryTag />}
        </div>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-3 gap-5 mb-5">
        <Readout
          label="萃取率"
          value={result.yield_text}
          color={zoneColor}
          advisory={result.advisory}
        />
        <Readout
          label="浓度 TDS"
          value={result.tds_text}
          size="h1"
          advisory={result.advisory}
        />
        <Readout label="粉液比" value={result.brew_ratio_text} size="h1" />
      </div>

      {/* 推算模式下把置信区间画成误差带，而不是给一个确定的数 */}
      {est && (
        <div className="mb-5 p-3 rounded-[var(--r-md)] bg-[var(--c-info-dim)] border border-dashed border-[var(--c-info-line)]">
          <div className="flex items-center justify-between gap-2 mb-2">
            <span className="text-[13px] text-[var(--c-info)] font-medium">
              {est.estimator_label}
            </span>
            <Badge color="var(--c-info)" bg="transparent">
              置信度 {est.confidence_tier === 'HIGH' ? '高' : est.confidence_tier === 'MEDIUM' ? '中' : '低'}
              {' · '}
              {(est.confidence * 100).toFixed(0)}%
            </Badge>
          </div>

          <ConfidenceBand
            lower={est.yield_lower_percent}
            upper={est.yield_upper_percent}
            center={est.yield_percent}
            profileMin={result.profile.yield_min_percent}
            profileMax={result.profile.yield_max_percent}
          />

          <p className="num text-[12px] text-[var(--c-text-2)] mt-2">
            萃取率区间 {est.yield_range_text}（{est.sample_size} 条历史样本）
          </p>

          {est.basis.length > 0 && (
            <ul className="mt-2 flex flex-col gap-0.5">
              {est.basis.map((b, i) => (
                <li key={i} className="text-[12px] text-[var(--c-text-3)]">
                  · {b}
                </li>
              ))}
            </ul>
          )}

          {/* 免责说明必须完整展示，不折叠、不截断 */}
          <p className="text-[12px] text-[var(--c-info)] mt-2 leading-relaxed">
            {est.disclaimer}
          </p>
        </div>
      )}

      <div className="flex flex-col mb-4">
        <KV k="粉量" v={result.dose_text} mono />
        <KV k="液重" v={result.beverage_text} mono />
        <KV k="粉层持水" v={result.absorbed_text} mono />
        <KV k="溶出物" v={result.dissolved_solids_text} mono />
      </div>

      <p className="text-[13px] text-[var(--c-text-2)] leading-relaxed mb-3">
        {result.zone.diagnosis}
      </p>

      {result.advice.length > 0 && (
        <div className="flex flex-col gap-2">
          <h4 className="text-[13px] text-[var(--c-text-2)] font-medium">下一步怎么调</h4>
          {result.advice.map((a, i) => (
            <div
              key={i}
              className="p-3 rounded-[var(--r-md)] bg-[var(--c-surface-2)] border border-[var(--c-border)]"
            >
              <div className="flex items-baseline justify-between gap-2">
                <p className="text-[13px] text-[var(--c-text)] font-medium">
                  {a.headline}
                </p>
                {a.target_text && (
                  <span className="num text-[12px] text-[var(--c-brand)] shrink-0">
                    {a.target_text}
                  </span>
                )}
              </div>
              <p className="text-[12px] text-[var(--c-text-3)] mt-1 leading-relaxed">
                {a.rationale}
              </p>
            </div>
          ))}
        </div>
      )}

      {result.warnings.length > 0 && (
        <ul className="mt-3 flex flex-col gap-1">
          {result.warnings.map((w, i) => (
            <li key={i} className="text-[12px] text-[var(--c-warn)] leading-relaxed">
              {w}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/**
 * 置信区间的误差带。
 *
 * 画成一条带而不是一个点，是因为推算出来的萃取率本质上是个区间。
 * 用点表示会把不确定性藏起来 —— 而这里的不确定性正是用户需要知道的事。
 */
function ConfidenceBand({
  lower,
  upper,
  center,
  profileMin,
  profileMax,
}: {
  lower: number;
  upper: number;
  center: number;
  profileMin: number;
  profileMax: number;
}) {
  // 坐标范围比金杯区两侧各放宽 3 个百分点，让越界的区间也能看见
  const axisMin = Math.min(lower, profileMin) - 3;
  const axisMax = Math.max(upper, profileMax) + 3;
  const span = axisMax - axisMin || 1;
  const pct = (v: number) => ((v - axisMin) / span) * 100;

  return (
    <div className="relative h-6">
      {/* 金杯区参考底 */}
      <div
        className="absolute top-2 h-2 rounded-full bg-[var(--c-good-dim)] border-y border-[var(--c-good-line)]"
        style={{ left: `${pct(profileMin)}%`, width: `${pct(profileMax) - pct(profileMin)}%` }}
      />
      {/* 置信区间 */}
      <div
        className="absolute top-1.5 h-3 rounded-full bg-[var(--c-info)] opacity-45"
        style={{ left: `${pct(lower)}%`, width: `${Math.max(1, pct(upper) - pct(lower))}%` }}
      />
      {/* 中心估计值 */}
      <div
        className="absolute top-0.5 w-[2px] h-5 bg-[var(--c-info)]"
        style={{ left: `${pct(center)}%` }}
      />
      <span
        className="num absolute top-[22px] text-[10px] text-[var(--c-info)] -translate-x-1/2 whitespace-nowrap"
        style={{ left: `${pct(center)}%` }}
      >
        {center.toFixed(2)}%
      </span>
    </div>
  );
}
