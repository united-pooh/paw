import json,glob,sqlite3,re,pathlib,statistics,time
P=json.load(open('data/free_paraphrases.json')); c=sqlite3.connect('/Users/united_pooh/agent-history-search-mcp/data/history.db'); id2sid={i:pathlib.Path(json.loads(o)[0]).stem for i,o in c.execute('select id,occurrences from docs')}
docs=[]
for f in glob.glob('/Users/united_pooh/.codex/sessions/**/*.jsonl',recursive=True):
 if '/2026/09/12/' in f: continue
 txt=open(f,encoding='utf-8',errors='ignore').read().lower();docs.append((pathlib.Path(f).stem,txt))
# pure keyword scan: query is split into contiguous CJK bigrams / ASCII words; rank by matched distinct terms
out=[]
for x in P:
 q=x['query'].lower(); toks=list(dict.fromkeys(re.findall(r'[\u4e00-\u9fff]{2}|[a-z0-9_]{2,}',q)))
 t=time.perf_counter();sc=[]
 for sid,txt in docs:
  hit=sum(tok in txt for tok in toks)
  if hit:sc.append((hit,sid))
 sc.sort(key=lambda z:(-z[0],z[1])); ids=[sid for _,sid in sc[:5]];target=next((sid for sid,_ in docs if sid==id2sid[x['target']]),id2sid[x['target']]);r=(ids.index(target)+1 if target in ids else None);out.append((r,(time.perf_counter()-t)*1000))
print({'sessions':len(docs),'top1':sum(r==1 for r,_ in out)/60,'top5':sum(r is not None for r,_ in out)/60,'mrr':sum(1/r for r,_ in out if r)/60,'p50':statistics.median(t for _,t in out),'p95':sorted(t for _,t in out)[56]})
