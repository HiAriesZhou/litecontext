import test from "node:test";
import assert from "node:assert/strict";
import { canApply, canRestore } from "../web/clients.js";
test("only unmanaged changes can apply; externally modified configs cannot auto-restore",()=>{
 assert.equal(canApply({managed:false,changed:true}),true);
 assert.equal(canApply({managed:true,changed:true}),false);
 assert.equal(canRestore({managed:true,state:"installed"}),true);
 assert.equal(canRestore({managed:true,state:"modified"}),false);
});
class Node {
 constructor(tag){this.tag=tag;this.children=[];this.listeners={};this.dataset={};}
 append(...nodes){this.children.push(...nodes);} replaceChildren(...nodes){this.children=nodes;}
 setAttribute(){} removeAttribute(){} focus(){} addEventListener(k,f){this.listeners[k]=f;}
}
const all=n=>[n,...n.children.flatMap(all)];
test("configuration UI requires preview and explicit action; failures remove stale action state",async()=>{
 const nodes=new Map();const get=id=>{if(!nodes.has(id))nodes.set(id,new Node());return nodes.get(id);};
 const nav=["sources","memories","task","deliveries","clients"].map(section=>{const n=new Node();n.dataset.section=section;return n;});
 const answers=[],calls=[];
 globalThis.document={getElementById:get,createElement:tag=>new Node(tag),querySelectorAll:()=>nav,addEventListener(){}};
 globalThis.window={__TAURI__:{core:{invoke:async(command,args)=>{calls.push({command,args});const result=answers.shift();if(result instanceof Error)throw result;return result;}}}};
 const button=label=>all(get("content")).find(n=>n.tag==="button"&&n.textContent===label);
 const plan={host:"codex",project:"/synthetic",destination:"/synthetic/.codex/hooks.json",expected:"hash",changed:true,managed:false,state:"unmanaged",fragment:{hooks:{}},loaded:"unknown"};
 try{
  await import(`../web/app.js?clients=${Math.random()}`);
  answers.push({bundlePath:"/bundle",report:{generation:1,sources:[],memories:[],deliveries:[],failures:[]}});await get("choose").listeners.click();nav.at(-1).listeners.click();
  assert.equal(button("应用此配置"),undefined);
  answers.push(plan);await button("选择项目并预览").listeners.click();
  button("应用此配置").listeners.click();assert.equal(calls.filter(c=>c.command==="change_client").length,0);
  answers.push({...plan,managed:true,changed:false,state:"installed"});await button("确认应用").listeners.click();
  assert.equal(button("应用此配置").disabled,true);assert.equal(button("撤销此接入").disabled,false);
  button("撤销此接入").listeners.click();answers.push(new Error("配置已改动"));await button("确认撤销").listeners.click();
  assert.equal(button("撤销此接入"),undefined);
  assert.match(all(get("content")).map(n=>n.textContent||"").join(" "),/配置已改动/);
  assert.deepEqual(calls.filter(c=>c.command==="change_client").map(c=>c.args),[{action:"apply"},{action:"restore"}]);
  answers.push({...plan,managed:true,changed:false,state:"installed"});await button("选择项目并预览").listeners.click();
  button("撤销此接入").listeners.click();
  answers.push({...plan,managed:false,changed:false,state:"restored",fragment:null});
  await button("确认撤销").listeners.click();
  assert.equal(button("应用此配置").disabled,true);
  assert.equal(button("撤销此接入").disabled,true);
  const restoredText=all(get("content")).map(n=>n.textContent||"").join(" ");
  assert.match(restoredText,/已还原，请重新预览/);
  assert.match(restoredText,/未检查，请重新预览/);
  assert.doesNotMatch(restoredText,/相同配置已存在|将使用的 Hook 配置/);
 } finally {delete globalThis.document;delete globalThis.window;}
});
