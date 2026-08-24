import { Link } from 'react-router-dom';
import type { BeanView } from '@/api/types';
import { Badge, Card, KV } from '@/ui/Card';
import { Button } from '@/ui/Button';
import { LifecycleBar, LifecycleUnknown } from '@/ui/LifecycleBar';
import { semantic } from '@/lib/semantic';

export function BeanCard({
  bean,
  onEdit,
  onConsume,
  onDelete,
}: {
  bean: BeanView;
  onEdit: (b: BeanView) => void;
  onConsume: (b: BeanView) => void;
  onDelete: (b: BeanView) => void;
}) {
  const s = semantic(bean.freshness.color_hint);
  const hasRoastDate = Boolean(bean.roasted_on);

  return (
    <Card className="flex flex-col" testID="bean-card">
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="min-w-0">
          <h3 className="text-h3 text-[var(--c-text)] truncate">{bean.name}</h3>
          <p className="text-[12px] text-[var(--c-text-3)] truncate">
            {bean.roaster || '未填烘焙商'}
            {bean.origin && ` · ${bean.origin}`}
          </p>
        </div>
        <Badge color={s.fg} bg={s.bg}>
          {bean.roast_level_label}
        </Badge>
      </div>

      {hasRoastDate ? (
        <LifecycleBar freshness={bean.freshness} />
      ) : (
        <LifecycleUnknown onFill={() => onEdit(bean)} />
      )}

      <div className="mt-4 flex flex-col">
        <KV
          k="余量"
          v={
            <>
              {bean.remaining_text}
              <span className="text-[var(--c-text-3)]">
                {' '}
                / {bean.initial_weight_g}g
              </span>
            </>
          }
          mono
        />
        <KV
          k="还能冲"
          v={
            bean.estimated_brews_left > 0
              ? `约 ${bean.estimated_brews_left} 次`
              : '不够一次了'
          }
          mono
        />
        <KV
          k="冲煮记录"
          v={
            bean.brew_count > 0 ? (
              <>
                {bean.brew_count} 次
                {bean.last_brewed_at && (
                  <span className="text-[var(--c-text-3)]">
                    {' '}
                    · 最近 {bean.last_brewed_at.slice(0, 10)}
                  </span>
                )}
              </>
            ) : (
              '还没冲过'
            )
          }
          mono
        />
        {bean.roasted_on && <KV k="烘焙日" v={bean.roasted_on} mono />}
      </div>

      {bean.flavors.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {bean.flavors.map((f) => (
            <Badge
              key={f.node_id}
              color={f.color || 'var(--c-text-2)'}
              bg="var(--c-surface-2)"
              className="border"
            >
              {f.name}
            </Badge>
          ))}
        </div>
      )}

      {bean.radar && bean.radar.sample_count > 0 && (
        <p className="mt-3 text-[12px] text-[var(--c-text-3)] leading-relaxed">
          {bean.radar.balance}
        </p>
      )}

      <div className="mt-4 pt-3 border-t border-[var(--c-border)] flex items-center gap-2">
        {/* 用 Link 而非 Button+navigate：这是一次真实的页面跳转，
            应该保留中键新开、右键复制链接这些浏览器原生行为。 */}
        <Link
          to={`/brew?bean=${bean.id}`}
          className="inline-flex items-center justify-center h-[30px] px-3 text-[13px] font-semibold rounded-[var(--r-sm)] bg-[var(--c-brand)] text-[#1a1208] hover:bg-[var(--c-brand-hover)] transition-colors active:translate-y-[1px]"
        >
          去冲一杯
        </Link>
        <Button size="sm" onClick={() => onConsume(bean)}>
          扣减
        </Button>
        <Button size="sm" onClick={() => onEdit(bean)}>
          编辑
        </Button>
        <Button
          size="sm"
          variant="danger"
          className="ml-auto"
          onClick={() => onDelete(bean)}
        >
          删除
        </Button>
      </div>
    </Card>
  );
}
