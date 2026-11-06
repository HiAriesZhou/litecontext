import test from "node:test";
import assert from "node:assert/strict";

class Node {
  constructor() { this.children = []; this.dataset = {}; this.listeners = {}; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  removeAttribute() {}
  addEventListener(key, handler) { this.listeners[key] = handler; }
}
function visible(node) {
  return [node.textContent || "", ...node.children.map(visible)].join("\n");
}
async function render(report, run) {
  const nodes = new Map();
  const get = (id) => {
    if (!nodes.has(id)) nodes.set(id, new Node());
    return nodes.get(id);
  };
  const navigation = ["sources", "memories", "task", "deliveries"].map((section) => {
    const node = new Node(); node.dataset.section = section; return node;
  });
  globalThis.document = {
    getElementById: get, createElement: () => new Node(),
    querySelectorAll: () => navigation, addEventListener() {},
  };
  globalThis.window = { __TAURI__: { core: { invoke: async () => ({
    bundlePath: "/synthetic/bundle", elapsedMs: 10, report,
  }) } } };
  try {
    await import(`../web/app.js?render=${Math.random()}`);
    await get("choose").listeners.click();
    await run(get, (section) => navigation.find((n) => n.dataset.section === section).listeners.click());
  } finally {
    delete globalThis.document;
    delete globalThis.window;
  }
}
const emptyReport = () => ({ generation: 0, sources: [], memories: [], task: null, deliveries: [], failures: [] });

test("failed sections remain unknown; partial counts describe only read records", async () => {
  const report = emptyReport();
  report.failures = ["sources unavailable", "memory unavailable", "task unavailable", "delivery unavailable"];
  await render(report, async (get, select) => {
    for (const section of ["sources", "memories", "task", "deliveries"]) {
      select(section);
      assert.equal(get(`count-${section}`).textContent, "—", section);
      assert.match(visible(get("content")), /无法读取/, section);
      assert.doesNotMatch(visible(get("content")), /没有当前任务|没有已保存|没有已采集|没有交付记录/, section);
    }
  });
  report.memories = [{ id: "candidate", text: "read record", status: "candidate", evidence: [] }];
  await render(report, async (get, select) => {
    select("memories");
    assert.equal(get("count-memories").textContent, "已读 1");
    assert.match(visible(get("content")), /read record/);
  });
  await render(emptyReport(), async (get, select) => {
    select("task");
    assert.equal(get("count-task").textContent, 0);
    assert.match(visible(get("content")), /没有当前任务/);
  });
});

test("omissions retain exact source identity with and without locator", async () => {
  const report = emptyReport();
  report.deliveries = [{
    eventId: "synthetic", selected: 2, emitted: 0, text: "", omissions: [
      { role: "pin", reason: "unavailable", ref: { sourceId: "src_pin", itemId: "itm_pin", recordHash: "sha256:pin" } },
      { role: "knowledge", locator: "notes/rule.md", reason: "budget", ref: { sourceId: "src_doc", itemId: "itm_doc", recordHash: "sha256:doc", startLine: 2, endLine: 4 } },
    ],
  }];
  await render(report, async (get, select) => {
    select("deliveries");
    const content = visible(get("content"));
    for (const value of ["pin", "不可用", "src_pin / itm_pin", "sha256:pin", "knowledge", "notes/rule.md", "超出容量", "src_doc / itm_doc", "sha256:doc", "L2–4"]) {
      assert.ok(content.includes(value), value);
    }
  });
});
