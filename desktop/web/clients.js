export const clientLabels = { codex: "Codex", claude: "Claude Code", gemini: "Gemini CLI" };
export const configStates = { unmanaged: "未由本机工作台管理", installed: "配置已写入", not_applied: "保留了撤销凭据，配置未应用", modified: "配置存在外部改动", restored: "已还原，请重新预览" };
export function canApply(plan) { return Boolean(plan && !plan.managed && plan.changed); }
export function canRestore(plan) { return Boolean(plan?.managed && ["installed", "not_applied"].includes(plan.state)); }
