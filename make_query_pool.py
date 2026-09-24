import json,glob,re,pathlib,random
random.seed(42); rows=[]
for f in glob.glob('/Users/united_pooh/.codex/sessions/**/*.jsonl',recursive=True):
 if '/2026/09/12/' in f: continue
 texts=[]
 for line in open(f,encoding='utf-8',errors='ignore'):
  try:o=json.loads(line)
  except:continue
  it=o.get('payload',{}).get('item',{})
  for x in it.get('content',[]) if isinstance(it,dict) else []:
   if isinstance(x,dict) and x.get('type')=='Text': texts.append(x['text'])
 txt=re.sub(r'\s+',' ',' '.join(texts)).strip()
 if len(txt)<300:continue
 # candidate sentences with technical anchors
 for s in re.split(r'(?<=[。！？.!?])',txt):
  s=s.strip()
  if 12<=len(s)<=100 and re.search(r'(错误|失败|修复|测试|构建|检索|训练|模型|MCP|Token|CUDA|前端|数据库|延迟|崩溃|过期|看板|滚动)',s,re.I):
   rows.append({'query':s[:80],'source':f,'session_id':pathlib.Path(f).stem,'type':'anchor'})
# unique sessions, deterministic pool
seen=set();out=[]
for r in rows:
 if r['session_id'] in seen: continue
 seen.add(r['session_id']);out.append(r)
 if len(out)>=80:break
json.dump(out,open('data/query_pool.json','w'),ensure_ascii=False,indent=2)
print({'pool':len(out),'sessions':len(seen)})
