import { expect, test } from '@playwright/test';

/**
 * 这个文件守的是「前后端字段名对不上」这一类缺陷。
 *
 * 它存在的直接原因：本项目在接前端时一次性写错了四个查询参数名
 * （node_ids/flavor_node_ids → flavor_ids、match → flavor_match、
 * stage → stages、limit → page_size）。当时后端不报错，只是返回**未经筛选的
 * 全量数据**，前端把它当成筛选结果渲染出来 —— 类型检查、单元测试、
 * 甚至肉眼看页面都发现不了，因为页面上确实有内容。
 *
 * 所以这里的断言方式是刻意的：不只看"请求成功了"，
 * 而是看"筛选后的数量真的比筛选前少"。前者对写错参数名的代码同样成立。
 */

const snake = /^[a-z][a-z0-9_]*$/;

/** 递归检查响应里所有键都是 snake_case，把 PascalCase 泄漏挡在契约外。 */
function assertSnakeCase(value: unknown, path = '$'): string[] {
  const bad: string[] = [];
  if (Array.isArray(value)) {
    value.forEach((v, i) => bad.push(...assertSnakeCase(v, `${path}[${i}]`)));
  } else if (value !== null && typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) {
      if (!snake.test(k)) bad.push(`${path}.${k}`);
      bad.push(...assertSnakeCase(v, `${path}.${k}`));
    }
  }
  return bad;
}

test.describe('命名规范', () => {
  // 逐个端点检查，而不是抽查一个 —— 上次泄漏正是发生在
  // 唯一一个没有 json tag 的结构体上（flavor.NodeView，只被 search 端点暴露）。
  const endpoints = [
    '/api/v1/meta',
    '/api/v1/beans?page_size=200',
    '/api/v1/beans/board',
    '/api/v1/beans/1',
    '/api/v1/brews?page_size=200',
    '/api/v1/brews/1',
    '/api/v1/brews/1/score',
    '/api/v1/flavors/tree',
    '/api/v1/flavors/filter?flavor_ids=1',
    '/api/v1/flavors/search?q=柑',
    '/api/v1/goldcup/profiles',
    '/api/v1/goldcup/chart',
    '/api/v1/radar/wall?bean_ids=1,2',
  ];

  for (const url of endpoints) {
    test(`${url} 全部字段为 snake_case`, async ({ request }) => {
      const res = await request.get(url);
      expect(res.ok(), `${url} 返回 HTTP ${res.status()}`).toBeTruthy();
      const offenders = assertSnakeCase(await res.json());
      expect(offenders, `以下字段不是 snake_case: ${offenders.join(', ')}`).toEqual([]);
    });
  }
});

test.describe('查询参数严格性', () => {
  // 这几组是真实写错过的名字。要求后端明确报错，而不是静默忽略。
  const typos: [string, string, string][] = [
    ['/api/v1/flavors/filter?node_ids=1', 'node_ids', 'flavor_ids'],
    ['/api/v1/beans?flavor_node_ids=1', 'flavor_node_ids', 'flavor_ids'],
    ['/api/v1/beans?stage=PEAK', 'stage', 'stages'],
    ['/api/v1/beans?match=ALL', 'match', 'flavor_match'],
    ['/api/v1/brews?limit=5', 'limit', 'page_size'],
  ];

  for (const [url, wrong, right] of typos) {
    test(`${wrong} 被拒绝并提示 ${right}`, async ({ request }) => {
      const res = await request.get(url);
      expect(
        res.status(),
        `写错的参数 ${wrong} 应返回 400，实际 ${res.status()} —— ` +
          `静默接受意味着调用方会拿到未经筛选的全量数据`,
      ).toBe(400);

      const body = await res.json();
      expect(body.error.code).toBe('UNKNOWN_QUERY_PARAM');
      expect(body.error.fields.map((f: { field: string }) => f.field)).toContain(wrong);
      // 提示里要给出正确的名字，否则调用方还得回去翻文档
      const reason = body.error.fields.find(
        (f: { field: string }) => f.field === wrong,
      ).reason;
      expect(reason, `提示 "${reason}" 应指向 ${right}`).toContain(right);
    });
  }

  test('未知的请求体字段同样被拒绝', async ({ request }) => {
    const res = await request.post('/api/v1/goldcup/solve', {
      data: { method: 'FILTER', target: 'DOSE', water_g: '300' },
    });
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.error.code).toBe('UNKNOWN_FIELD');
  });

  test('多个未知参数一次全部报出', async ({ request }) => {
    const res = await request.get('/api/v1/beans?foo=1&bar=2&baz=3');
    const body = await res.json();
    // 一次只报一个会让调用方反复试错
    expect(body.error.fields.length).toBe(3);
  });
});

