const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function harness(){
 const elements=new Map(),requests=[];
 function el(id){if(!elements.has(id))elements.set(id,{value:'',textContent:'',disabled:false,children:[],hidden:false,handlers:{},classList:{add(){el(id).hidden=true},toggle(_,on){el(id).hidden=on}},replaceChildren(...c){this.children=c;this.value=c[0]?.value||''},append(c){this.children.push(c);if(this.children.length===1)this.value=c.value},addEventListener(n,f){this.handlers[n]=f}});return elements.get(id)}
 const box=vm.createContext({Map,JSON,Error,setTimeout,clearTimeout,document:{getElementById:el,createElement:()=>({value:'',textContent:''})},fetch:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}))});
 vm.runInContext(fs.readFileSync(__dirname+'/static/branch-rules.js','utf8'),box);
 const reply=async(n,data,status=200)=>{requests[n].resolve({ok:status===200,status,json:async()=>data});await new Promise(r=>setImmediate(r))};
 const login=()=>{el('rules-token').value='secret';return el('rules-login').handlers.submit({preventDefault(){}})};
 return {el,requests,reply,login};
}
const settings={scopes:[{team_id:'T1',team_name:'One',slack_user_id:'U1'},{team_id:'T2',team_name:'Two',slack_user_id:'U2'}],rules:[]};
test('logout discards delayed rules response',async()=>{const h=harness();const pending=h.login();h.el('rules-logout').handlers.click();await h.reply(0,settings);await pending;assert.equal(h.el('rules-content').hidden,true);assert.equal(h.el('rules-team').children.length,0);assert.equal(h.el('rules-token').value,'')});
test('selected workspace limits users; saves exact custom rule without preset',async()=>{const h=harness();const pending=h.login();await h.reply(0,settings);await pending;assert.equal(h.el('rules-template').value,'');assert.deepEqual(h.el('rules-user').children.map(o=>o.value),['','U1']);h.el('rules-team').value='T2';h.el('rules-team').handlers.change();assert.deepEqual(h.el('rules-user').children.map(o=>o.value),['','U2']);h.el('rules-user').value='U2';h.el('rules-template').value='my-company/{slug}';h.el('rules-form').handlers.submit({preventDefault(){}});assert.equal(h.requests[1].options.method,'PUT');assert.deepEqual(JSON.parse(h.requests[1].options.body),{team_id:'T2',slack_user_id:'U2',template:'my-company/{slug}'});h.el('rules-logout').handlers.click();await h.reply(1,{});assert.equal(h.el('rules-content').hidden,true)});
