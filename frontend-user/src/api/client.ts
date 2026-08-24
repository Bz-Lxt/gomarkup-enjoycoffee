import type { Envelope, ErrorBody, FieldError } from './types';

/**
 * 默认同源：生产由 nginx 把 /api/ 代理到后端，开发由 vite 的 devServer 代理。
 *
 * 刻意不在构建期烘焙后端地址。烘焙会让产物和部署位置绑死 ——
 * 一个按 localhost:31410 构建的镜像，换端口或从另一台机器访问就会去
 * 请求访问者自己的 31410 端口。VITE_API_BASE 只作为一个逃生口保留，
 * 供"前端和后端确实不同源"的特殊部署使用。
 */
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? '';

/**
 * WebSocket 没有相对地址，必须给出完整 URL，所以按当前页面的协议与主机拼。
 * https 页面必须用 wss，否则浏览器会以混合内容为由直接拦掉。
 */
export const WS_BASE: string =
  import.meta.env.VITE_WS_BASE ??
  (typeof location === 'undefined'
    ? ''
    : `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`);

/**
 * ApiError 保留后端的完整错误结构，而不是拍平成一个 message 字符串。
 *
 * fields 必须能传到表单：后端会一次返回全部字段错误，
 * 前端若只弹一个 toast，用户就得靠猜哪一格填错了（DesignSpec §5.2）。
 */
export class ApiError extends Error {
  readonly kind: ErrorBody['kind'] | 'NETWORK';
  readonly code: string;
  readonly fields: FieldError[];
  readonly status: number;

  constructor(opts: {
    kind: ErrorBody['kind'] | 'NETWORK';
    code: string;
    message: string;
    fields?: FieldError[];
    status: number;
  }) {
    super(opts.message);
    this.name = 'ApiError';
    this.kind = opts.kind;
    this.code = opts.code;
    this.fields = opts.fields ?? [];
    this.status = opts.status;
  }

  /** 按字段名取错误文案，供输入框下方渲染。 */
  fieldMap(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const f of this.fields) {
      // 同一字段多条时保留第一条：后端已按重要性排序，
      // 堆叠多行会把表单撑变形。
      if (!(f.field in out)) out[f.field] = f.reason;
    }
    return out;
  }

  get isValidation(): boolean {
    return this.kind === 'VALIDATION';
  }
  get isNotFound(): boolean {
    return this.kind === 'NOT_FOUND';
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';
  body?: unknown;
  query?: Record<
    string,
    string | number | boolean | undefined | null | number[] | string[]
  >;
  signal?: AbortSignal;
}

/** 收集本次响应的 warnings，交给调用方决定是否提示。 */
export interface Result<T> {
  data: T;
  warnings: string[];
  meta: Envelope<T>['meta'];
}

function buildURL(path: string, query?: RequestOptions['query']): string {
  // URL 构造器要求 base 是绝对地址，同源时用当前页面的 origin 顶上
  const base = API_BASE === '' ? location.origin : API_BASE;
  const url = new URL(
    path.startsWith('/') ? path : `/${path}`,
    base.endsWith('/') ? base : `${base}/`,
  );
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null || v === '') continue;
      // 数组参数走逗号分隔，与后端 QueryInt64List 的解析方式一致
      url.searchParams.set(k, Array.isArray(v) ? v.join(',') : String(v));
    }
  }
  return url.toString();
}

export async function request<T>(
  path: string,
  opts: RequestOptions = {},
): Promise<Result<T>> {
  const { method = 'GET', body, query, signal } = opts;

  let res: Response;
  try {
    res = await fetch(buildURL(path, query), {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (e) {
    // AbortError 是调用方主动取消，不该被包装成"网络异常"弹给用户
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError({
      kind: 'NETWORK',
      code: 'NETWORK_UNREACHABLE',
      message: '连不上后端服务。确认容器已启动，或检查网络。',
      status: 0,
    });
  }

  // 204 没有响应体，解析 JSON 会抛
  if (res.status === 204) {
    return { data: undefined as T, warnings: [], meta: undefined };
  }

  let env: Envelope<T>;
  try {
    env = (await res.json()) as Envelope<T>;
  } catch {
    throw new ApiError({
      kind: 'INTERNAL',
      code: 'MALFORMED_RESPONSE',
      message: `服务端返回了非 JSON 内容（HTTP ${res.status}）`,
      status: res.status,
    });
  }

  if (!res.ok || !env.ok) {
    const err = env.error;
    throw new ApiError({
      kind: err?.kind ?? 'INTERNAL',
      code: err?.code ?? 'UNKNOWN',
      message: err?.message ?? `请求失败（HTTP ${res.status}）`,
      fields: err?.fields,
      status: res.status,
    });
  }

  return {
    data: env.data as T,
    warnings: env.warnings ?? [],
    meta: env.meta,
  };
}

/** 大多数调用只关心数据，不关心 warnings。 */
export async function get<T>(
  path: string,
  query?: RequestOptions['query'],
  signal?: AbortSignal,
): Promise<T> {
  return (await request<T>(path, { query, signal })).data;
}

export async function post<T>(path: string, body?: unknown): Promise<T> {
  return (await request<T>(path, { method: 'POST', body })).data;
}

export async function patch<T>(path: string, body?: unknown): Promise<T> {
  return (await request<T>(path, { method: 'PATCH', body })).data;
}

export async function del<T = void>(
  path: string,
  query?: RequestOptions['query'],
): Promise<T> {
  return (await request<T>(path, { method: 'DELETE', query })).data;
}
