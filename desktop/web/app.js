import { memoryActions, actionLabels, actionHelp, memoryEdit } from "./memory.js";
import { sections, states, failureLabels, text, count, incomplete, describeError, basename, evidence } from "./model.js";

const $ = (id) => document.getElementById(id);
let section = "sources";
let snapshot = null;
let selectedPath = "";
let loadedAt = null;
let busy = false;
let lastError = "";
let notice = "";
let draft = null;
let openMemory = "";
let writeBlocked = false;

function element(tag, content, className) {
  const node = document.createElement(tag);
  if (content !== undefined) node.textContent = content;
  if (className) node.className = className;
  return node;
}
function append(parent, ...nodes) { parent.append(...nodes); return parent; }
function pill(label, tone = "") { return element("span", label, `pill ${tone}`); }
function field(label, value, mono = false) {
  return append(element("div", undefined, "field"),
    element("dt", label), element("dd", text(value), mono ? "mono" : ""));
}
function record(title, meta, body, badge) {
  const details = element("details", undefined, "record");
  const summary = element("summary");
  append(summary, append(element("div", undefined, "record-label"),
    element("span", title, "record-title"), element("span", meta, "record-meta")));
  if (badge) summary.append(badge);
  return append(details, summary, body);
}
function empty(title, description) {
  return append(element("div", undefined, "empty"),
    element("span", "○", "empty-mark"),
    element("h2", title), element("p", description));
}
function renderSources(report, container) {
  if (!report.sources.length) return container.append(empty("没有已采集的来源", "这个 bundle 尚未保存来源，或来源列表未能读取。"));
  for (const source of report.sources) {
    const body = append(element("dl", undefined, "record-body"),
      field("来源标识", source.id, true), field("类型", source.type),
      field("更新时间", source.updatedAt), field("连接状态", "未验证"));
    container.append(record(text(source.name), text(source.type), body,
      pill(source.active ? "可检索" : "已停用", source.active ? "positive" : "")));
  }
}
function renderMemories(report, container) {
  if (!report.memories.length) return container.append(empty("没有已保存的记忆", "这里将显示候选结论、已生效记忆及其替代或撤销状态。"));
  for (const memory of report.memories) {
    const body = append(element("div", undefined, "record-body"),
      element("p", memory.text, "prose"),
      append(element("dl"), field("范围", memory.scope), field("可见性", memory.visibility),
        field("记忆标识", memory.id, true)));
    body.append(element("h3", "证据", "detail-heading"));
    for (const ref of memory.evidence || []) body.append(element("pre", evidence(ref), "citation"));
    if (memory.supersedes) body.append(element("p", `替代：${memory.supersedes}`, "mono"));
    if (memory.supersededBy) body.append(element("p", `后续记忆：${memory.supersededBy}`, "mono"));
    const row = record(text(memory.text), `${text(memory.scope)} · ${text(memory.visibility)}`, body,
      pill(states[memory.status] || "未知状态", memory.status === "active" ? "positive" : ""));
    row.open = openMemory === memory.id;
    row.addEventListener("toggle", () => { if (row.open) openMemory = memory.id; });
    const blocked = busy || writeBlocked || incomplete(report, "memories") || report.failures.includes("snapshot changed");
    if (draft?.memory.id === memory.id) {
      const editor = element("div", undefined, "memory-editor");
      editor.append(element("h3", actionLabels[draft.action]), element("p", actionHelp[draft.action], "muted"));
      if (["correct", "supersede"].includes(draft.action)) {
        const label = element("label", "记忆内容"); label.htmlFor = "memory-text";
        const input = element("textarea"); input.id = "memory-text"; input.value = draft.text; input.rows = 5; input.disabled = busy;
        input.addEventListener("input", () => { draft.text = input.value; });
        editor.append(label, input, element("p", "最多 2000 字节；保留原有引用。", "muted"));
      }
      if (draft.error) { const error = element("p", draft.error, "form-error"); error.setAttribute("role", "alert"); editor.append(error); }
      const actions = element("div", undefined, "actions");
      actions.append(actionButton("取消", () => { draft = null; render(); $("content").focus?.(); }, busy),
        actionButton(busy ? "正在保存…" : draft.action === "correct" ? "保存候选" : actionLabels[draft.action], submitMemory, blocked));
      editor.append(actions); body.append(editor);
    } else {
      const actions = element("div", undefined, "actions");
      for (const action of memoryActions(memory.status)) actions.append(actionButton(actionLabels[action], () => {
        draft = { action, memory, text: memory.text, error: "" }; openMemory = memory.id; notice = ""; render();
        focusEditor();
      }, blocked || Boolean(draft) || (action === "supersede" && report.memories.length >= 50)));
      if (memory.status === "active" && report.memories.length >= 50) actions.append(element("p", "已达 50 条记录上限，暂不能创建替代。仍可撤销。", "muted"));
      body.append(actions);
    }
    container.append(row);
  }
}
function focusEditor() {
  const target = ["correct", "supersede"].includes(draft?.action) ? document.getElementById("memory-text") : $("content");
  target?.focus?.();
}
function actionButton(label, handler, disabled) {
  const button = element("button", label, "button secondary"); button.type = "button"; button.disabled = disabled;
  button.addEventListener("click", handler); return button;
}
async function submitMemory() {
  if (busy || !draft || writeBlocked) return;
  let edit;
  try { edit = memoryEdit(draft, draft.action === "supersede" ? `mem-${window.crypto.randomUUID()}` : ""); }
  catch (error) { draft.error = error.message; render(); focusEditor(); return; }
  busy = true; draft.error = ""; notice = ""; render();
  try {
    const saved = await window.__TAURI__.core.invoke("change_memory", { bundlePath: selectedPath, edit });
    draft = null; openMemory = saved.id;
    notice = "记忆已保存。";
    try {
      snapshot = await window.__TAURI__.core.invoke("refresh_bundle"); loadedAt = new Date(); lastError = ""; writeBlocked = false;
    } catch (error) {
      writeBlocked = true;
      lastError = `记忆已保存，但刷新失败。${describeError(error, true)} 请刷新后再操作。`;
    }
  } catch (error) {
    draft.error = text(error?.message, "操作结果尚未确认，请刷新后检查，勿重复提交。");
    if (error?.code !== "invalid_change") {
      writeBlocked = true;
      lastError = `${draft.error} 以下仍是上次读取的内容。`;
    }
  } finally { busy = false; render(); if (draft) focusEditor(); else $("content").focus?.(); }
}
function renderTask(report, container) {
  if (!report.task) return container.append(empty("没有当前任务", "尚未保存任务检查点。没有记录不代表 Agent 没有在工作。"));
  const labels = { goal: "目标", progress: "已完成", checks: "验证", blockers: "阻塞", next: "下一步" };
  const list = element("dl", undefined, "task-fields");
  for (const [key, label] of Object.entries(labels)) list.append(field(label, report.task[key]));
  container.append(list);
}
function renderDeliveries(report, container) {
  if (!report.deliveries.length) return container.append(empty("没有交付记录", "Agent 接入并保存一次上下文输出后，记录会出现在这里。"));
  for (const delivery of report.deliveries) {
    const body = element("div", undefined, "record-body");
    body.append(append(element("dl"), field("客户端接收", "未知"), field("实际使用", "未知")));
    body.append(element("h3", "输出内容", "detail-heading"), element("pre", text(delivery.text), "delivered-text"));
    body.append(element("h3", "来源版本", "detail-heading"));
    if (!Array.isArray(delivery.entries)) body.append(element("p", "未记录来源版本。", "muted"));
    else if (!delivery.entries.length) body.append(element("p", delivery.entriesTruncated ? "来源列表受限，未保留条目。" : "没有输出来源条目。", "muted"));
    else for (const entry of delivery.entries) body.append(element("pre", `${text(entry.role)} · ${text(entry.locator)}\n${evidence(entry.ref)}`, "citation"));
    if (delivery.entriesTruncated) body.append(element("p", "来源条目列表不完整。", "notice"));
    body.append(element("h3", "未提供的内容", "detail-heading"));
    if (!Array.isArray(delivery.omissions)) body.append(element("p", "未记录省略原因。", "muted"));
    else if (!delivery.omissions.length) body.append(element("p", "没有记录到省略项。", "muted"));
    else for (const omitted of delivery.omissions) {
      const reasons = { budget: "超出容量", unavailable: "不可用", truncated: "记录受限" };
      body.append(element("p", [reasons[omitted.reason] || "未知原因", text(omitted.role), omitted.locator].filter(Boolean).join(" · "), "omission"));
      if (omitted.ref) body.append(element("pre", evidence(omitted.ref), "citation"));
    }
    const elapsed = Number.isInteger(delivery.elapsedMs) ? `${delivery.elapsedMs} ms` : "耗时未记录";
    container.append(record(text(delivery.eventId), `已选 ${delivery.selected} · 已输出 ${delivery.emitted} · ${elapsed}`, body, pill("接收未验证")));
  }
}
function render() {
  const report = snapshot?.report;
  $("section-title").textContent = sections[section].title;
  $("bundle-name").textContent = selectedPath ? basename(selectedPath) : "尚未选择";
  $("bundle-path").textContent = selectedPath || "尚未读取任何数据";
  $("generation").textContent = report ? `来源快照 ${report.generation}` : "";
  $("freshness").textContent = loadedAt
    ? `上次读取 ${loadedAt.toLocaleTimeString("zh-CN")} · 手动刷新`
    : "选择一个 bundle，查看实际保存的信息。";
  $("timing").textContent = snapshot ? `读取耗时 ${snapshot.elapsedMs} ms` : "";
  $("choose").disabled = busy || Boolean(draft);
  $("refresh").disabled = busy || !selectedPath;
  $("status").textContent = busy ? "正在处理本机数据…" : notice;
  $("content").setAttribute("aria-busy", String(busy));
  $("error").hidden = !lastError;
  $("error").textContent = lastError;
  for (const key of Object.keys(sections)) $(`count-${key}`).textContent = count(report, key);
  for (const button of document.querySelectorAll("[data-section]")) {
    if (button.dataset.section === section) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  const warnings = report?.failures || [];
  $("warnings").hidden = !warnings.length;
  $("warnings").textContent = warnings.map((value) => failureLabels[value] || `读取提示：${value}`).join("；");
  const content = $("content");
  content.replaceChildren(element("p", sections[section].description, "section-description"));
  if (!report) {
    content.append(empty(selectedPath ? "暂时无法显示数据" : "打开你的信息底座",
      selectedPath ? "修复文件或访问问题后，点击刷新重试。也可以选择另一个 bundle。" : "选择已有的 LiteContext bundle 文件夹，查看来源、记忆、任务和交付记录。"));
    return;
  }
  const hasRecords = section === "task" ? Boolean(report.task) : report[section].length > 0;
  if (!hasRecords && incomplete(report, section)) {
    content.append(empty(`无法读取${sections[section].title}`, "当前无法确认是否存在记录。请检查读取提示，修复后刷新重试。"));
  } else {
    ({ sources: renderSources, memories: renderMemories, task: renderTask, deliveries: renderDeliveries })[section](report, content);
  }
}
async function load(choose) {
  if (busy || (choose && draft)) return;
  const previousError = lastError;
  busy = true;
  lastError = "";
  render();
  try {
    const result = await window.__TAURI__.core.invoke(choose ? "choose_bundle" : "refresh_bundle");
    if (result !== null) {
      snapshot = result;
      selectedPath = result.bundlePath;
      loadedAt = new Date();
      draft = null; writeBlocked = false; notice = "";
    } else {
      lastError = previousError;
    }
  } catch (error) {
    if (choose) {
      snapshot = null;
      loadedAt = null;
      selectedPath = error?.bundlePath || "";
    }
    writeBlocked = true;
    lastError = describeError(error, Boolean(snapshot));
  } finally {
    busy = false;
    render();
  }
}
$("choose").addEventListener("click", () => load(true));
$("refresh").addEventListener("click", () => load(false));
for (const button of document.querySelectorAll("[data-section]")) {
  button.addEventListener("click", () => { section = button.dataset.section; render(); });
}
document.addEventListener("keydown", (event) => {
  if (!event.metaKey || event.altKey || event.shiftKey) return;
  if (event.key.toLowerCase() === "o") { event.preventDefault(); load(true); }
  if (event.key.toLowerCase() === "r") { event.preventDefault(); if (selectedPath) load(false); }
});
render();
