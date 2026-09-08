package artifact

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TraceEntry is one line of a ghostchrome MCP session trace (JSONL).
type TraceEntry struct {
	TS         int64          `json:"ts"`
	Op         string         `json:"op"`
	Args       map[string]any `json:"args,omitempty"`
	OK         bool           `json:"ok"`
	Outcome    string         `json:"outcome,omitempty"`
	DurationMs int64          `json:"duration_ms"`
	Summary    string         `json:"summary,omitempty"`
	Error      string         `json:"error,omitempty"`
	Shot       string         `json:"shot,omitempty"`
}

// ReadTrace reads up to limit last entries from a JSONL trace file.
// limit <= 0 means all entries.
func ReadTrace(path string, limit int) ([]TraceEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []TraceEntry
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			line := data[start:i]
			start = i + 1
			if len(line) == 0 {
				continue
			}
			var e TraceEntry
			if json.Unmarshal(line, &e) == nil {
				entries = append(entries, e)
			}
		}
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// RenderTraceHTML produces a self-contained HTML viewer of a trace.
// No external deps; opens directly in a browser.
func RenderTraceHTML(entries []TraceEntry, sourcePath string) string {
	var totalMs int64
	okCount, errCount := 0, 0
	opCounts := map[string]int{}
	for _, e := range entries {
		totalMs += e.DurationMs
		opCounts[e.Op]++
		if e.OK {
			okCount++
		} else {
			errCount++
		}
	}
	opNames := make([]string, 0, len(opCounts))
	for k := range opCounts {
		opNames = append(opNames, k)
	}
	sort.Strings(opNames)

	var b strings.Builder
	fmt.Fprintf(&b, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<title>ghostchrome trace — %s</title>
<style>
:root { --bg:#0d1117; --panel:#161b22; --panel-2:#1c2128; --fg:#c9d1d9; --muted:#8b949e; --accent:#58a6ff; --ok:#3fb950; --err:#f85149; --border:#30363d; }
* { box-sizing:border-box; margin:0; padding:0; }
body { font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace; background:var(--bg); color:var(--fg); display:grid; grid-template-rows:auto 1fr; min-height:100vh; }
header { background:var(--panel); padding:12px 20px; border-bottom:1px solid var(--border); display:flex; gap:24px; align-items:center; flex-wrap:wrap; }
h1 { font-size:14px; font-weight:600; color:var(--accent); }
.stat { font-size:12px; color:var(--muted); }
.stat b { color:var(--fg); font-weight:600; }
main { display:grid; grid-template-columns:380px 1fr; min-height:0; }
.timeline { background:var(--panel); border-right:1px solid var(--border); overflow-y:auto; }
.entry { padding:10px 14px; border-bottom:1px solid var(--border); cursor:pointer; display:grid; grid-template-columns:auto 1fr auto; gap:8px; align-items:baseline; font-size:12px; transition:background .1s; }
.entry:hover { background:var(--panel-2); }
.entry.active { background:var(--panel-2); border-left:3px solid var(--accent); padding-left:11px; }
.entry .idx { color:var(--muted); font-size:10px; }
.entry .op { font-weight:600; color:var(--fg); }
.entry .op.err { color:var(--err); }
.entry .ms { color:var(--muted); font-size:10px; }
.entry .summary { grid-column:1/4; color:var(--muted); margin-top:3px; font-size:11px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.detail { padding:24px; overflow-y:auto; }
.detail h2 { font-size:18px; color:var(--accent); margin-bottom:8px; }
.detail .meta { font-size:12px; color:var(--muted); margin-bottom:20px; }
.detail .meta span { margin-right:14px; }
.section { background:var(--panel); border:1px solid var(--border); border-radius:8px; padding:14px 18px; margin-bottom:14px; }
.section h3 { font-size:11px; text-transform:uppercase; letter-spacing:.06em; color:var(--muted); margin-bottom:8px; }
pre { font-size:12px; line-height:1.5; white-space:pre-wrap; word-break:break-word; }
.shot img { max-width:100%%; border:1px solid var(--border); border-radius:6px; display:block; }
.empty { color:var(--muted); padding:48px; text-align:center; }
.badge { display:inline-block; padding:2px 8px; border-radius:10px; font-size:10px; font-weight:600; }
.badge.ok { background:rgba(63,185,80,.15); color:var(--ok); }
.badge.err { background:rgba(248,81,73,.15); color:var(--err); }
</style></head><body>
<header>
  <h1>ghostchrome trace</h1>
  <span class="stat"><b>%d</b> entries</span>
  <span class="stat"><b>%d</b> OK · <b>%d</b> errors</span>
  <span class="stat">total <b>%d ms</b></span>
  <span class="stat">source <b>%s</b></span>
</header>
<main>
  <aside class="timeline" id="timeline">`,
		html.EscapeString(filepath.Base(sourcePath)),
		len(entries), okCount, errCount, totalMs, html.EscapeString(sourcePath))

	for i, e := range entries {
		klass := ""
		if !e.OK {
			klass = " err"
		}
		fmt.Fprintf(&b, `<div class="entry" data-i="%d" onclick="show(%d)"><span class="idx">%03d</span><span class="op%s">%s</span><span class="ms">%dms</span><span class="summary">%s</span></div>`,
			i, i, i+1, klass, html.EscapeString(e.Op), e.DurationMs, html.EscapeString(e.Summary+e.Error))
	}

	b.WriteString(`</aside><section class="detail" id="detail"><div class="empty">Select an entry on the left.</div></section></main><script>const ENTRIES=`)

	enc, _ := json.Marshal(entries)
	b.Write(enc)

	b.WriteString(`;const SHOTS={};`)
	for i, e := range entries {
		if e.Shot == "" {
			continue
		}
		data, err := os.ReadFile(e.Shot)
		if err != nil || len(data) == 0 {
			continue
		}
		fmt.Fprintf(&b, `SHOTS[%d]=%q;`, i, "data:image/webp;base64,"+base64.StdEncoding.EncodeToString(data))
	}

	b.WriteString(`function show(i){
const e=ENTRIES[i],d=document.getElementById('detail');
document.querySelectorAll('.entry').forEach((n,j)=>n.classList.toggle('active',j===i));
const args=Object.keys(e.args||{}).length?'<pre>'+JSON.stringify(e.args,null,2).replace(/[<>&]/g,c=>({'<':'&lt;','>':'&gt;','&':'&amp;'}[c]))+'</pre>':'<pre>(none)</pre>';
const summary='<pre>'+(e.summary||e.error||'(no output)').replace(/[<>&]/g,c=>({'<':'&lt;','>':'&gt;','&':'&amp;'}[c]))+'</pre>';
const shotImg=SHOTS[i]?'<div class="section shot"><h3>screenshot (post-call)</h3><img src="'+SHOTS[i]+'"></div>':'';
const badge=e.ok?'<span class="badge ok">ok</span>':'<span class="badge err">error</span>';
d.innerHTML='<h2>['+String(i+1).padStart(3,'0')+'] '+e.op+'</h2><div class="meta"><span>'+badge+'</span><span>'+e.duration_ms+' ms</span><span>'+new Date(e.ts).toISOString()+'</span></div><div class="section"><h3>arguments</h3>'+args+'</div><div class="section"><h3>'+(e.ok?'summary':'error')+'</h3>'+summary+'</div>'+shotImg;
}
if(ENTRIES.length)show(0);
</script></body></html>`)
	return b.String()
}
