(() => {
 const el = id => document.getElementById(id);
 let token = '', settings = {scopes:[],rules:[]}, version = 0, previewVersion = 0, timer, busy = false;
 const status = text => { el('rules-status').textContent = text; };
 const reset = () => {
  el('rules-save').disabled=el('rules-remove').disabled=false;
  token=''; version++; previewVersion++; clearTimeout(timer); settings={scopes:[],rules:[]};busy=false;
  el('rules-content').classList.add('hidden'); el('rules-token').value='';
  el('rules-team').replaceChildren(); el('rules-user').replaceChildren();el('rules-template').value='';el('rules-preview').textContent='—';
  status('Kuralları görmek için bağlan.');
  updateSaveState();
 };
 async function api(method, body, preview=false) {
  const response = await fetch('/api/dashboard/branch-rules'+(preview?'/preview':''), {method, headers:{'X-Dashboard-Token':token,'Content-Type':'application/json'}, ...(body?{body:JSON.stringify(body)}:{})});
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || 'İşlem tamamlanamadı.');
  return data;
 }
 function option(value,text) {const o=document.createElement('option');o.value=value;o.textContent=text;return o;}
 function scope() {return {team_id:el('rules-team').value,slack_user_id:el('rules-user').value};}
 function currentRule() {
  const s=scope();return settings.rules.find(r=>r.team_id===s.team_id && r.slack_user_id===s.slack_user_id);
 }
 function updateSaveState() {
  const rule=currentRule(),value=el('rules-template').value;
  const saved=Boolean(rule && value===rule.template),dirty=value!==(rule?.template || '');
  const button=el('rules-save'),feedback=el('rules-save-state');
  button.textContent=busy?'Kaydediliyor…':saved?'✓ Kaydedildi':rule?'Değişiklikleri kaydet':'Kuralı kaydet';
  button.disabled=busy || !dirty || !value;
  button.classList.toggle('is-saved',saved && !busy);
  feedback.textContent=busy?'Değişiklikler kaydediliyor…':saved?'Bu kural kaydedildi.':dirty?'Kaydedilmemiş değişiklikler var.':'Henüz özel kural yok.';
  feedback.classList.toggle('is-saved',saved && !busy);
  el('rules-remove').disabled=busy || !rule;
  for(const id of ['rules-template','rules-team','rules-user']) el(id).disabled=busy;
 }
 function selectUsers() {
  const team=el('rules-team').value;
  el('rules-user').replaceChildren(option('','Workspace’in tamamı'));
  for(const row of settings.scopes.filter(s=>s.team_id===team && s.slack_user_id)) el('rules-user').append(option(row.slack_user_id,'Kişi · '+row.slack_user_id));
  selectRule();
 }
 function selectRule() {
  const s=scope(),rule=settings.rules.find(r=>r.team_id===s.team_id && r.slack_user_id===s.slack_user_id);
  const workspace=settings.rules.find(r=>r.team_id===s.team_id && !r.slack_user_id);
  el('rules-template').value=rule?.template || '';
  el('rules-priority').textContent=s.slack_user_id && workspace?'Workspace kuralı öncelikli: '+workspace.template:'Bu kapsam için kaydedilen kural yeni görevlerde kullanılır.';
  updateSaveState();
  preview();
 }
 async function preview() {
  clearTimeout(timer);const n=++previewVersion,session=version;
  const template=el('rules-template').value;
  if(!template){el('rules-preview').textContent='Henüz özel kural yok.';return;}
  el('rules-preview').textContent='Kontrol ediliyor…';
  try{const data=await api('POST',{template},true);if(n===previewVersion && session===version)el('rules-preview').textContent=data.example;}
  catch(err){if(n===previewVersion && session===version)el('rules-preview').textContent=err.message;}
 }
 async function load() {
  const session=version;const data=await api('GET');if(session!==version)return;
  settings=data;const old=scope();el('rules-team').replaceChildren();
  const teams=new Map(settings.scopes.map(s=>[s.team_id,s.team_name]));
  for(const [id,name] of teams)el('rules-team').append(option(id,name+' · '+id));
  if(teams.has(old.team_id))el('rules-team').value=old.team_id;
  selectUsers();if(settings.scopes.some(s=>s.team_id===old.team_id && s.slack_user_id===old.slack_user_id)){el('rules-user').value=old.slack_user_id;selectRule();}
  el('rules-content').classList.toggle('hidden',teams.size===0);
  status(teams.size?'Kurallar yüklendi.':'Önce Nudgy’yi bir Slack workspace’ine bağla.');
 }
 async function save(remove) {
  if(busy)return;
  const previous=currentRule(),value=el('rules-template').value;
  if(remove?!previous:(!value || value===previous?.template))return;
  const payload={...scope(),template:remove?'':value};
  busy=true;updateSaveState();const session=version;
  try{
   await api('PUT',payload);if(session!==version)return;
   // Only a confirmed write changes the saved baseline, never a preview.
   settings.rules=settings.rules.filter(r=>r.team_id!==payload.team_id || r.slack_user_id!==payload.slack_user_id);
   if(!remove)settings.rules.push(payload);
   selectRule();
   status(remove?'Kural kaldırıldı. Yeni görevlerde üst kural veya varsayılan format kullanılacak.':'Kural kaydedildi. Yeni görevlerde geçerli olacak.');
  }
  catch(err){if(session===version)status(err.message);}
  finally{if(session===version){busy=false;updateSaveState();}}
 }
 el('rules-login').addEventListener('submit',async e=>{e.preventDefault();const next=el('rules-token').value.trim();reset();token=next;const session=version;try{await load();}catch(err){if(session===version){reset();status(err.message);}}});
 el('rules-logout').addEventListener('click',reset);
 el('rules-team').addEventListener('change',selectUsers);el('rules-user').addEventListener('change',selectRule);
 el('rules-template').addEventListener('input',()=>{updateSaveState();status('Şablonu düzenledikten sonra değişikliklerini kaydet.');clearTimeout(timer);previewVersion++;timer=setTimeout(preview,250);});
 el('rules-form').addEventListener('submit',e=>{e.preventDefault();save(false);});el('rules-remove').addEventListener('click',()=>save(true));
})();
