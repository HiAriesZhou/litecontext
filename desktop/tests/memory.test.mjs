import test from "node:test";
import assert from "node:assert/strict";
import { memoryActions, memoryEdit, validMemoryText } from "../web/memory.js";

const candidate = {id:"rule",status:"candidate",text:"Use local storage",scope:"project",visibility:"private",evidence:[{sourceId:"src_test",itemId:"itm_test",recordHash:`sha256:${"ab".repeat(32)}`}]};
test("memory editing preserves the full precondition and limits actions and UTF-8 size", () => {
  assert.deepEqual(memoryActions("revoked"), []);
  assert.deepEqual(memoryActions("active"), ["supersede", "revoke"]);
  const edit = memoryEdit({ action:"correct",memory:candidate,text:"New rule" });
  assert.deepEqual(edit.expected,candidate);
  assert.equal(validMemoryText("中".repeat(667)),false);
  assert.equal(validMemoryText(" "),false);
  assert.throws(()=>memoryEdit({action:"correct",memory:{...candidate,status:"active"},text:"changed"}));
});
class Node {
  constructor(tag) { this.tag=tag; this.children=[]; this.dataset={}; this.listeners={}; }
  append(...nodes) {this.children.push(...nodes);}
  replaceChildren(...nodes) {this.children=nodes;}
  setAttribute(){} removeAttribute(){} focus(){}
  addEventListener(key,fn){this.listeners[key]=fn;}
}
function all(node){return [node,...node.children.flatMap(all)];}
function visible(node){return all(node).map(n=>n.textContent||"").join("\n");}
async function harness(run) {
 const nodes=new Map();const get=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};
 const nav=["sources","memories","task","deliveries"].map(section=>{const n=new Node();n.dataset.section=section;return n;});
 const answers=[],calls=[];
 globalThis.document={getElementById:get,createElement:tag=>new Node(tag),querySelectorAll:()=>nav,addEventListener(){}};
 globalThis.window={crypto:{randomUUID:()=>"unique"},__TAURI__:{core:{invoke:async (command,args)=>{calls.push({command,args});const answer=answers.shift();if(answer instanceof Error)throw answer;return answer;}}}};
 const report={generation:1,sources:[],memories:[candidate],task:null,deliveries:[],failures:[]};
 const snapshot=()=>({bundlePath:"/synthetic/bundle",report,elapsedMs:1});
 const button=label=>all(get("content")).find(n=>n.tag==="button"&&n.textContent===label);
 try{
  await import(`../web/app.js?memory=${Math.random()}`);
  answers.push(snapshot());await get("choose").listeners.click();nav[1].listeners.click();
  await run({get,answers,calls,report,snapshot,button});
 }finally{delete globalThis.document;delete globalThis.window;}
}
test("successful save plus failed refresh is not reported as a failed write and blocks repeats",async()=>{
 await harness(async({get,answers,calls,button})=>{
  button("修改候选").listeners.click();
  let input=all(get("content")).find(n=>n.tag==="textarea");input.value="Updated rule";input.listeners.input();
  answers.push({...candidate,text:"Updated rule"},new Error("刷新失败"));
  await button("保存候选").listeners.click();
  assert.equal(calls.filter(c=>c.command==="change_memory").length,1);
  assert.deepEqual(calls.find(c=>c.command==="change_memory").args.edit.expected,candidate);
  assert.match(get("error").textContent,/记忆已保存，但刷新失败/);
  assert.equal(button("修改候选").disabled,true);
 });
});
test("conflict preserves draft, disables retry, and explicit refresh unlocks current records",async()=>{
 await harness(async({get,answers,report,snapshot,button})=>{
  button("确认生效").listeners.click();
  answers.push(Object.assign(new Error("记忆已变化"),{code:"conflict"}));
  await button("确认生效").listeners.click();
  assert.match(visible(get("content")),/记忆已变化/);
  assert.equal(button("确认生效").disabled,true);
  report.memories=[{...candidate,text:"Changed by other client"}];answers.push(snapshot());
  await get("refresh").listeners.click();
  assert.match(visible(get("content")),/Changed by other client/);
  assert.equal(button("确认生效").disabled,false);
 });
});
test("partial memory reports disable mutation controls",async()=>{
 await harness(async({answers,report,snapshot,get,button})=>{
  report.failures=["memory unavailable"];answers.push(snapshot());await get("refresh").listeners.click();
  assert.equal(button("确认生效").disabled,true);
 });
});
