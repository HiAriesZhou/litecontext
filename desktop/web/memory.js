export function memoryActions(status) {
  if (status === "candidate") return ["correct", "confirm", "revoke"];
  if (status === "active") return ["supersede", "revoke"];
  return [];
}
export const actionLabels = { correct: "修改候选", confirm: "确认生效", revoke: "撤销记忆", supersede: "创建替代" };
export const actionHelp = {
  correct: "仅修改候选内容，范围、可见性和证据保持不变。保存后仍需确认。",
  confirm: "确认后，这条记忆可参与上下文准备。请先核对内容和证据。",
  revoke: "撤销后不再参与上下文准备，记录和证据仍保留。",
  supersede: "提交后旧记忆立即退出上下文，新内容保存为待确认候选，并沿用范围、可见性和证据。请核对这些证据仍支持新内容。",
};
export function validMemoryText(value) {
  return typeof value === "string" && value.trim().length > 0 && new TextEncoder().encode(value).length <= 2000;
}
export function memoryEdit(draft, replacementID) {
  const { action, memory, text } = draft;
  if (!memoryActions(memory.status).includes(action)) throw new Error("当前状态不支持此操作。");
  if (["correct", "supersede"].includes(action) && !validMemoryText(text)) throw new Error("请填写内容，长度不得超过 2000 字节（约 660 个汉字）。");
  return { action, expected: memory, text, replacement_id: action === "supersede" ? replacementID : "" };
}
