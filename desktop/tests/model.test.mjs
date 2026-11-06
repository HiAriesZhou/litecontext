import test from "node:test";
import assert from "node:assert/strict";
import { count, describeError, basename, evidence } from "../web/model.js";

test("no report is unknown, not a zero count", () => {
  assert.equal(count(null, "memories"), "—");
  assert.equal(count({ memories: [] }, "memories"), 0);
  assert.equal(count({ task: null }, "task"), 0);
  assert.equal(count({ task: { goal: "Check" } }, "task"), 1);
});
test("refresh errors explicitly identify retained data", () => {
  assert.match(describeError({ message: "读取失败" }, true), /上次成功读取/);
  assert.equal(describeError({ message: "读取失败" }, false), "读取失败");
});
test("paths and citations retain meaningful identity", () => {
  assert.equal(basename("/tmp/中文 bundle/"), "中文 bundle");
  assert.match(evidence({ sourceId: "src_a", itemId: "itm_b", recordHash: "sha256:test", startLine: 3, endLine: 5 }), /sha256:test/);
  assert.match(evidence({ sourceId: "src_a", itemId: "itm_b" }), /未固定来源版本/);
});
