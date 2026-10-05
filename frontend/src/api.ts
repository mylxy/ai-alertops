/** 业务 ID 必须使用字符串，避免 Snowflake 在浏览器中丢失精度。 */
export type Row = Record<string, any>;
export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
/** 统一带会话请求，并提供 CSRF 请求标识。 */
export async function api<T = Row>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const response = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-AlertOps-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const result = await response.json();
  if (!response.ok)
    throw new APIError(response.status, result.error || "请求失败");
  return result as T;
}
/** 所有时间明确按上海时区展示；缺失值不填 1970 年。 */
export function time(value: number | string): string {
  return Number(value) > 0
    ? new Intl.DateTimeFormat("zh-CN", {
        timeZone: "Asia/Shanghai",
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      }).format(new Date(Number(value)))
    : "—";
}
export function duration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.floor(ms / 1000);
  return s < 60
    ? `${s} 秒`
    : s < 3600
      ? `${Math.floor(s / 60)} 分 ${s % 60} 秒`
      : `${Math.floor(s / 3600)} 小时 ${Math.floor((s % 3600) / 60)} 分`;
}
export const labels: Record<string, string> = {
  LOG: "日志告警",
  METRIC: "指标告警",
  NOTICE: "业务提示",
  PENDING: "待处理",
  PROCESSING: "处理中",
  HANDLED: "已处理",
  RECOVERED: "已恢复",
  QUEUED: "排队中",
  STARTING: "启动中",
  RUNNING: "执行中",
  FINALIZING: "收尾中",
  SUCCEEDED: "执行完成",
  FAILED: "失败",
  SKIPPED: "已跳过",
  WAITING: "等待执行",
  DIAGNOSING: "排查中",
  LOCATED: "已定位",
  REPAIRING: "修复中",
  VERIFYING: "验证中",
  DELIVERING: "交付中",
  FINISHED: "已结束",
  FIX_PROPOSED: "已提出修复",
  NO_ISSUE: "无需代码修复",
  UNRESOLVED: "待人工排查",
  UNDETERMINED: "尚无结论",
  COMPLETE: "交付齐全",
  PARTIAL_FAILED: "交付不完整",
  NOT_REQUIRED: "无需交付",
  OPEN: "待合并",
  MERGED: "已合并",
  CLOSED: "已关闭",
  APPROVED: "评审通过",
  REJECTED: "评审未通过",
  UNKNOWN: "待核对",
  EXACT: "准确计数",
  BEST_EFFORT: "尽力计数",
  USER: "普通用户",
  ADMIN: "管理员",
  ACTIVE: "可用",
  DISABLED: "已停用",
  READY: "就绪",
  OCCUPIED: "占用",
  BLOCKED: "阻塞",
  NOTE: "人工备注",
  LOG_HANDLED: "人工处理完成",
  METRIC_RECOVERED: "监控恢复",
  AI_PROGRESS: "AI 进度",
  AI_RESULT: "AI 结果",
  MR_PROGRESS: "合并进展",
  SENT: "已同步",
  SENDING: "同步中",
  ENABLED: "已启用",
  SEALED: "已封口",
  PASSED: "通过",
  NOT_RUN: "未验证",
  UNAVAILABLE: "无法验证",
  DELIVERED: "已交付",
};
