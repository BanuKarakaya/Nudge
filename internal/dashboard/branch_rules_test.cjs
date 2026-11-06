const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function harness(){
 const elements=new Map(),requests=[];
 function el(id){if(!elements.has(id))elements.set(id,{value:'',textContent:'',disabled:false,children:[],hidden:false,handlers:{},classList:{add(){el(id).hidden=true},toggle(_,on){el(id).hidden=on}},replaceChildren(...c){this.children=c;this.value=c[0]?.value||''},append(c){this.children.push(c);if(this.children.length===1)this.value=c.value},addEventListener(n,f){this.handlers[n]=f}});return elements.get(id)}
 const box=vm.createContext({Map,JSON,Error,setTimeout,clearTimeout,document:{getElementById:el,createElement:()=>({value:'',textContent:''})},fetch:(url,options)=>new Promise(resolve=>requests.push({url,options,resolve}))});
 vm.runInContext(fs.readFileSync(__dirname+'/static/branch-rules.js','utf8'),box);
 const reply=async(n,data,status=200)=>{requests[n].resolve({ok:status===200,status,json:async()=>JSON.parse(JSON.stringify(data))});await new Promise(r=>setImmediate(r))};
 const login=()=>{el('rules-token').value='secret';return el('rules-login').handlers.submit({preventDefault(){}})};
 return {el,requests,reply,login};
}
const settings={scopes:[{team_id:'T1',team_name:'One',slack_user_id:'U1'},{team_id:'T2',team_name:'Two',slack_user_id:'U2'}],rules:[]};
test('logout discards delayed rules response',async()=>{const h=harness();const pending=h.login();h.el('rules-logout').handlers.click();await h.reply(0,settings);await pending;assert.equal(h.el('rules-content').hidden,true);assert.equal(h.el('rules-team').children.length,0);assert.equal(h.el('rules-token').value,'')});
test('selected workspace limits users; saves exact custom rule without preset',async()=>{const h=harness();const pending=h.login();await h.reply(0,settings);await pending;assert.equal(h.el('rules-template').value,'');assert.deepEqual(h.el('rules-user').children.map(o=>o.value),['','U1']);h.el('rules-team').value='T2';h.el('rules-team').handlers.change();assert.deepEqual(h.el('rules-user').children.map(o=>o.value),['','U2']);h.el('rules-user').value='U2';h.el('rules-template').value='my-company/{slug}';h.el('rules-form').handlers.submit({preventDefault(){}});assert.equal(h.requests[1].options.method,'PUT');assert.deepEqual(JSON.parse(h.requests[1].options.body),{team_id:'T2',slack_user_id:'U2',template:'my-company/{slug}'});h.el('rules-logout').handlers.click();await h.reply(1,{});assert.equal(h.el('rules-content').hidden,true)});
test('confirmed save stays saved until edited, reverting restores saved state',async()=>{
 const h=harness(),pending=h.login();await h.reply(0,settings);await pending;
 const field=h.el('rules-template'),button=h.el('rules-save');
 field.value='nudgy/{slug}';field.handlers.input();
 assert.equal(button.disabled,false);
 h.el('rules-form').handlers.submit({preventDefault(){}});
 assert.equal(button.textContent,'Kaydediliyor…');assert.equal(field.disabled,true);
 await h.reply(1,{});
 assert.equal(button.textContent,'✓ Kaydedildi');assert.equal(button.disabled,true);
 assert.equal(h.el('rules-save-state').textContent,'Bu kural kaydedildi.');
 field.value='feature/{slug}';field.handlers.input();
 assert.equal(button.textContent,'Değişiklikleri kaydet');assert.equal(button.disabled,false);
 assert.equal(h.el('rules-save-state').textContent,'Kaydedilmemiş değişiklikler var.');
 field.value='nudgy/{slug}';field.handlers.input();assert.equal(button.textContent,'✓ Kaydedildi');assert.equal(button.disabled,true);
 h.el('rules-logout').handlers.click();
});
test('failed save never shows saved and keeps retry available',async()=>{
 const h=harness(),pending=h.login();await h.reply(0,settings);await pending;
 h.el('rules-template').value='feature/{slug}';h.el('rules-form').handlers.submit({preventDefault(){}});
 await h.reply(1,{error:'Kaydedilemedi.'},500);
 assert.equal(h.el('rules-save').textContent,'Kuralı kaydet');assert.equal(h.el('rules-save').disabled,false);
 assert.equal(h.el('rules-template').value,'feature/{slug}');assert.equal(h.el('rules-status').textContent,'Kaydedilemedi.');
 h.el('rules-logout').handlers.click();
});
