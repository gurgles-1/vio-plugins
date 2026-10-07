package main

const adminPageHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Cover Studio</title>
<style>
body{font-family:system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem;color:#e5e9f0;background:#0b0e14}
.card{background:#151a24;border:1px solid #2a3346;border-radius:8px;padding:1rem;margin-bottom:1rem}
button{background:#3b82f6;color:#fff;border:0;border-radius:6px;padding:.5rem 1rem;font-size:.95rem;cursor:pointer;margin-right:.5rem}
button:disabled{opacity:.5}
button.danger{background:#7f1d1d}
table{width:100%;border-collapse:collapse;font-size:.9rem}
th,td{text-align:left;padding:.45rem .5rem;border-bottom:1px solid #2a3346;vertical-align:middle}
th{color:#9aa4b2;font-weight:600}
select,input[type=checkbox]{accent-color:#3b82f6}
select{background:#0b0e14;color:#e5e9f0;border:1px solid #2a3346;border-radius:4px;padding:.25rem}
.ok{color:#4ade80}.bad{color:#f87171}.dim{color:#9aa4b2}
.preview{display:flex;gap:1rem;margin-top:1rem;flex-wrap:wrap}
.preview img{border:1px solid #2a3346;border-radius:6px;max-width:300px}
.preview figure{margin:0}.preview figcaption{font-size:.8rem;color:#9aa4b2;text-align:center;margin-top:.25rem}
#msg{min-height:1.4em;margin-top:.5rem}
.rowbtns{white-space:nowrap}
</style>
</head><body><h1>Cover Studio</h1>
<div class="card"><span id="msg" class="dim">loading…</span></div>
<div class="card"><h3 style="margin-top:0">Collections</h3>
<table><thead><tr><th></th><th>Collection</th><th>Overlay</th><th>Layout</th><th>Last build</th><th></th></tr></thead>
<tbody id="rows"></tbody></table></div>
<div class="card"><h3 style="margin-top:0">Preview</h3>
<div class="dim" style="font-size:.85rem;margin-bottom:.5rem">Select a collection, then render a live preview. Previews do not upload anything.</div>
<div><select id="pvsel"></select>
<button id="pvbtn">Render preview</button></div>
<div class="preview" id="pv"></div></div>
<script>
const $=id=>document.getElementById(id);
let state=null;
const overlays=[['logo','Logo'],['text','Text'],['none','None']];
const layouts=[['grid','Grid 3×2'],['spotlight','Spotlight'],['columns','Columns']];
function sel(name,val,opts){return '<select data-k="'+name+'">'+opts.map(o=>'<option value="'+o[0]+'"'+(o[0]===val?' selected':'')+'>'+o[1]+'</option>').join('')+'</select>'}
async function load(){
  try{
    const ctl=new AbortController();const to=setTimeout(()=>ctl.abort(),30000);
    const r=await fetch('/admin/cover-studio/status',{signal:ctl.signal});clearTimeout(to);
    if(!r.ok)throw new Error('status '+r.status);
    state=await r.json();
  }catch(e){$('msg').textContent='error: cannot reach plugin ('+(e.name==='AbortError'?'timeout':e.message)+') — is it running?';$('msg').className='bad';return}
  if(state.error){$('msg').textContent='error: '+state.error;$('msg').className='bad';return}
  $('msg').textContent='';$('msg').className='dim';
  const tb=$('rows');tb.innerHTML='';
  const ps=$('pvsel');ps.innerHTML='';
  (state.collections||[]).forEach(c=>{
    const tr=document.createElement('tr');
    const lb=c.last_build;
    const lbHtml=lb?('<span class="'+(lb.success?'ok':'bad')+'">'+(lb.success?'ok':'failed')+'</span> <span class="dim">'+lb.at.replace('T',' ').slice(0,16)+'</span>'+(lb.error?'<div class="bad" style="font-size:.8rem">'+lb.error.slice(0,120)+'</div>':'')+'<div class="dim" style="font-size:.8rem">'+(lb.members_used||0)+' posters · '+(lb.overlay_used||'')+(lb.backdrop_ok?' · backdrop':'')+'</div>'):'<span class="dim">never</span>';
    tr.innerHTML='<td><input type="checkbox" data-k="enabled"'+(c.enabled?' checked':'')+'></td>'
      +'<td>'+escapeHtml(c.title)+'<div class="dim" style="font-size:.8rem">id '+escapeHtml(c.id)+'</div></td>'
      +'<td>'+sel('overlay',c.overlay,overlays)+'</td>'
      +'<td>'+sel('layout',c.layout,layouts)+'</td>'
      +'<td>'+lbHtml+'</td>'
      +'<td class="rowbtns"><button data-act="save">Save</button><button data-act="rebuild">Rebuild now</button></td>';
    tr.dataset.id=c.id;
    tb.appendChild(tr);
    const opt=document.createElement('option');opt.value=c.id;opt.textContent=c.title;ps.appendChild(opt);
  });
  tb.querySelectorAll('button').forEach(b=>b.onclick=()=>rowAction(b));
}
function escapeHtml(s){return String(s).replace(/[&<>"']/g,m=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[m]))}
async function rowAction(btn){
  const tr=btn.closest('tr');const id=tr.dataset.id;
  btn.disabled=true;
  try{
    if(btn.dataset.act==='save'){
      const body={collection_id:id,
        enabled:tr.querySelector('[data-k=enabled]').checked,
        overlay_mode:tr.querySelector('[data-k=overlay]').value,
        layout:tr.querySelector('[data-k=layout]').value};
      const r=await fetch('/admin/cover-studio/options',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
      const j=await r.json();
      $('msg').textContent=j.error?('error: '+j.error):'saved';$('msg').className=j.error?'bad':'ok';
    }else{
      $('msg').textContent='rebuilding '+id+'…';$('msg').className='dim';
      const r=await fetch('/admin/cover-studio/rebuild?collection_id='+encodeURIComponent(id),{method:'POST'});
      const j=await r.json();
      $('msg').textContent=j.error?('error: '+j.error):('rebuilt: poster '+(j.poster_ok?'ok':'failed')+', backdrop '+(j.backdrop_ok?'ok':'skipped'));
      $('msg').className=j.error?'bad':'ok';
      load();
    }
  }finally{btn.disabled=false}
}
$('pvbtn').onclick=async()=>{
  const id=$('pvsel').value;if(!id)return;
  $('pv').innerHTML='<span class="dim">rendering…</span>';
  $('pv').innerHTML='<figure><img src="/admin/cover-studio/preview?collection_id='+encodeURIComponent(id)+'&kind=portrait"><figcaption>portrait 1000×1500</figcaption></figure>'
    +'<figure><img src="/admin/cover-studio/preview?collection_id='+encodeURIComponent(id)+'&kind=landscape"><figcaption>landscape 1920×1080</figcaption></figure>';
};
load();
</script></body></html>`
