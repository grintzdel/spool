(() => {
  const rows = document.getElementById("rows");
  const live = document.getElementById("live");
  const flash = document.getElementById("flash");
  const refresh = (el) => window.htmx && htmx.trigger(el, "refresh");

  if (window.EventSource && (rows || live)) {
    const source = new EventSource("/api/events");
    const onEvent = (e) => {
      if (rows) return refresh(rows);
      try {
        if (JSON.parse(e.data).request_id === live.dataset.id) refresh(live);
      } catch {}
    };
    for (const kind of ["request", "attempt", "verification"]) source.addEventListener(kind, onEvent);
    source.onopen = () => document.body.classList.add("connected");
    source.onerror = () => document.body.classList.remove("connected");
  }

  const show = (text, failed) => {
    if (!flash) return;
    flash.textContent = text;
    flash.classList.toggle("err", failed);
    flash.hidden = false;
  };

  const summarize = (data) => {
    if (data.attempts) {
      const lines = data.attempts.map((a) =>
        `${a.kind} → ${a.target_url}: ${a.error || a.status_code} in ${a.duration_ms.toFixed(1)}ms${a.resigned ? " (re-signed)" : ""}`);
      return lines.concat((data.warnings || []).map((w) => "warning: " + w)).join("\n");
    }
    return `${data.reason}: ${data.meaning}`;
  };

  document.addEventListener("click", async (e) => {
    const copy = e.target.closest("[data-copy]");
    if (copy) {
      const text = document.getElementById(copy.dataset.copy).textContent;
      try {
        await navigator.clipboard.writeText(text);
        copy.textContent = "Copied";
      } catch {
        copy.textContent = "Copy failed";
      }
      setTimeout(() => (copy.textContent = "Copy"), 1500);
      return;
    }

    const btn = e.target.closest("[data-post]");
    if (!btn) return;
    btn.disabled = true;
    try {
      const res = await fetch(btn.dataset.post, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: btn.dataset.body || "{}",
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || res.statusText);
      show(summarize(data), data.reason !== undefined && !data.ok);
      if (live) refresh(live);
    } catch (err) {
      show(err.message, true);
    } finally {
      btn.disabled = false;
    }
  });
})();
