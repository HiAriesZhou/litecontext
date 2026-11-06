import test from "node:test";
import assert from "node:assert/strict";

// A small DOM adapter exercises the actual app event handlers and state.
// Native rendering and picker behavior are verified separately on macOS.
class Node {
  constructor() { this.children = []; this.dataset = {}; this.listeners = {}; this.attributes = {}; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(key, value) { this.attributes[key] = value; }
  removeAttribute(key) { delete this.attributes[key]; }
  addEventListener(key, handler) { this.listeners[key] = handler; }
}

test("cancel retains refresh failure; failed new selection cannot display the previous bundle", async () => {
  const nodes = new Map();
  const get = (id) => {
    if (!nodes.has(id)) nodes.set(id, new Node());
    return nodes.get(id);
  };
  const navigation = ["sources", "memories", "task", "deliveries"].map((section) => {
    const node = new Node(); node.dataset.section = section; return node;
  });
  const answers = [];
  globalThis.document = {
    getElementById: get, createElement: () => new Node(),
    querySelectorAll: () => navigation, addEventListener() {},
  };
  globalThis.window = { __TAURI__: { core: { invoke: async () => {
    const next = answers.shift();
    if (next instanceof Error) throw next;
    return next;
  } } } };
  try {
    await import("../web/app.js");
    const loaded = {
      bundlePath: "/synthetic/first", elapsedMs: 23,
      report: { generation: 1, sources: [], memories: [], deliveries: [], failures: [], task: { goal: "first bundle" } },
    };
    answers.push(loaded);
    await get("choose").listeners.click();
    assert.equal(get("count-task").textContent, 1);
    answers.push(new Error("刷新失败"));
    await get("refresh").listeners.click();
    assert.match(get("error").textContent, /上次成功读取/);
    const previousError = get("error").textContent;
    answers.push(null);
    await get("choose").listeners.click();
    assert.equal(get("error").textContent, previousError);
    assert.equal(get("bundle-path").textContent, "/synthetic/first");
    const badSelection = new Error("不是有效 bundle");
    badSelection.bundlePath = "/synthetic/invalid";
    answers.push(badSelection);
    await get("choose").listeners.click();
    assert.equal(get("bundle-path").textContent, "/synthetic/invalid");
    assert.equal(get("count-task").textContent, "—");
    assert.doesNotMatch(get("error").textContent, /上次成功读取/);
    answers.push(loaded);
    await get("choose").listeners.click();
    assert.equal(get("error").hidden, true);
    assert.equal(get("count-task").textContent, 1);
  } finally {
    delete globalThis.document;
    delete globalThis.window;
  }
});
