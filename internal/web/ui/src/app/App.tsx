export function App() {
  return (
    <main className="workbench-shell">
      <aside className="workspace-rail">
        <strong>Paw</strong>
        <button type="button">新会话</button>
      </aside>
      <section className="conversation-pane">
        <header><h1>浏览器工作台</h1><span>本地连接</span></header>
        <div className="empty-state">选择工作区与会话，开始与智能体协作。</div>
        <form className="composer"><textarea aria-label="消息" placeholder="给智能体发消息" /><button type="submit">发送</button></form>
      </section>
    </main>
  );
}
