#!/usr/bin/env python3
"""Auditable chunk-level lexical retrieval prototype for history experiments."""
import argparse, json, sqlite3, re

def build(corpus, dbpath):
 db=sqlite3.connect(dbpath); db.executescript('DROP TABLE IF EXISTS chunks; DROP TABLE IF EXISTS chunks_fts; CREATE TABLE chunks(id INTEGER PRIMARY KEY,session_id TEXT,source TEXT,text TEXT); CREATE VIRTUAL TABLE chunks_fts USING fts5(text,content="chunks",content_rowid="id");')
 for line in open(corpus,encoding='utf-8'):
  d=json.loads(line); db.execute('INSERT INTO chunks VALUES(?,?,?,?)',(d['id'],d['session_id'],d['source'],d['text'])); db.execute('INSERT INTO chunks_fts(rowid,text) VALUES(?,?)',(d['id'],d['text']))
 db.commit(); return db

ALIASES={'不跟手':['卡顿','延迟','滚动','响应慢','响应不灵敏'],'响应不灵敏':['不跟手','卡顿','延迟','滚动'],'卡顿':['不跟手','延迟','掉帧'],'陈旧':['未更新','过期','新鲜度','stale'],'新鲜度':['陈旧','未更新','构建产物','dist'],'思考不对齐':['completion_mismatch','reasoning','rawReasoning'],'崩溃':['错误','异常','crash']}
def fetch(source):
 return open(source,encoding='utf-8',errors='ignore').read()

def search(db,q,k=5):
 toks=re.findall(r'[\w\u4e00-\u9fff]+',q)
 for t in list(toks): toks.extend(ALIASES.get(t, []))
 toks=list(dict.fromkeys(toks))
 match=' OR '.join('"'+x+'"' for x in toks)
 rows=db.execute('SELECT c.session_id,c.source,c.text FROM chunks_fts f JOIN chunks c ON c.id=f.rowid WHERE chunks_fts MATCH ? ORDER BY rank LIMIT ?', (match,k*8)).fetchall()
 out=[]
 for r in rows:
  if r[0] not in [x[0] for x in out]: out.append(r)
 return out[:k]
if __name__=='__main__':
 p=argparse.ArgumentParser();p.add_argument('--corpus',required=True);p.add_argument('--db',required=True);p.add_argument('query');a=p.parse_args();db=build(a.corpus,a.db)
 for x in search(db,a.query): print(json.dumps({'session_id':x[0],'source':x[1],'evidence':x[2][:500]},ensure_ascii=False))
