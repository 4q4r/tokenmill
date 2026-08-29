import { createSignal, createEffect, onCleanup } from "solid-js"

export function TokenMillStats() {
  const [data, setData] = createSignal<any>(null)
  const fetchData = async () => {
    try {
      const res = await fetch("/api/tokenmill/gain?format=json").then(r=>r.json()).catch(async ()=>{
        // fallback to shelling tokenmill if API not available
        // @ts-ignore - opencode $ is available in plugin context but not here; graceful
        return null
      })
      if (res) setData(res)
    } catch (e) {
      if ((globalThis as any).TOKENMILL_DEBUG) console.debug("[tokenmill-tui] fetch fail-open", e)
    }
  }
  createEffect(() => {
    fetchData()
    const id = setInterval(fetchData, 30*60*1000)
    onCleanup(()=>clearInterval(id))
  })
  return (
    <div style={{ padding: "8px", "font-size": "12px" }}>
      <div style={{ "font-weight": "600", "margin-bottom": "4px" }}>TokenMill</div>
      {data() ? (
        <div>
          <div>Saved: {data().summary?.total_saved ?? 0} ({(data().summary?.avg_savings_pct ?? 0).toFixed(1)}%)</div>
          <div style={{ background: "#eee", height: "6px", "border-radius": "3px", overflow: "hidden", margin: "4px 0" }}>
            <div style={{ width: String(Math.min(100, data().summary?.avg_savings_pct ?? 0)) + "%", background: "#4ade80", height: "100%" }} />
          </div>
          <div style={{ opacity: 0.6 }}>Commands: {data().summary?.total_commands ?? 0}</div>
        </div>
      ) : (
        <div style={{ opacity: 0.6 }}>No data — run <code>tokenmill gain</code></div>
      )}
    </div>
  )
}

// Register as sidebar_content slot order 40 (graceful if slot not present)
export default {
  sidebar_content: {
    order: 40,
    component: TokenMillStats,
  },
}
