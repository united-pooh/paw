#!/usr/bin/env python3
"""Build auditable chunk corpus for agent-history-search-mcp experiments."""
import argparse, glob, json, pathlib, re

def main():
 p=argparse.ArgumentParser(); p.add_argument('--input',required=True); p.add_argument('--output',required=True); p.add_argument('--chars',type=int,default=1800); p.add_argument('--stride',type=int,default=1400); p.add_argument('--exclude',action='append',default=[],help='substring; files containing it are skipped'); a=p.parse_args()
 files=sorted(glob.glob(str(pathlib.Path(a.input)/'**/*.jsonl'),recursive=True)); out=[]; cid=0
 for f in files:
  if any(x in f for x in a.exclude): continue
  texts=[]
  for line in open(f,encoding='utf-8',errors='ignore'):
   try:o=json.loads(line)
   except: continue
   item=o.get('payload',{}).get('item',{})
   for x in item.get('content',[]) if isinstance(item,dict) else []:
    if isinstance(x,dict) and x.get('type')=='Text' and x.get('text'): texts.append(x['text'])
  text=re.sub(r'\s+',' ',' '.join(texts)).strip()
  if not text: continue
  session=pathlib.Path(f).stem
  for start in range(0,len(text),a.stride):
   chunk=text[start:start+a.chars]
   if len(chunk)<200: break
   out.append({'id':cid,'session_id':session,'source':f,'char_start':start,'char_end':start+len(chunk),'text':chunk}); cid+=1
 with open(a.output,'w',encoding='utf-8') as h:
  for row in out: h.write(json.dumps(row,ensure_ascii=False)+'\n')
 print(json.dumps({'sessions':len({x['session_id'] for x in out}),'chunks':len(out),'chars':sum(len(x['text']) for x in out)},ensure_ascii=False))
if __name__=='__main__': main()
