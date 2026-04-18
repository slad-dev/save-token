package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"agent-gateway/internal/gateway"
	"agent-gateway/internal/localapp"
)

type LocalAppHandler struct {
	service *localapp.Service
	reload  func(context.Context) error
}

func NewLocalAppHandler(service *localapp.Service, reload func(context.Context) error) *LocalAppHandler {
	return &LocalAppHandler{
		service: service,
		reload:  reload,
	}
}

func (h *LocalAppHandler) Page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(localAppHTML))
}

func (h *LocalAppHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	snapshot, err := h.service.Snapshot(r.Context(), localProxyBaseURL(r))
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "local_mode_error", err.Error())
		return
	}
	writeLocalJSON(w, http.StatusOK, snapshot)
}

func (h *LocalAppHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	defer r.Body.Close()

	var payload struct {
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Strategy string `json:"strategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	if _, err := h.service.Save(r.Context(), localapp.AppConfig{
		BaseURL:  payload.BaseURL,
		APIKey:   payload.APIKey,
		Strategy: payload.Strategy,
	}); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if h.reload != nil {
		if err := h.reload(r.Context()); err != nil {
			gateway.WriteJSONError(w, http.StatusInternalServerError, "reload_error", err.Error())
			return
		}
	}

	snapshot, err := h.service.Snapshot(r.Context(), localProxyBaseURL(r))
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "local_mode_error", err.Error())
		return
	}
	writeLocalJSON(w, http.StatusOK, snapshot)
}

func localProxyBaseURL(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:8080"
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "http://" + host
}

func writeLocalJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

const localAppHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Save Token Gateway</title>
  <style>
    :root {
      --bg: #f7f1e8;
      --panel: rgba(255, 255, 255, 0.92);
      --line: rgba(27, 37, 54, 0.12);
      --text: #182031;
      --muted: #5d687b;
      --accent: #cb6a2d;
      --accent-soft: rgba(203, 106, 45, 0.12);
      --ok: #19714a;
      --warn: #b66b16;
      --danger: #a33131;
      --shadow: 0 20px 48px rgba(24, 32, 49, 0.12);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      color: var(--text);
      font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
      background:
        radial-gradient(circle at top right, rgba(203, 106, 45, 0.18), transparent 32%),
        linear-gradient(160deg, #faf5ee 0%, #f0e5d7 100%);
    }
    .shell {
      max-width: 1240px;
      margin: 0 auto;
      padding: 28px 18px 48px;
    }
    .hero, .layout {
      display: grid;
      gap: 22px;
    }
    .hero {
      grid-template-columns: minmax(0, 1.25fr) minmax(320px, 0.75fr);
      margin-bottom: 22px;
    }
    .layout {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 24px;
      box-shadow: var(--shadow);
      backdrop-filter: blur(8px);
      padding: 24px;
    }
    .hero-main h1 {
      margin: 0 0 12px;
      font-size: clamp(30px, 4vw, 52px);
      line-height: 1.04;
      letter-spacing: -0.04em;
    }
    .hero-main p, .hero-side p, .note, .helper, .meta, .empty {
      color: var(--muted);
      line-height: 1.7;
      margin: 0;
    }
    .pill {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 8px 14px;
      border-radius: 999px;
      background: var(--accent-soft);
      color: var(--accent);
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      margin-bottom: 16px;
    }
    .hero-side {
      background:
        linear-gradient(180deg, rgba(23, 32, 51, 0.96), rgba(33, 43, 62, 0.92)),
        linear-gradient(120deg, rgba(203, 106, 45, 0.18), transparent 60%);
      color: #f8fafc;
    }
    .hero-side p, .hero-side .helper {
      color: rgba(248, 250, 252, 0.8);
    }
    .toolbar {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      gap: 12px;
      margin-bottom: 18px;
      flex-wrap: wrap;
    }
    h2 {
      margin: 0 0 8px;
      font-size: 24px;
    }
    form {
      display: grid;
      gap: 16px;
    }
    label {
      display: grid;
      gap: 8px;
      font-size: 14px;
      font-weight: 600;
    }
    input, textarea {
      width: 100%;
      padding: 14px 16px;
      border: 1px solid rgba(24, 32, 49, 0.14);
      border-radius: 16px;
      background: rgba(255, 255, 255, 0.86);
      color: var(--text);
      font-size: 14px;
      font-family: inherit;
    }
    textarea {
      min-height: 132px;
      resize: vertical;
    }
    input:focus, textarea:focus {
      outline: none;
      border-color: rgba(203, 106, 45, 0.9);
      box-shadow: 0 0 0 4px rgba(203, 106, 45, 0.12);
    }
    .row {
      display: flex;
      gap: 12px;
      align-items: center;
      flex-wrap: wrap;
    }
    button {
      appearance: none;
      border: 0;
      border-radius: 999px;
      padding: 13px 20px;
      cursor: pointer;
      font-size: 14px;
      font-weight: 700;
      color: #fff;
      background: linear-gradient(135deg, #cb6a2d, #b75522);
      box-shadow: 0 16px 30px rgba(183, 85, 34, 0.22);
    }
    button.secondary {
      background: rgba(24, 32, 49, 0.08);
      color: var(--text);
      box-shadow: none;
    }
    button:disabled {
      opacity: 0.7;
      cursor: wait;
    }
    .status {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 8px 12px;
      border-radius: 999px;
      font-size: 12px;
      font-weight: 700;
      background: rgba(25, 113, 74, 0.12);
      color: var(--ok);
    }
    .status.pending {
      background: rgba(182, 107, 22, 0.12);
      color: var(--warn);
    }
    .status.bad {
      background: rgba(163, 49, 49, 0.12);
      color: var(--danger);
    }
    .flash {
      min-height: 20px;
      font-size: 13px;
      font-weight: 600;
    }
    .flash.ok {
      color: var(--ok);
    }
    .flash.error {
      color: var(--danger);
    }
    .strategy-grid {
      display: grid;
      gap: 12px;
    }
    .strategy {
      display: grid;
      gap: 8px;
      position: relative;
      padding: 16px 18px 16px 50px;
      border-radius: 18px;
      border: 1px solid rgba(24, 32, 49, 0.12);
      background: rgba(255, 255, 255, 0.84);
      cursor: pointer;
      transition: transform 140ms ease, box-shadow 140ms ease, border-color 140ms ease;
    }
    .strategy:hover {
      transform: translateY(-1px);
      border-color: rgba(203, 106, 45, 0.44);
      box-shadow: 0 14px 28px rgba(24, 32, 49, 0.08);
    }
    .strategy input {
      position: absolute;
      left: 18px;
      top: 18px;
      margin: 0;
      width: 18px;
      height: 18px;
    }
    .strategy strong {
      font-size: 16px;
    }
    .strategy span {
      color: var(--muted);
      font-size: 13px;
      line-height: 1.7;
    }
    .mono, pre {
      margin: 0;
      font-family: "Consolas", "SFMono-Regular", monospace;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .codebox, .output {
      padding: 16px;
      border-radius: 18px;
      background: rgba(24, 32, 49, 0.06);
      font-size: 13px;
      line-height: 1.7;
    }
    .hero-side .codebox {
      background: rgba(255, 255, 255, 0.08);
      color: #f8fafc;
    }
    .metrics {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 14px;
    }
    .metric {
      display: grid;
      gap: 4px;
      padding: 14px;
      border-radius: 18px;
      background: rgba(24, 32, 49, 0.04);
    }
    .metric b {
      font-size: 26px;
      letter-spacing: -0.03em;
    }
    .metric span {
      font-size: 12px;
      color: var(--muted);
    }
    .request-list {
      display: grid;
      gap: 14px;
    }
    .request-item {
      display: grid;
      gap: 10px;
      padding: 16px;
      border-radius: 18px;
      background: rgba(24, 32, 49, 0.04);
      border: 1px solid rgba(24, 32, 49, 0.06);
    }
    .request-head {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: flex-start;
      flex-wrap: wrap;
    }
    .chips {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
    .chip {
      display: inline-flex;
      align-items: center;
      padding: 6px 10px;
      border-radius: 999px;
      background: rgba(24, 32, 49, 0.08);
      font-size: 12px;
      font-weight: 700;
    }
    .chip.ok {
      background: rgba(25, 113, 74, 0.12);
      color: var(--ok);
    }
    .chip.warn {
      background: rgba(182, 107, 22, 0.12);
      color: var(--warn);
    }
    .chip.bad {
      background: rgba(163, 49, 49, 0.12);
      color: var(--danger);
    }
    .request-grid {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 10px;
    }
    .request-grid .metric b {
      font-size: 18px;
    }
    .wide {
      margin-top: 22px;
    }
    @media (max-width: 980px) {
      .hero, .layout, .metrics, .request-grid {
        grid-template-columns: 1fr;
      }
    }
    @media (max-width: 720px) {
      .shell {
        padding: 16px 12px 36px;
      }
      .card {
        padding: 18px;
      }
    }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <section class="card hero-main">
        <div class="pill">Save Token Gateway</div>
        <h1>先配置上游，再把你的应用切到本地网关。</h1>
        <p>这个程序不是聊天站点，而是你本地的 token 节省代理。你在这里填写上游的 <code>base_url</code> 和 <code>api_key</code>，之后自己的应用只需要改连本地地址，缓存、瘦身、滑窗和转发都会在本地完成。</p>
      </section>
      <aside class="card hero-side">
        <h2>如何调用本地网关</h2>
        <p>页面里保存的是上游站点的密钥。你后续真正对接时，<strong>Base URL 改成本地地址</strong>，<strong>API Key 只要非空即可</strong>，因为本地网关会替你转发到你配置的上游。</p>
        <div class="codebox mono" id="quick-start">加载中...</div>
      </aside>
    </section>

    <section class="layout">
      <section class="card">
        <div class="toolbar">
          <div>
            <h2>1. 上游配置</h2>
            <p class="note">支持任意 OpenAI 兼容上游。保存后，本地网关会立即切换到新的转发配置。</p>
          </div>
          <div class="status pending" id="status-badge">等待配置</div>
        </div>
        <form id="config-form">
          <label>
            Upstream Base URL
            <input id="base-url" name="base_url" placeholder="https://your-openai-compatible-host.example/v1">
          </label>
          <label>
            Upstream API Key
            <input id="api-key" name="api_key" placeholder="填写你的上游 API Key">
          </label>
          <div>
            <label>节省策略</label>
            <div class="strategy-grid" id="strategy-grid"></div>
          </div>
          <div class="row">
            <button type="submit" id="save-btn">保存并启用</button>
            <button type="button" class="secondary" id="refresh-btn">刷新面板</button>
          </div>
          <div id="flash" class="flash"></div>
        </form>
      </section>

      <section class="card">
        <div class="toolbar">
          <div>
            <h2>2. 页面内测试</h2>
            <p class="note">这里会直接请求本地 <code>/v1/chat/completions</code>，方便你快速验证模型是否打通。</p>
          </div>
          <div class="status pending" id="test-status">尚未测试</div>
        </div>
        <form id="test-form">
          <label>
            测试模型名
            <input id="test-model" name="model" value="gpt-5.4" placeholder="例如 gpt-5.4">
          </label>
          <label>
            测试消息
            <textarea id="test-message" name="message" placeholder="例如：请把下面需求整理成开发任务，并标出风险点。">请用 Go 写一个简单的 HTTP 服务，并给出项目结构。</textarea>
          </label>
          <div class="row">
            <button type="submit" id="test-btn">发送测试请求</button>
          </div>
          <div id="test-flash" class="flash"></div>
        </form>
        <div class="output mono" id="test-output">测试结果会显示在这里。</div>
      </section>
    </section>

    <section class="layout wide">
      <section class="card">
        <div class="toolbar">
          <div>
            <h2>3. 本地接入方式</h2>
            <p class="note">你的应用今后应该调用下面这个本地地址，而不是继续直连上游站点。</p>
          </div>
        </div>
        <div class="codebox mono" id="proxy-usage">加载中...</div>
      </section>

      <section class="card">
        <div class="toolbar">
          <div>
            <h2>4. 运行概览</h2>
            <p class="note">概览每 5 秒自动刷新一次，能看到缓存命中、压缩命中和累计节省的 token。</p>
          </div>
        </div>
        <div class="metrics">
          <div class="metric">
            <b id="requests-count">0</b>
            <span>累计请求数</span>
          </div>
          <div class="metric">
            <b id="saved-tokens">0</b>
            <span>累计节省 Token</span>
          </div>
          <div class="metric">
            <b id="cache-hits">0</b>
            <span>缓存命中次数</span>
          </div>
          <div class="metric">
            <b id="compression-hits">0</b>
            <span>压缩命中次数</span>
          </div>
          <div class="metric">
            <b id="last-request">-</b>
            <span>最近请求时间</span>
          </div>
          <div class="metric">
            <b>本地代理</b>
            <span>当前工作模式</span>
          </div>
        </div>
      </section>
    </section>

    <section class="card wide">
      <div class="toolbar">
        <div>
          <h2>5. 最近请求</h2>
          <p class="note">这里显示最近经过本地网关的请求。如果这里没有记录，说明流量还没有真正走到本地代理。</p>
        </div>
      </div>
      <div id="request-list" class="request-list"></div>
    </section>
  </div>

  <script>
    const form = document.getElementById('config-form');
    const flash = document.getElementById('flash');
    const saveBtn = document.getElementById('save-btn');
    const refreshBtn = document.getElementById('refresh-btn');
    const statusBadge = document.getElementById('status-badge');
    const strategyGrid = document.getElementById('strategy-grid');
    const baseURLInput = document.getElementById('base-url');
    const apiKeyInput = document.getElementById('api-key');

    const testForm = document.getElementById('test-form');
    const testBtn = document.getElementById('test-btn');
    const testFlash = document.getElementById('test-flash');
    const testOutput = document.getElementById('test-output');
    const testStatus = document.getElementById('test-status');

    let currentStrategy = 'balanced';
    let exampleApiKey = 'local-not-used';
    let latestProxyBaseURL = 'http://127.0.0.1:8080';
    let pollTimer = null;
    let formDirty = false;
    let renderedStrategyKeys = '';
    let snapshotRequestID = 0;
    let latestAppliedSnapshotID = 0;

    function markConfigDirty() {
      formDirty = true;
    }

    function clearConfigDirty() {
      formDirty = false;
    }

    function setFlash(message, type) {
      flash.textContent = message || '';
      flash.className = 'flash' + (type ? ' ' + type : '');
    }

    function setTestFlash(message, type) {
      testFlash.textContent = message || '';
      testFlash.className = 'flash' + (type ? ' ' + type : '');
    }

    function setStatus(configured) {
      statusBadge.textContent = configured ? '已配置完成' : '等待配置';
      statusBadge.className = configured ? 'status' : 'status pending';
    }

    function setTestStatus(text, kind) {
      testStatus.textContent = text;
      testStatus.className = 'status' + (kind ? ' ' + kind : '');
    }

    function formatNumber(value) {
      return Number(value || 0).toLocaleString('zh-CN');
    }

    function escapeHTML(value) {
      return String(value || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    }

    function renderStrategies(items, selectedStrategy) {
      strategyGrid.innerHTML = '';
      for (const item of items) {
        const label = document.createElement('label');
        label.className = 'strategy';
        label.innerHTML =
          "<input type='radio' name='strategy' value='" + item.key + "'>" +
          '<strong>' + escapeHTML(item.label) + '</strong>' +
          '<span>' + escapeHTML(item.description) + '</span>';
        const radio = label.querySelector("input[name='strategy']");
        if (radio && item.key === selectedStrategy) {
          radio.checked = true;
        }
        strategyGrid.appendChild(label);
      }
    }

    function ensureStrategies(items, selectedStrategy, force) {
      const nextKeys = (items || []).map(function(item) { return item.key; }).join('|');
      if (!force && renderedStrategyKeys === nextKeys && strategyGrid.childElementCount > 0) {
        const existing = document.querySelector("input[name='strategy'][value='" + selectedStrategy + "']");
        if (existing) {
          existing.checked = true;
        }
        return;
      }
      renderedStrategyKeys = nextKeys;
      renderStrategies(items || [], selectedStrategy);
    }

    function updateQuickStart(proxyBaseURL) {
      latestProxyBaseURL = proxyBaseURL;
      const openaiBaseURL = proxyBaseURL.replace(/\/$/, '') + '/v1';
      document.getElementById('quick-start').textContent =
        '推荐给 OpenAI 兼容客户端的 Base URL\n' + openaiBaseURL + '\n\n' +
        '本地调用 API Key\n' + exampleApiKey + '\n\n' +
        '你在页面里保存的上游配置\n' +
        'base_url = 你的上游 Base URL\n' +
        'api_key  = 你的上游 API Key';

      document.getElementById('proxy-usage').textContent =
        'curl ' + openaiBaseURL + '/chat/completions \\\n' +
        '  -H "Authorization: Bearer ' + exampleApiKey + '" \\\n' +
        '  -H "Content-Type: application/json" \\\n' +
        '  -d "{\n' +
        '    \\"model\\": \\"gpt-5.4\\",\n' +
        '    \\"messages\\": [{\\"role\\": \\"user\\", \\"content\\": \\"请用 Go 写一个简单的 HTTP 服务\\"}]\n' +
        '  }"\n\n' +
        '说明：\n' +
        '1. 对 OpenClaw 这类客户端，Base URL 推荐填写 ' + openaiBaseURL + '\n' +
        '2. 也兼容直接填写 ' + proxyBaseURL + '，因为网关已兼容 /chat/completions 根路径\n' +
        '3. API Key 只要非空即可\n' +
        '4. model 仍然填写你上游实际支持的模型名';
    }

    function renderRecentRequests(items) {
      const container = document.getElementById('request-list');
      if (!items || !items.length) {
        container.innerHTML = '<div class="empty">还没有请求记录。你可以先在本页发送一次测试请求，或者把你的应用 Base URL 切到本地网关后再回来查看。</div>';
        return;
      }

      container.innerHTML = items.map(function(item) {
        const chips = [];
        chips.push('<span class="chip ' + (item.success ? 'ok' : 'bad') + '">' + (item.success ? '成功' : '失败') + '</span>');
        chips.push('<span class="chip">' + escapeHTML(item.upstream_kind || 'unknown') + '</span>');
        if (item.cache_hit) {
          chips.push('<span class="chip ok">缓存命中</span>');
        }
        if (item.compression_applied) {
          chips.push('<span class="chip warn">已压缩</span>');
        }
        if (item.search_applied) {
          chips.push('<span class="chip warn">已搜索增强</span>');
        }

        const modelLine = escapeHTML(item.original_model || item.final_model || '-') +
          (item.original_model && item.final_model && item.original_model !== item.final_model ? ' → ' + escapeHTML(item.final_model) : '');

        const detail = item.error_message || item.decision_reason || '本次请求没有额外决策说明。';

        return '' +
          '<article class="request-item">' +
            '<div class="request-head">' +
              '<div>' +
                '<strong>' + modelLine + '</strong>' +
                '<div class="meta">' + escapeHTML(item.created_at) + ' · ' + escapeHTML(item.endpoint || '-') + ' · HTTP ' + escapeHTML(item.status_code) + '</div>' +
              '</div>' +
              '<div class="chips">' + chips.join('') + '</div>' +
            '</div>' +
            '<div class="request-grid">' +
              '<div class="metric"><b>' + formatNumber(item.saved_tokens) + '</b><span>节省 Token</span></div>' +
              '<div class="metric"><b>' + formatNumber(item.total_tokens) + '</b><span>实际 Token</span></div>' +
              '<div class="metric"><b>' + formatNumber(item.duration_ms) + ' ms</b><span>耗时</span></div>' +
              '<div class="metric"><b>' + escapeHTML(item.upstream_name || '-') + '</b><span>命中的上游</span></div>' +
            '</div>' +
            '<div class="helper">' + escapeHTML(detail) + '</div>' +
          '</article>';
      }).join('');
    }

    function fillSnapshot(snapshot, options) {
      const forceConfig = Boolean(options && options.forceConfig);
      if (forceConfig || !formDirty) {
        baseURLInput.value = snapshot.config.base_url || '';
        apiKeyInput.value = snapshot.config.api_key || '';
        currentStrategy = snapshot.config.strategy || 'balanced';
      }

      ensureStrategies(
        snapshot.strategies || [],
        formDirty ? currentStrategy : (snapshot.config.strategy || currentStrategy),
        forceConfig
      );

      exampleApiKey = snapshot.example_api_key || 'local-not-used';
      setStatus(Boolean(snapshot.config.configured));
      updateQuickStart(snapshot.proxy_base_url);
      renderRecentRequests(snapshot.recent_requests || []);

      document.getElementById('requests-count').textContent = formatNumber(snapshot.overview.requests);
      document.getElementById('saved-tokens').textContent = formatNumber(snapshot.overview.saved_tokens);
      document.getElementById('cache-hits').textContent = formatNumber(snapshot.overview.cache_hits);
      document.getElementById('compression-hits').textContent = formatNumber(snapshot.overview.compression_hits);
      document.getElementById('last-request').textContent = snapshot.overview.last_request_at || '-';
    }

    async function loadSnapshot(options) {
      const silent = options && options.silent;
      const requestID = ++snapshotRequestID;
      const response = await fetch('/local/api/config', { headers: { 'Accept': 'application/json' } });
      if (!response.ok) {
        const payload = await response.json().catch(function() { return {}; });
        if (!silent) {
          throw new Error(payload && payload.error && payload.error.message ? payload.error.message : '加载失败');
        }
        return;
      }

      const snapshot = await response.json();
      if (requestID < latestAppliedSnapshotID) {
        return;
      }
      latestAppliedSnapshotID = requestID;
      fillSnapshot(snapshot, options);
    }

    function extractMessageContent(payload) {
      const choice = payload && payload.choices && payload.choices[0];
      if (!choice) {
        return JSON.stringify(payload, null, 2);
      }
      if (choice.message && typeof choice.message.content === 'string') {
        return choice.message.content;
      }
      if (choice.message && Array.isArray(choice.message.content)) {
        return choice.message.content.map(function(item) {
          if (typeof item === 'string') {
            return item;
          }
          if (item && typeof item.text === 'string') {
            return item.text;
          }
          return JSON.stringify(item);
        }).join('\n');
      }
      if (typeof choice.text === 'string') {
        return choice.text;
      }
      return JSON.stringify(payload, null, 2);
    }

    form.addEventListener('submit', async function(event) {
      event.preventDefault();
      const chosen = document.querySelector("input[name='strategy']:checked");
      const payload = {
        base_url: baseURLInput.value.trim(),
        api_key: apiKeyInput.value.trim(),
        strategy: chosen ? chosen.value : currentStrategy
      };

      saveBtn.disabled = true;
      setFlash('正在保存上游配置...', '');
      try {
        const response = await fetch('/local/api/config', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json'
          },
          body: JSON.stringify(payload)
        });
        const data = await response.json().catch(function() { return {}; });
        if (!response.ok) {
          throw new Error(data && data.error && data.error.message ? data.error.message : '保存失败');
        }

        currentStrategy = data && data.config && data.config.strategy ? data.config.strategy : payload.strategy;
        clearConfigDirty();
        latestAppliedSnapshotID = ++snapshotRequestID;
        fillSnapshot(data, { forceConfig: true });
        setFlash('上游配置已保存，本地网关已切换到新的转发设置。', 'ok');
      } catch (error) {
        setFlash(error && error.message ? error.message : '保存失败', 'error');
      } finally {
        saveBtn.disabled = false;
      }
    });

    refreshBtn.addEventListener('click', function() {
      loadSnapshot({ forceConfig: true }).catch(function(error) {
        setFlash(error && error.message ? error.message : '刷新失败', 'error');
      });
    });

    testForm.addEventListener('submit', async function(event) {
      event.preventDefault();
      const model = document.getElementById('test-model').value.trim();
      const message = document.getElementById('test-message').value.trim();
      if (!model) {
        setTestFlash('请先填写模型名。', 'error');
        return;
      }
      if (!message) {
        setTestFlash('请先填写测试消息。', 'error');
        return;
      }

      testBtn.disabled = true;
      setTestStatus('测试中', 'pending');
      setTestFlash('正在请求本地 /v1/chat/completions ...', '');
      testOutput.textContent = '请求发送中，请稍候...';

      try {
        const response = await fetch('/v1/chat/completions', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
            'Authorization': 'Bearer ' + exampleApiKey
          },
          body: JSON.stringify({
            model: model,
            messages: [{ role: 'user', content: message }]
          })
        });
        const data = await response.json().catch(function() { return {}; });
        if (!response.ok) {
          throw new Error(data && data.error && data.error.message ? data.error.message : '测试请求失败');
        }

        testOutput.textContent = extractMessageContent(data);
        setTestFlash('测试请求成功，下面的运行概览和最近请求会自动刷新。', 'ok');
        setTestStatus('测试成功', '');
        await loadSnapshot({ silent: false });
      } catch (error) {
        testOutput.textContent = error && error.message ? error.message : '测试请求失败';
        setTestFlash(error && error.message ? error.message : '测试请求失败', 'error');
        setTestStatus('测试失败', 'bad');
        await loadSnapshot({ silent: true });
      } finally {
        testBtn.disabled = false;
      }
    });

    baseURLInput.addEventListener('input', markConfigDirty);
    apiKeyInput.addEventListener('input', markConfigDirty);
    strategyGrid.addEventListener('change', function(event) {
      const target = event.target;
      if (!target || target.name !== 'strategy') {
        return;
      }
      currentStrategy = target.value || currentStrategy;
      markConfigDirty();
    });

    function startPolling() {
      if (pollTimer) {
        clearInterval(pollTimer);
      }
      pollTimer = window.setInterval(function() {
        loadSnapshot({ silent: true }).catch(function() {});
      }, 5000);
    }

    loadSnapshot({ forceConfig: true }).then(function() {
      startPolling();
    }).catch(function(error) {
      setFlash(error && error.message ? error.message : '初始化失败', 'error');
      startPolling();
    });
  </script>
</body>
</html>`
