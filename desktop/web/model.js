export const sections = {
  sources: { title: "来源", description: "已采集的资料及其当前状态。原始文件仍保留在原处。" },
  memories: { title: "记忆", description: "持久结论及其证据。候选记忆只有确认后才参与上下文准备。" },
  task: { title: "任务", description: "当前目标、进度与下一步，独立于长期记忆。" },
  deliveries: { title: "交付记录", description: "实际输出过的上下文。记录输出不代表客户端已接收或使用。" },
};
export const states = {
  candidate: "待确认", active: "已生效", superseded: "已替代", revoked: "已撤销",
  unknown: "未知",
};
export const failureLabels = {
  "sources unavailable": "来源读取不完整",
  "task unavailable": "任务无法读取",
  "memory unavailable": "部分记忆无法读取",
  "memory list capped": "记忆数量达到读取上限，列表不完整",
  "events unavailable": "部分事件无法读取",
  "delivery unavailable": "部分交付记录无法读取",
  "snapshot changed": "读取期间数据发生变化，请刷新",
};
export function text(value, fallback = "未记录") {
  return typeof value === "string" && value.length ? value : fallback;
}
export function incomplete(report, section) {
  const markers = {
    sources: ["sources unavailable"],
    memories: ["memory unavailable", "memory list capped"],
    task: ["task unavailable"],
    deliveries: ["delivery unavailable"],
  };
  return markers[section].some((marker) => report.failures?.includes(marker));
}
export function count(report, section) {
  if (!report) return "—";
  const total = section === "task" ? (report.task ? 1 : 0) : report[section].length;
  return incomplete(report, section) ? (total ? `已读 ${total}` : "—") : total;
}
export function describeError(error, hasPrevious) {
  const message = text(error?.message, "无法读取数据，请重试。");
  return hasPrevious ? `${message} 以下仍是上次成功读取的内容。` : message;
}
export function basename(path) {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path;
}
export function evidence(ref) {
  if (!ref) return "来源版本未记录";
  const lines = ref.startLine ? ` · L${ref.startLine}${ref.endLine ? `–${ref.endLine}` : ""}` : "";
  return `${text(ref.sourceId)} / ${text(ref.itemId)}${lines}\n${text(ref.recordHash, "未固定来源版本")}`;
}
