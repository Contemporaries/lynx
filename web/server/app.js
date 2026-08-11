(function () {
  const LS_BASE = "lynx.api.base";
  const LS_SECRET = "lynx.api.secret";

  function $(id) { return document.getElementById(id); }

  function settings() {
    return {
      base: (localStorage.getItem(LS_BASE) || "http://127.0.0.1:9090").replace(/\/$/, ""),
      secret: localStorage.getItem(LS_SECRET) || "",
    };
  }

  async function api(path, opts = {}) {
    const s = settings();
    const headers = Object.assign({ "Content-Type": "application/json" }, opts.headers || {});
    if (s.secret) headers.Authorization = "Bearer " + s.secret;
    const res = await fetch(s.base + path, Object.assign({}, opts, { headers }));
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
    if (!res.ok) throw new Error((data && data.error) || res.statusText || String(res.status));
    return data;
  }

  function showPage(name) {
    document.querySelectorAll(".page").forEach((el) => el.classList.toggle("active", el.id === name));
    document.querySelectorAll("nav button").forEach((btn) => btn.classList.toggle("active", btn.dataset.page === name));
    if (name === "overview") loadOverview();
    if (name === "devices") loadDevices();
    if (name === "config") loadConfig();
    if (name === "logs") startLogs();
  }

  document.querySelectorAll("nav button").forEach((btn) => {
    btn.addEventListener("click", () => showPage(btn.dataset.page));
  });

  async function loadOverview() {
    const body = $("overview-body");
    body.innerHTML = "<div class='card'><div class='k'>Loading…</div></div>";
    try {
      const [ver, st] = await Promise.all([api("/api/v1/version"), api("/api/v1/status")]);
      const rows = Object.assign({}, ver, st);
      body.innerHTML = Object.keys(rows).map((k) =>
        `<div class="card"><div class="k">${k}</div><div class="v">${escapeHtml(String(rows[k]))}</div></div>`
      ).join("");
    } catch (e) {
      body.innerHTML = `<div class="card"><div class="k">Error</div><div class="v">${escapeHtml(e.message)}</div></div>`;
    }
  }

  async function loadDevices() {
    const body = $("devices-body");
    try {
      const clients = await api("/api/v1/clients");
      const names = Object.keys(clients || {});
      if (!names.length) {
        body.innerHTML = "<p class='lede'>No devices in config.</p>";
        return;
      }
      body.innerHTML = `<table><thead><tr><th>Name</th><th>Enabled</th><th>Fingerprint</th></tr></thead><tbody>` +
        names.map((n) => {
          const c = clients[n];
          return `<tr><td>${escapeHtml(n)}</td><td>${c.enabled ? "yes" : "no"}</td><td><code>${escapeHtml(c.certificate_sha256 || "")}</code></td></tr>`;
        }).join("") + `</tbody></table>`;
    } catch (e) {
      body.innerHTML = `<pre class="msg">${escapeHtml(e.message)}</pre>`;
    }
  }

  async function loadConfig() {
    try {
      const cfg = await api("/api/v1/config");
      $("config-json").value = JSON.stringify(cfg, null, 2);
    } catch (e) {
      $("cfg-msg").textContent = e.message;
    }
  }

  async function saveConfig(restart) {
    $("cfg-msg").textContent = "Saving…";
    try {
      const body = $("config-json").value;
      JSON.parse(body);
      const res = await api("/api/v1/config", { method: "PUT", body });
      $("cfg-msg").textContent = JSON.stringify(res, null, 2);
      if (restart) {
        await api("/api/v1/service/restart", { method: "POST", body: "{}" });
        $("cfg-msg").textContent += "\nRestart requested; polling health…";
        await waitHealthy();
        $("cfg-msg").textContent += "\nBack online.";
      }
    } catch (e) {
      $("cfg-msg").textContent = e.message;
    }
  }

  async function waitHealthy() {
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 500));
      try {
        const s = settings();
        const res = await fetch(s.base + "/api/v1/health");
        if (res.ok) return;
      } catch {}
    }
    throw new Error("health check timed out");
  }

  $("cfg-save").addEventListener("click", () => saveConfig(false));
  $("cfg-save-restart").addEventListener("click", () => saveConfig(true));

  $("svc-restart").addEventListener("click", async () => {
    $("svc-msg").textContent = "Restarting…";
    try {
      await api("/api/v1/service/restart", { method: "POST", body: "{}" });
      await waitHealthy();
      $("svc-msg").textContent = "Service is back.";
    } catch (e) {
      $("svc-msg").textContent = e.message;
    }
  });

  $("svc-upgrade").addEventListener("click", async () => {
    $("svc-msg").textContent = "Starting upgrade…";
    try {
      const tag = $("upgrade-tag").value.trim();
      await api("/api/v1/upgrade", { method: "POST", body: JSON.stringify({ tag }) });
      for (let i = 0; i < 120; i++) {
        await new Promise((r) => setTimeout(r, 1000));
        const st = await api("/api/v1/upgrade/status");
        $("svc-msg").textContent = JSON.stringify(st, null, 2);
        if (st.state === "done" || st.state === "error") break;
      }
    } catch (e) {
      $("svc-msg").textContent = e.message;
    }
  });

  let es = null;
  function startLogs() {
    if (es) { es.close(); es = null; }
    const s = settings();
    const level = $("log-level").value;
    const view = $("log-view");
    view.textContent = "";
    // EventSource cannot set Authorization; use fetch stream fallback via query is not supported.
    // Use fetch SSE with headers.
    const ctrl = new AbortController();
    window.__lynxLogAbort = ctrl;
    fetch(s.base + "/api/v1/logs?level=" + encodeURIComponent(level), {
      headers: s.secret ? { Authorization: "Bearer " + s.secret } : {},
      signal: ctrl.signal,
    }).then(async (res) => {
      if (!res.ok) throw new Error("logs " + res.status);
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const parts = buf.split("\n\n");
        buf = parts.pop();
        for (const part of parts) {
          const line = part.split("\n").find((l) => l.startsWith("data: "));
          if (!line) continue;
          try {
            const entry = JSON.parse(line.slice(6));
            view.textContent += `${entry.time} ${entry.level}: ${entry.message}\n`;
            view.scrollTop = view.scrollHeight;
          } catch {}
        }
      }
    }).catch((e) => {
      if (e.name !== "AbortError") view.textContent += "error: " + e.message + "\n";
    });
  }
  $("log-level").addEventListener("change", startLogs);

  $("api-base").value = settings().base;
  $("api-secret").value = settings().secret;
  $("settings-save").addEventListener("click", () => {
    localStorage.setItem(LS_BASE, $("api-base").value.trim());
    localStorage.setItem(LS_SECRET, $("api-secret").value);
    showPage("overview");
  });

  function escapeHtml(s) {
    return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  showPage("overview");
})();
