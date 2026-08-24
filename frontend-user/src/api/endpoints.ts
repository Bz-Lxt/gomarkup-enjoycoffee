import { del, get, patch, post, request } from './client';
import type {
  AppendPourResponse,
  BeanBoard,
  BeanListResponse,
  BeanPayload,
  BeanView,
  BrewListResponse,
  BrewPayload,
  BrewView,
  DeleteNodeResult,
  FilterResult,
  FlavorNodeView,
  FlavorSearchHit,
  FlavorTreeResponse,
  GoldCupChart,
  GoldCupProfilesResponse,
  GoldCupResult,
  MatchMode,
  MetaResponse,
  PourEventPayload,
  ProfilePayload,
  RadarWall,
  ScorePayload,
  ScoreResponse,
  SolvePayload,
  SolveResult,
} from './types';

/**
 * 每个函数对应 docs/API.md 里的一条路由。
 *
 * 页面不直接拼 URL —— 路径散落在组件里，改一条路由就得全局搜字符串。
 *
 * 查询参数名以后端 handler 实际读取的键为准，与请求体的字段名**不一定同名**。
 * 例：豆库列表按 `flavor_ids` 筛选，但创建豆子的请求体用 `flavor_node_ids`。
 * 写错的查询参数不会报错，只会被静默忽略 —— 表现为"筛选点了没反应"，
 * 所以这里的每个键都对应一处 handler 读取点，改名前先确认后端。
 */

export const metaApi = {
  load: (signal?: AbortSignal) => get<MetaResponse>('/api/v1/meta', undefined, signal),
};

export const beansApi = {
  list: (
    params?: {
      keyword?: string;
      stages?: string[];
      roast_levels?: string[];
      flavor_ids?: number[];
      flavor_match?: MatchMode;
      exact_flavor?: boolean;
      include_archived?: boolean;
      only_opened?: boolean;
      only_unopened?: boolean;
      sort?: string;
      page?: number;
      page_size?: number;
    },
    signal?: AbortSignal,
  ) => get<BeanListResponse>('/api/v1/beans', params, signal),

  board: () => get<BeanBoard>('/api/v1/beans/board'),

  detail: (id: number) => get<BeanView>(`/api/v1/beans/${id}`),

  create: (payload: Partial<BeanPayload>) => post<BeanView>('/api/v1/beans', payload),

  // 后端是 PUT 全量替换，不是 PATCH 局部更新：
  // 调用方必须传完整对象，否则未传字段会被清空。
  update: async (id: number, payload: Partial<BeanPayload>) =>
    (await request<BeanView>(`/api/v1/beans/${id}`, { method: 'PUT', body: payload }))
      .data,

  remove: (id: number) => del(`/api/v1/beans/${id}`),

  consume: (id: number, amountG: string) =>
    post<BeanView>(`/api/v1/beans/${id}/consume`, { amount_g: amountG }),

  setFlavors: async (id: number, nodeIDs: number[]) =>
    (
      await request<BeanView>(`/api/v1/beans/${id}/flavors`, {
        method: 'PUT',
        body: { node_ids: nodeIDs },
      })
    ).data,
};

export const brewsApi = {
  // 分页走 page/page_size —— 后端没有 limit 参数，传 limit 会被忽略。
  list: (params?: {
    bean_id?: number;
    method?: string;
    only_gold?: boolean;
    only_measured?: boolean;
    since?: string;
    page?: number;
    page_size?: number;
  }) => get<BrewListResponse>('/api/v1/brews', params),

  detail: (id: number) => get<BrewView>(`/api/v1/brews/${id}`),

  create: (payload: Partial<BrewPayload>) => post<BrewView>('/api/v1/brews', payload),

  update: async (id: number, payload: Partial<BrewPayload>) =>
    (await request<BrewView>(`/api/v1/brews/${id}`, { method: 'PUT', body: payload }))
      .data,

  remove: (id: number) => del(`/api/v1/brews/${id}`),

  /** 不落库的试算，供沙盘在用户输入时实时反馈。 */
  preview: (payload: Partial<BrewPayload>) =>
    post<GoldCupResult>('/api/v1/brews/preview', payload),

  appendPour: (id: number, events: PourEventPayload[]) =>
    post<AppendPourResponse>(`/api/v1/brews/${id}/pour`, { events }),

  startSimulation: (id: number) => post<unknown>(`/api/v1/brews/${id}/simulate`),
  stopSimulation: (id: number) => del(`/api/v1/brews/${id}/simulate`),
};

export const scoresApi = {
  get: (brewID: number) => get<ScoreResponse>(`/api/v1/brews/${brewID}/score`),

  save: async (brewID: number, payload: ScorePayload) =>
    (
      await request<ScoreResponse>(`/api/v1/brews/${brewID}/score`, {
        method: 'PUT',
        body: payload,
      })
    ).data,

  remove: (brewID: number) => del(`/api/v1/brews/${brewID}/score`),

  /** 最多 6 支，超出后端会拒（DesignSpec §2.4）。 */
  wall: (beanIDs: number[]) =>
    get<RadarWall>('/api/v1/radar/wall', { bean_ids: beanIDs }),
};

export const flavorsApi = {
  tree: () => get<FlavorTreeResponse>('/api/v1/flavors/tree'),

  filter: (
    params: {
      flavor_ids: number[];
      match?: MatchMode;
      exact?: boolean;
      facets?: boolean;
    },
    signal?: AbortSignal,
  ) => get<FilterResult>('/api/v1/flavors/filter', params, signal),

  search: (q: string, limit = 20) =>
    get<FlavorSearchHit[]>('/api/v1/flavors/search', { q, limit }),

  createNode: (payload: {
    parent_id: number | null;
    name: string;
    color?: string;
    icon?: string;
    sort_order?: number;
  }) => post<FlavorNodeView>('/api/v1/flavors/nodes', payload),

  updateNode: (
    id: number,
    payload: { name?: string; color?: string; icon?: string; sort_order?: number },
  ) => patch<FlavorNodeView>(`/api/v1/flavors/nodes/${id}`, payload),

  moveNode: (id: number, payload: { parent_id: number | null; to_root?: boolean }) =>
    post<FlavorNodeView>(`/api/v1/flavors/nodes/${id}/move`, payload),

  // PROMOTE 把子节点上提到被删节点的父级，CASCADE 连带删掉整棵子树。
  // 二次确认弹层必须说明影响范围（DesignSpec §8）。
  deleteNode: (id: number, mode: 'CASCADE' | 'PROMOTE') =>
    del<DeleteNodeResult>(`/api/v1/flavors/nodes/${id}`, { mode }),
};

export const goldcupApi = {
  profiles: () => get<GoldCupProfilesResponse>('/api/v1/goldcup/profiles'),

  saveProfile: async (method: string, payload: ProfilePayload) =>
    (
      await request<GoldCupProfilesResponse>(`/api/v1/goldcup/profiles/${method}`, {
        method: 'PUT',
        body: payload,
      })
    ).data,

  resetProfile: (method: string) =>
    del<GoldCupProfilesResponse>(`/api/v1/goldcup/profiles/${method}`),

  chart: (params?: { method?: string; bean_id?: number }) =>
    get<GoldCupChart>('/api/v1/goldcup/chart', params),

  solve: (payload: Partial<SolvePayload>) =>
    post<SolveResult>('/api/v1/goldcup/solve', payload),
};

export const healthApi = {
  live: () => get<{ status: string; service: string }>('/api/v1/health'),
  ready: () => get<{ status: string }>('/api/v1/ready'),
};