test.describe('筛选真的生效', () => {
  test('风味筛选会缩小结果集，而不是原样返回', async ({ request }) => {
    const all = await (await request.get('/api/v1/flavors/filter?flavor_ids=')).json();

    const tree = await (await request.get('/api/v1/flavors/tree')).json();
    const leaf = findDeepNode(tree.data.tree);
    expect(leaf, '风味树里应能找到一个较深的节点用于筛选').not.toBeNull();

    const res = await request.get(
      `/api/v1/flavors/filter?flavor_ids=${leaf!.id}&match=ALL`,
    );
    const filtered = (await res.json()).data;

    expect(filtered.total_beans).toBeGreaterThan(0);
    // 核心断言：命中数必须严格少于总数。若参数被忽略，这两个值会相等。
    expect(
      filtered.matched_count,
      `按节点 ${leaf!.id}（${leaf!.name}）筛选后命中 ${filtered.matched_count} 支，` +
        `与总数 ${filtered.total_beans} 相同 —— 筛选条件很可能被忽略了`,
    ).toBeLessThan(filtered.total_beans);
    expect(filtered.conditions.length).toBe(1);
    expect(filtered.unknown_node_ids).toEqual([]);
    void all;
  });

  test('豆库列表按风味筛选后数量下降', async ({ request }) => {
    const all = (await (await request.get('/api/v1/beans?page_size=200')).json()).data;
    expect(all.items.length).toBeGreaterThan(1);

    const tree = await (await request.get('/api/v1/flavors/tree')).json();
    const leaf = findDeepNode(tree.data.tree);

    const res = await request.get(
      `/api/v1/beans?flavor_ids=${leaf!.id}&flavor_match=ALL&page_size=200`,
    );
    const filtered = (await res.json()).data;

    expect(filtered.items.length).toBeLessThan(all.items.length);
    // flavor_filter 必须回传，前端要靠它显示联动计数与耗时
    expect(filtered.flavor_filter).not.toBeNull();
    expect(filtered.flavor_filter.matched_count).toBe(filtered.items.length);
  });

  test('不存在的风味节点被明确报告，而不是当作无条件', async ({ request }) => {
    const res = await request.get('/api/v1/flavors/filter?flavor_ids=999999');
    const { data } = await res.json();
    // 静默忽略会让用户以为筛选生效了，实际上条件被丢掉了
    expect(data.unknown_node_ids).toContain(999999);
  });
});

test.describe('NFR-01 风味筛选延迟', () => {
  test('筛选自报耗时在 10ms 预算内', async ({ request }) => {
    const tree = await (await request.get('/api/v1/flavors/tree')).json();
    const ids = collectIDs(tree.data.tree).slice(0, 8).join(',');

    const url = `/api/v1/flavors/filter?flavor_ids=${ids}&match=ANY&facets=true`;
    const measure = async () =>
      (await (await request.get(url)).json()).data.elapsed_micros as number;

    // 预热不计入统计。刚起的容器第一次筛选要付一次性成本（首次触页、
    // 分配器预热），那不是用户实际感知的稳态延迟。曾经因为没有预热，
    // 一个 63ms 的冷启动首样本就能让这条断言失败。
    const warmup: number[] = [];
    for (let i = 0; i < 20; i++) warmup.push(await measure());

    // 样本数必须够表达 P99。之前只取 30 个样本，
    // samples[floor(30 * 0.99)] 就是第 30 个 —— 即最大值。
    // 把最大值叫做 P99，等于让任意单个离群点判定整条需求不达标，
    // 而 NFR-01 写的是"P99 ≤ 10ms（压测）"，本就允许长尾里有个别毛刺。
    const n = 200;
    const samples: number[] = [];
    for (let i = 0; i < n; i++) samples.push(await measure());
    samples.sort((a, b) => a - b);

    const at = (q: number) => samples[Math.min(n - 1, Math.floor(n * q))];
    const p50 = at(0.5);
    const p99 = at(0.99);
    const max = samples[n - 1];
    const detail =
      `P50=${p50}µs P99=${p99}µs MAX=${max}µs ` +
      `预热首样本=${warmup[0]}µs（不计入）`;

    // elapsed_micros 是后端筛选本身的耗时，不含 HTTP 往返 ——
    // NFR-01 承诺的正是这一段。
    expect(p99, `NFR-01 P99 超预算：${detail}`).toBeLessThan(10_000);

    // 单个离群点不判需求不达标，但也不能放任无上限：真出现几百毫秒的
    // 停顿，说明有阻塞或锁竞争，那是另一类问题，必须让它可见。
    expect(max, `出现异常长尾，疑有阻塞：${detail}`).toBeLessThan(200_000);

    console.log(`[NFR-01] ${detail}`);
  });
});

// ---------------------------------------------------------------- 辅助

interface TreeNode {
  id: number;
  name: string;
  children: TreeNode[];
  aggregate_bean_count: number;
}

/** 找一个有豆子标记、但不是全部豆子都有的节点，用于验证筛选确实收窄了结果。 */
function findDeepNode(nodes: TreeNode[]): TreeNode | null {
  let best: TreeNode | null = null;
  const walk = (ns: TreeNode[], depth: number) => {
    for (const n of ns) {
      if (n.aggregate_bean_count > 0 && (best === null || depth > 0)) {
        if (best === null || n.aggregate_bean_count <= best.aggregate_bean_count) {
          best = n;
        }
      }
      walk(n.children ?? [], depth + 1);
    }
  };
  walk(nodes, 0);
  return best;
}

function collectIDs(nodes: TreeNode[]): number[] {
  const out: number[] = [];
  const walk = (ns: TreeNode[]) => {
    for (const n of ns) {
      out.push(n.id);
      walk(n.children ?? []);
    }
  };
  walk(nodes);
  return out;
}
