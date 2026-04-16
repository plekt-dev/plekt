const ICONS = ['folder','code','star','home','globe','database','terminal','zap','settings','layers','box','cpu','git-branch','rocket','shield','book','music','camera','bar-chart','grid','heart','flag','lock','briefcase'];

window.MC = {
  _i18n: (function() {
    try {
      var el = document.getElementById('mc-i18n-data');
      return el ? JSON.parse(el.getAttribute('data-translations') || '{}') : {};
    } catch(e) { return {}; }
  })(),
  _lang: document.documentElement.lang || 'en',
  t(key, fallback) {
    return MC._i18n[key] || fallback || key;
  },
  renderers: {},
  registerRenderer(type, fn) {
    MC.renderers[type] = fn;
  },
  callAction(ctx, toolName, params) {
    return fetch(ctx.actionUrl + toolName, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': ctx.csrf },
      body: JSON.stringify(params),
    }).then(r => {
      if (!r.ok) return r.text().then(t => { throw new Error(t || r.statusText); });
      return r.json();
    });
  },
  showToast(msg, variant) {
    const el = document.createElement('div');
    el.className = 'mc-toast' + (variant === 'error' ? ' mc-toast-error' : '');
    el.textContent = msg;
    document.body.appendChild(el);
    setTimeout(() => el.remove(), 4000);
  },
  esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML.replace(/"/g, '&quot;').replace(/'/g, '&#39;'); },
  reloadPage() { location.reload(); },

  /** Debounce: returns a wrapper that delays invocation until `wait`ms after the last call. */
  debounce(fn, wait) {
    let t = null;
    return function() {
      const args = arguments;
      const ctx = this;
      if (t) clearTimeout(t);
      t = setTimeout(function() { t = null; fn.apply(ctx, args); }, wait);
    };
  },

  /**
   * Re-fetch the current plugin page in the background and re-render
   * `.plugin-page-content` elements in place — without a full page reload.
   * Used by the realtime SSE bridge so that agent-side changes show up live.
   */
  refetchPluginPage() {
    const targets = Array.from(document.querySelectorAll('.plugin-page-content[data-plugin]'));
    if (!targets.length) return;
    fetch(location.pathname + location.search, {
      credentials: 'same-origin',
      headers: { 'X-Requested-With': 'mc-refetch' },
    }).then(function(r) {
      if (!r.ok) return null;
      return r.text();
    }).then(function(html) {
      if (!html) return;
      const doc = new DOMParser().parseFromString(html, 'text/html');
      targets.forEach(function(el) {
        const plugin = el.dataset.plugin;
        const pageType = el.dataset.pageType || '';
        const fresh = doc.querySelector(
          '.plugin-page-content[data-plugin="' + plugin + '"][data-page-type="' + pageType + '"]'
        );
        if (!fresh) return;
        // Copy data-json (the source of truth for renderers) and any other dynamic attrs.
        if (fresh.dataset.json !== undefined) el.dataset.json = fresh.dataset.json;
      });
      if (typeof renderPluginPages === 'function') renderPluginPages();
    }).catch(function() {});
  },

  /**
   * Validate required fields inside a container.
   * Returns true if all required fields are filled, false otherwise.
   * Adds .input-error class and .field-error message to empty required fields.
   * Plugins call MC.validateRequired(container) before submitting.
   */
  validateRequired(container) {
    // Clear previous errors
    container.querySelectorAll('.field-error').forEach(e => e.remove());
    container.querySelectorAll('.input-error').forEach(e => e.classList.remove('input-error'));

    let valid = true;
    container.querySelectorAll('[required]').forEach(field => {
      // Skip hidden inputs (project-select auto-filled etc.)
      if (field.type === 'hidden') return;

      // For editor (markdown) fields, check the hidden textarea
      if (field.hasAttribute('data-editor')) {
        const name = field.dataset.fieldName;
        const textarea = field.querySelector('textarea');
        if (textarea && !textarea.value.trim()) {
          field.classList.add('input-error');
          const err = document.createElement('div');
          err.className = 'field-error';
          err.textContent = 'Required';
          field.parentElement.appendChild(err);
          valid = false;
        }
        return;
      }

      const val = (field.value || '').trim();
      if (!val) {
        field.classList.add('input-error');
        const err = document.createElement('div');
        err.className = 'field-error';
        err.textContent = 'Required';
        field.parentElement.appendChild(err);
        valid = false;
      }
    });

    // Clear error on focus
    if (!valid) {
      container.querySelectorAll('.input-error').forEach(field => {
        const handler = () => {
          field.classList.remove('input-error');
          const errEl = field.parentElement.querySelector('.field-error');
          if (errEl) errEl.remove();
          field.removeEventListener('focus', handler);
          field.removeEventListener('input', handler);
        };
        field.addEventListener('focus', handler);
        field.addEventListener('input', handler);
      });
    }

    return valid;
  },

  /**
   * Styled confirmation dialog. Returns a Promise<boolean>.
   * Options:
   *   title       — dialog heading
   *   message     — body text
   *   okText      — confirm button label (default "Delete")
   *   cancelText  — cancel button label (default "Cancel")
   *   inputMatch  — if set, user must type this string to enable confirm
   *   inputLabel  — label shown above the input field
   *   danger      — if true, confirm button is red (default true)
   */
  /** Open dashboard settings modal with widget visibility checkboxes. */
  dashboardSettings() {
    const grid = document.querySelector('.widget-grid');
    if (!grid) return;
    const csrf = grid.dataset.csrf || '';
    let meta;
    try { meta = JSON.parse(grid.dataset.widgetsMeta || '[]'); } catch { meta = []; }
    if (!meta.length) { MC.showToast(MC.t('dashboard.no_widgets', 'No widgets available'), 'error'); return; }

    let dialog = document.getElementById('mc-dashboard-settings');
    if (dialog) dialog.remove();

    dialog = document.createElement('dialog');
    dialog.id = 'mc-dashboard-settings';
    dialog.className = 'mc-dialog mc-confirm-dialog';

    const items = meta.map(w =>
      `<label class="dash-settings-item">
        <input type="checkbox" name="visible[]" value="${MC.esc(w.key)}" ${w.visible ? 'checked' : ''}/>
        <span>${MC.esc(w.title)}</span>
      </label>`
    ).join('');

    dialog.innerHTML = `
      <div class="mc-confirm-content">
        <h3 class="mc-confirm-title">${MC.t('dashboard.settings_title', 'Dashboard Settings')}</h3>
        <p class="mc-confirm-message">${MC.t('dashboard.settings_desc', 'Choose which widgets to display.')}</p>
        <div class="dash-settings-list">${items}</div>
        <div class="mc-confirm-actions">
          <button class="button button-ghost mc-confirm-cancel">${MC.t('common.cancel', 'Cancel')}</button>
          <button class="button button-primary mc-confirm-ok">${MC.t('common.save', 'Save')}</button>
        </div>
      </div>
    `;

    document.body.appendChild(dialog);

    const cancelBtn = dialog.querySelector('.mc-confirm-cancel');
    const saveBtn = dialog.querySelector('.mc-confirm-ok');

    function close() { dialog.close(); dialog.remove(); }

    cancelBtn.addEventListener('click', close);
    dialog.addEventListener('cancel', e => { e.preventDefault(); close(); });
    dialog.addEventListener('mousedown', e => {
      dialog._bg = true;
      const r = dialog.getBoundingClientRect();
      if (e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom) dialog._bg = false;
    });
    dialog.addEventListener('click', e => {
      if (!dialog._bg) return;
      const r = dialog.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) close();
    });

    saveBtn.addEventListener('click', () => {
      const form = new FormData();
      form.append('csrf_token', csrf);
      // Collect current grid order for visible widgets, then append hidden ones
      const gridKeys = Array.from(grid.querySelectorAll('.widget-card[data-widget-key]')).map(c => c.dataset.widgetKey);
      const checked = new Set(Array.from(dialog.querySelectorAll('input[name="visible[]"]:checked')).map(i => i.value));
      // Preserve grid order for currently visible, append newly visible, then hidden
      const ordered = [];
      gridKeys.forEach(k => { if (!ordered.includes(k)) ordered.push(k); });
      meta.forEach(w => { if (!ordered.includes(w.key)) ordered.push(w.key); });
      ordered.forEach(k => {
        form.append('widget_keys[]', k);
        if (checked.has(k)) form.append('visible[]', k);
      });
      fetch('/dashboard/layout', { method: 'POST', body: form })
        .then(r => { if (r.ok) location.reload(); else MC.showToast(MC.t('common.save_failed', 'Save failed'), 'error'); })
        .catch(() => MC.showToast(MC.t('common.save_failed', 'Save failed'), 'error'));
      close();
    });

    dialog.showModal();
  },

  /** Initialize drag & drop reordering on the dashboard widget grid. */
  initDashboardDragDrop() {
    const grid = document.querySelector('.widget-grid');
    if (!grid) return;
    const csrf = grid.dataset.csrf || '';
    let dragEl = null;

    grid.querySelectorAll('.widget-card[draggable]').forEach(card => {
      card.addEventListener('dragstart', e => {
        dragEl = card;
        card.classList.add('widget-dragging');
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', card.dataset.widgetKey);
      });
      card.addEventListener('dragend', () => {
        card.classList.remove('widget-dragging');
        dragEl = null;
        grid.querySelectorAll('.widget-card').forEach(c => c.classList.remove('widget-drop-before', 'widget-drop-after'));
      });
    });

    grid.addEventListener('dragover', e => {
      if (!dragEl) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      const target = e.target.closest('.widget-card');
      if (!target || target === dragEl) return;
      grid.querySelectorAll('.widget-card').forEach(c => c.classList.remove('widget-drop-before', 'widget-drop-after'));
      const rect = target.getBoundingClientRect();
      const mid = rect.left + rect.width / 2;
      if (e.clientX < mid) {
        target.classList.add('widget-drop-before');
      } else {
        target.classList.add('widget-drop-after');
      }
    });

    grid.addEventListener('drop', e => {
      e.preventDefault();
      if (!dragEl) return;
      const target = e.target.closest('.widget-card');
      if (!target || target === dragEl) return;
      const rect = target.getBoundingClientRect();
      const mid = rect.left + rect.width / 2;
      if (e.clientX < mid) {
        grid.insertBefore(dragEl, target);
      } else {
        grid.insertBefore(dragEl, target.nextSibling);
      }
      grid.querySelectorAll('.widget-card').forEach(c => c.classList.remove('widget-drop-before', 'widget-drop-after'));

      // Auto-save new order
      const form = new FormData();
      form.append('csrf_token', csrf);
      let meta;
      try { meta = JSON.parse(grid.dataset.widgetsMeta || '[]'); } catch { meta = []; }
      const visibleKeys = new Set(meta.filter(w => w.visible).map(w => w.key));
      const gridKeys = Array.from(grid.querySelectorAll('.widget-card[data-widget-key]')).map(c => c.dataset.widgetKey);
      const ordered = [...gridKeys];
      meta.forEach(w => { if (!ordered.includes(w.key)) ordered.push(w.key); });
      ordered.forEach(k => {
        form.append('widget_keys[]', k);
        if (visibleKeys.has(k)) form.append('visible[]', k);
      });
      fetch('/dashboard/layout', { method: 'POST', body: form }).catch(() => {});
    });
  },

  confirm(opts = {}) {
    return new Promise(resolve => {
      const title = opts.title || MC.t('confirm.title', 'Confirm');
      const message = opts.message || MC.t('confirm.message', 'Are you sure?');
      const okText = opts.okText || MC.t('confirm.delete', 'Delete');
      const cancelText = opts.cancelText || MC.t('common.cancel', 'Cancel');
      const inputMatch = opts.inputMatch || '';
      const inputLabel = opts.inputLabel || '';
      const danger = opts.danger !== false;

      let dialog = document.getElementById('mc-confirm-dialog');
      if (dialog) dialog.remove();

      dialog = document.createElement('dialog');
      dialog.id = 'mc-confirm-dialog';
      dialog.className = 'mc-dialog mc-confirm-dialog';

      let inputHtml = '';
      if (inputMatch) {
        inputHtml = `
          <label class="mc-confirm-input-label">${MC.esc(inputLabel || 'Type "' + inputMatch + '" to confirm')}</label>
          <input type="text" class="form-input mc-confirm-input" autocomplete="off" spellcheck="false" />
        `;
      }

      dialog.innerHTML = `
        <div class="mc-confirm-content">
          <h3 class="mc-confirm-title">${MC.esc(title)}</h3>
          <p class="mc-confirm-message">${MC.esc(message)}</p>
          ${inputHtml}
          <div class="mc-confirm-actions">
            <button class="button button-ghost mc-confirm-cancel">${MC.esc(cancelText)}</button>
            <button class="button ${danger ? 'button-danger' : 'button-primary'} mc-confirm-ok" ${inputMatch ? 'disabled' : ''}>${MC.esc(okText)}</button>
          </div>
        </div>
      `;

      document.body.appendChild(dialog);

      const okBtn = dialog.querySelector('.mc-confirm-ok');
      const cancelBtn = dialog.querySelector('.mc-confirm-cancel');
      const input = dialog.querySelector('.mc-confirm-input');

      function cleanup(result) {
        dialog.close();
        dialog.remove();
        resolve(result);
      }

      cancelBtn.addEventListener('click', () => cleanup(false));
      okBtn.addEventListener('click', () => cleanup(true));

      if (input && inputMatch) {
        input.addEventListener('input', () => {
          okBtn.disabled = input.value !== inputMatch;
        });
        input.addEventListener('keydown', e => {
          if (e.key === 'Enter' && input.value === inputMatch) cleanup(true);
        });
      }

      dialog.addEventListener('cancel', e => { e.preventDefault(); cleanup(false); });
      dialog.addEventListener('mousedown', e => {
        dialog._mousedownOnBackdrop = true;
        const r = dialog.getBoundingClientRect();
        if (e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom) dialog._mousedownOnBackdrop = false;
      });
      dialog.addEventListener('click', e => {
        if (!dialog._mousedownOnBackdrop) return;
        const r = dialog.getBoundingClientRect();
        if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) cleanup(false);
      });

      dialog.showModal();
      if (input) input.focus();
    });
  },
};

// -------- MC.pluginSettingsSubmit: shared submit handler for plugin --------
// settings sections rendered by PluginSettingsSection on /admin/settings.
//
// The native form action would POST to /p/{plugin}/action/{tool} and the
// browser would navigate away to the JSON response. We intercept submit,
// build a JSON body matching the WASM action endpoint contract, fetch it,
// and render the result inline next to the form.
//
// Returns false to cancel native submission.
MC.pluginSettingsSubmit = function (event) {
  if (!event) return false;
  event.preventDefault();
  event.stopPropagation();
  var form = event.target;
  if (!form || form.tagName !== 'FORM') return false;

  var plugin = form.getAttribute('data-plugin') || '';
  var tool = form.getAttribute('data-action') || '';
  var csrf = form.getAttribute('data-csrf') || '';
  if (!plugin || !tool) return false;

  var status = form.parentNode && form.parentNode.querySelector('.plugin-section-status');
  function setStatus(text, ok) {
    if (!status) return;
    status.textContent = text || '';
    status.style.color = ok ? 'var(--primary)' : 'var(--destructive)';
  }
  setStatus('Saving…', true);

  // Serialize all form fields into a JSON object. Skip the csrf_token field
  // (it goes in the header). Numeric inputs are left as strings — the WASM
  // handler unmarshals into typed structs and coerces as needed.
  var body = {};
  var data = new FormData(form);
  data.forEach(function (value, key) {
    if (key === 'csrf_token') return;
    body[key] = value;
  });

  fetch('/p/' + plugin + '/action/' + tool, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      'X-CSRF-Token': csrf,
    },
    body: JSON.stringify(body),
  }).then(function (r) {
    if (!r.ok) {
      return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
    }
    return r.json().catch(function () { return {}; });
  }).then(function (result) {
    setStatus('Saved.', true);
    // Plugins may signal a follow-up requirement via needs_host_grant.
    if (result && result.needs_host_grant) {
      MC.confirm({
        title: 'Host grant required',
        message: 'Plugin "' + plugin + '" needs network access to "' + result.needs_host_grant + '". Open Permissions to grant it.',
        okText: 'Open Permissions',
        cancelText: 'Dismiss',
        danger: false,
      }).then(function (ok) {
        if (ok) window.location.href = '/admin/plugins/' + plugin + '/permissions';
      });
    }
  }).catch(function (err) {
    setStatus((err && err.message) || 'Save failed', false);
  });

  return false;
};

// -------- MC.events: realtime SSE client (task #26) --------
// Single EventSource per page that consumes /api/events and dispatches
// to handlers registered with MC.events.on(name, handler). Handlers
// receive the parsed `.payload` field, not the raw Event wrapper.
MC.events = {
  _source: null,
  _handlers: {},        // name -> [handler, ...]
  _connectCbs: [],
  _disconnectCbs: [],

  _init() {
    if (this._source) return;
    try {
      this._source = new EventSource('/api/events');
    } catch (e) {
      return;
    }
    const self = this;
    this._source.addEventListener('open', function() {
      self._connectCbs.forEach(function(cb) { try { cb(); } catch (e) {} });
    });
    this._source.addEventListener('error', function() {
      self._disconnectCbs.forEach(function(cb) { try { cb(); } catch (e) {} });
    });
    this._source.addEventListener('message', function(e) {
      self._dispatch(e);
    });

    // CRITICAL: explicitly close the EventSource when the page goes away.
    // Without this, the browser keeps the SSE connection alive in bfcache
    // and a fresh one is opened on every navigation. After ~5 navigations
    // the HTTP/1.1 6-connections-per-origin limit is exhausted (SSE + 5
    // leaked SSEs) and ALL subsequent requests on the page stack pending.
    // Symptoms: page loads taking 30+ seconds, Pending requests in DevTools.
    function shutdown() {
      if (self._source) {
        try { self._source.close(); } catch (e) {}
        self._source = null;
      }
    }
    // pagehide fires for both unload and bfcache freeze. Use it as the
    // primary teardown trigger; visibilitychange "hidden" handles the
    // mobile/tab-backgrounded case where pagehide might not fire.
    window.addEventListener('pagehide', shutdown, { once: false });
    window.addEventListener('beforeunload', shutdown, { once: false });
    // Also listen for typed events. The server emits `event: <name>`
    // so EventSource.addEventListener(name, ...) would work, but we
    // prefer a single dispatcher driven by the JSON body's `name`
    // field to keep registration simple for plugins.
    // Fallback: wire addEventListener for each known name as plugins
    // call on() — see _ensureNamedListener.
  },

  _dispatch(e) {
    let wire;
    try { wire = JSON.parse(e.data); } catch (err) { return; }
    if (!wire || !wire.name) return;
    const list = this._handlers[wire.name];
    if (!list || !list.length) return;
    list.forEach(function(h) {
      try { h(wire.payload, wire); } catch (err) {
        if (window.console) console.error('MC.events handler error', err);
      }
    });
  },

  _ensureNamedListener(name) {
    if (!this._source) return;
    if (this._source['_mc_listen_' + name]) return;
    const self = this;
    this._source.addEventListener(name, function(e) {
      // Named SSE events land here too; dispatch via the shared map.
      let wire;
      try { wire = JSON.parse(e.data); } catch (err) { return; }
      if (!wire) return;
      const list = self._handlers[name];
      if (!list) return;
      list.forEach(function(h) {
        try { h(wire.payload, wire); } catch (err) {}
      });
    });
    this._source['_mc_listen_' + name] = true;
  },

  on(name, handler) {
    if (!this._handlers[name]) this._handlers[name] = [];
    this._handlers[name].push(handler);
    this._ensureNamedListener(name);
    const self = this;
    return function unsubscribe() {
      const list = self._handlers[name];
      if (!list) return;
      const i = list.indexOf(handler);
      if (i >= 0) list.splice(i, 1);
    };
  },

  /**
   * bindRefresh(name, { url, selector, merge }) — whenever the given
   * realtime event fires, re-fetch `url` via htmx.ajax into `selector`.
   * `merge` defaults to 'innerHTML'.
   */
  bindRefresh(name, opts) {
    opts = opts || {};
    return this.on(name, function() {
      if (!window.htmx || !opts.url || !opts.selector) return;
      try {
        window.htmx.ajax('GET', opts.url, {
          target: opts.selector,
          swap: opts.merge || 'innerHTML',
        });
      } catch (e) {}
    });
  },

  onConnect(cb) { this._connectCbs.push(cb); },
  onDisconnect(cb) { this._disconnectCbs.push(cb); },

  get state() {
    if (!this._source) return 'disconnected';
    switch (this._source.readyState) {
      case 0: return 'connecting';
      case 1: return 'connected';
      default: return 'disconnected';
    }
  },
};

// Backward-compat aliases used throughout this file
function callAction(ctx, tool, params) { return MC.callAction(ctx, tool, params); }
function showToast(msg, v) { return MC.showToast(msg, v); }
function esc(s) { return MC.esc(s); }
function reloadPage() { MC.reloadPage(); }

// Global backdrop-click handler for ALL <dialog class="mc-dialog"> elements,
// including those created by plugins (scheduler, notes, etc). A two-phase
// mousedown/click check prevents accidental close when the user starts a
// drag selection inside the dialog and releases outside it.
document.addEventListener('mousedown', e => {
  const dlg = e.target;
  if (!(dlg instanceof HTMLDialogElement)) return;
  if (!dlg.classList.contains('mc-dialog')) return;
  const r = dlg.getBoundingClientRect();
  const inside = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
  dlg._mcBackdropDown = !inside;
}, true);
document.addEventListener('click', e => {
  const dlg = e.target;
  if (!(dlg instanceof HTMLDialogElement)) return;
  if (!dlg.classList.contains('mc-dialog')) return;
  if (!dlg._mcBackdropDown) return;
  dlg._mcBackdropDown = false;
  const r = dlg.getBoundingClientRect();
  const outside = e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  if (outside) dlg.close();
}, true);

// Wire realtime SSE → live UI updates. The SSE pipeline (internal/realtime)
// fans out EventBus events to the browser; here we connect those events to a
// generic re-render of the active plugin page so the human sees agent-side
// changes (task moved, timer started, etc) without F5.
document.addEventListener('DOMContentLoaded', () => {
  if (MC.events && typeof MC.events._init === 'function') {
    MC.events._init();
    const refetch = MC.debounce(MC.refetchPluginPage.bind(MC), 200);
    [
      'task.created', 'task.updated', 'task.deleted', 'task.completed',
      'comment.created', 'comment.deleted',
      'project.created', 'project.updated', 'project.archived', 'project.deleted',
      'pomodoro.started', 'pomodoro.completed', 'pomodoro.interrupted',
    ].forEach(name => MC.events.on(name, refetch));

    // Dashboard pomodoro widget: trigger htmx refresh on pomodoro events so
    // the running timer / stats card reflects state changes immediately.
    const refreshPomodoroWidget = () => {
      const el = document.querySelector('.widget-card[data-widget-key="pomodoro"]');
      if (!el || !window.htmx) return;
      const url = el.getAttribute('hx-get');
      if (!url) return;
      try { window.htmx.ajax('GET', url, { target: el, swap: 'innerHTML' }); } catch (e) {}
    };
    ['pomodoro.started', 'pomodoro.completed', 'pomodoro.interrupted'].forEach(
      name => MC.events.on(name, refreshPomodoroWidget)
    );
  }
});

document.addEventListener('DOMContentLoaded', () => {
  const sidebar = document.querySelector('.sidebar');
  const sidebarTrigger = document.querySelector('.sidebar-trigger');

  if (sidebarTrigger && sidebar) {
    sidebarTrigger.addEventListener('click', () => {
      sidebar.classList.toggle('collapsed');
      const isCollapsed = sidebar.classList.contains('collapsed');
      localStorage.setItem('sidebar-collapsed', isCollapsed);
    });
  }

  // Animation delay for elements
  const revealElements = document.querySelectorAll('.animate-reveal');
  revealElements.forEach((el, index) => {
    el.style.animationDelay = `${index * 80}ms`;
  });

  // Language picker
  const langPicker = document.querySelector('.lang-picker');
  if (langPicker) {
    const toggle = langPicker.querySelector('.lang-picker-toggle');
    const menu = langPicker.querySelector('.lang-picker-menu');
    const csrf = menu.querySelector('[name="csrf_token"]');

    toggle.addEventListener('click', (e) => {
      e.stopPropagation();
      const isOpen = langPicker.classList.toggle('open');
      toggle.setAttribute('aria-expanded', isOpen);

      if (isOpen) {
        const rect = toggle.getBoundingClientRect();
        const sidebar = document.querySelector('.sidebar');
        const collapsed = sidebar && sidebar.classList.contains('collapsed');
        if (collapsed) {
          menu.style.position = 'fixed';
          menu.style.left = (rect.right + 6) + 'px';
          menu.style.bottom = (window.innerHeight - rect.bottom) + 'px';
          menu.style.top = 'auto';
        } else {
          menu.style.position = '';
          menu.style.left = '';
          menu.style.bottom = '';
          menu.style.top = '';
        }
      }
    });

    menu.querySelectorAll('.lang-picker-item').forEach(btn => {
      btn.addEventListener('click', () => {
        const lang = btn.dataset.lang;
        langPicker.classList.remove('open');
        toggle.setAttribute('aria-expanded', 'false');

        const body = new URLSearchParams();
        body.append('language', lang);
        if (csrf) body.append('csrf_token', csrf.value);

        fetch('/api/language', { method: 'POST', body })
          .then(() => location.reload());
      });
    });

    document.addEventListener('click', () => {
      langPicker.classList.remove('open');
      toggle.setAttribute('aria-expanded', 'false');
    });

    langPicker.addEventListener('click', (e) => e.stopPropagation());
  }

  renderWidgets();
  renderPluginPages();
  MC.initDashboardDragDrop();
});

document.body.addEventListener('htmx:afterSettle', () => { renderWidgets(); renderPluginPages(); });

function renderWidgets() {
  document.querySelectorAll('.widget-data[data-json]').forEach(el => {
    const raw = el.dataset.json;
    if (!raw) { el.innerHTML = `<p class="text-muted-foreground text-sm">${MC.t('table.no_data', 'No data')}</p>`; return; }
    try {
      const linkTpl = el.dataset.linkTemplate || '';
      el.innerHTML = renderJSON(JSON.parse(raw), linkTpl);
    } catch {
      el.innerHTML = `<p class="widget-error">${MC.t('table.invalid_data', 'Invalid data')}</p>`;
    }
  });
}

function renderJSON(val, linkTpl) {
  if (val === null || val === undefined) return '<span class="text-muted-foreground">—</span>';
  if (typeof val === 'string') return esc(val);
  if (typeof val === 'number' || typeof val === 'boolean') return String(val);
  if (Array.isArray(val)) {
    if (val.length === 0) return '<p class="text-muted-foreground text-sm">Empty</p>';
    if (typeof val[0] === 'object' && val[0] !== null) {
      return '<ul class="widget-list">' + val.map(item => {
        const label = item.title || item.name || item.id || '';
        let labelHtml;
        if (linkTpl && item.id) {
          let href = linkTpl;
          for (const [k, v] of Object.entries(item)) {
            href = href.replace('{' + k + '}', encodeURIComponent(v));
          }
          // Only link if all placeholders were resolved
          if (!href.includes('{')) {
            labelHtml = `<a href="${esc(href)}" class="link font-medium">${esc(String(label))}</a>`;
          } else {
            labelHtml = `<span class="font-medium">${esc(String(label))}</span>`;
          }
        } else {
          labelHtml = `<span class="font-medium">${esc(String(label))}</span>`;
        }
        const detail = Object.entries(item)
          .filter(([k]) => k !== 'title' && k !== 'name' && k !== 'id' && k !== 'description' && !k.endsWith('_at') && !k.endsWith('_id') && k !== 'kind')
          .map(([k, v]) => `<span class="badge badge-default">${esc(String(v))}</span>`)
          .slice(0, 2).join(' ');
        return `<li class="widget-list-item">${labelHtml}${detail}</li>`;
      }).join('') + '</ul>';
    }
    return val.map(v => renderJSON(v, linkTpl)).join(', ');
  }
  // Object: render key-value pairs
  const entries = Object.entries(val);
  // If object has a single array field, render the array
  const arrayField = entries.find(([, v]) => Array.isArray(v));
  if (arrayField) {
    const other = entries.filter(([k]) => k !== arrayField[0] && !Array.isArray(val[k]));
    let html = '';
    if (other.length > 0) {
      html += '<div class="widget-stats">' + other.map(([k, v]) =>
        `<div class="widget-stat"><span class="widget-stat-value">${esc(String(v))}</span><span class="widget-stat-label">${esc(k.replace(/_/g, ' '))}</span></div>`
      ).join('') + '</div>';
    }
    return html + renderJSON(arrayField[1], linkTpl);
  }
  // Single-key wrapper object: unwrap and render the inner value directly
  if (entries.length === 1 && entries[0][1] && typeof entries[0][1] === 'object' && !Array.isArray(entries[0][1])) {
    return renderJSON(entries[0][1], linkTpl);
  }
  // Plain key-value
  return '<div class="widget-stats">' + entries.map(([k, v]) => {
    if (v && typeof v === 'object') return `<div class="widget-stat">${renderJSON(v, linkTpl)}<span class="widget-stat-label">${esc(k.replace(/_/g, ' '))}</span></div>`;
    const display = String(v ?? '—');
    return `<div class="widget-stat"><span class="widget-stat-value">${esc(display)}</span><span class="widget-stat-label">${esc(k.replace(/_/g, ' '))}</span></div>`;
  }).join('') + '</div>';
}

// --- Plugin page renderers ---

function renderPluginPages() {
  document.querySelectorAll('.plugin-page-content[data-json]').forEach(el => {
    const raw = el.dataset.json;
    const pageType = el.dataset.pageType || 'table';
    if (!raw) { el.innerHTML = `<p class="text-muted-foreground">${MC.t('table.no_data', 'No data')}</p>`; return; }
    try {
      const data = JSON.parse(raw);
      const ctx = {
        actionUrl: el.dataset.actionUrl || '',
        csrf: el.dataset.csrf || '',
        tools: JSON.parse(el.dataset.tools || '[]'),
        extensions: JSON.parse(el.dataset.extensions || '[]'),
        plugin: el.dataset.plugin,
        username: el.dataset.username || '',
        userId: el.dataset.userId ? Number(el.dataset.userId) : null,
        projectId: el.dataset.projectId ? Number(el.dataset.projectId) : null,
        el: el,
      };
      const renderer = MC.renderers[pageType] || MC.renderers['table'];
      el.innerHTML = renderer(data, ctx);
      bindPageActions(el, ctx);
      // Plugin-specific deep link handling (e.g. ?task=ID for tasks-plugin)
      if (typeof MC.handleDeepLink === 'function') MC.handleDeepLink(ctx);
    } catch(e) {
      el.innerHTML = `<p class="widget-error">${MC.t('common.failed_to_render', 'Failed to render page')}</p>`;
    }
  });
}

// --- Action API ---

function bindPageActions(el, ctx) {
  // Create form modal — use <dialog> for proper top-layer rendering
  el.querySelectorAll('[data-action="show-create"]').forEach(btn => {
    btn.addEventListener('click', () => {
      let dialog = document.getElementById('mc-create-dialog');
      if (!dialog) {
        dialog = document.createElement('dialog');
        dialog.id = 'mc-create-dialog';
        dialog.className = 'mc-dialog';
        document.body.appendChild(dialog);
        dialog.addEventListener('mousedown', e => { const r = dialog.getBoundingClientRect(); dialog._mousedownOnBackdrop = e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom; });
        dialog.addEventListener('click', e => { if (!dialog._mousedownOnBackdrop) return; const r = dialog.getBoundingClientRect(); if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) dialog.close(); });
      }
      const form = el.querySelector('.create-form');
      if (!form) return;
      if (dialog.open) { dialog.close(); return; }
      // Move form content into dialog
      dialog.innerHTML = '';
      const clone = form.cloneNode(true);
      clone.classList.remove('hidden');
      dialog.appendChild(clone);
      dialog.showModal();
      // Initialise any markdown editors injected into the dialog
      if (typeof window.initEditor === 'function') {
        dialog.querySelectorAll('[data-editor]').forEach(editorEl => window.initEditor(editorEl));
      }
      // Bind submit inside dialog
      dialog.querySelectorAll('[data-action="submit-create"]').forEach(submitBtn => {
        submitBtn.addEventListener('click', () => {
          // Validate required fields before collecting params
          if (!MC.validateRequired(dialog)) return;

          const params = {};
          dialog.querySelectorAll('[data-field]').forEach(input => {
            const key = input.dataset.field;
            if (input.type === 'hidden') { if (input.value) { params[key] = (input.dataset.projectSelect || input.dataset.typeSelect) ? Number(input.value) : input.value; } return; }
            const val = input.value.trim();
            if (val) {
              if (input.type === 'number' || input.dataset.projectSelect || input.dataset.typeSelect) params[key] = Number(val);
              else params[key] = val;
            }
          });
          const toolName = submitBtn.dataset.tool;
          submitBtn.disabled = true;
          submitBtn.textContent = MC.t('common.saving', 'Saving...');
          callAction(ctx, toolName, params).then(() => { dialog.close(); reloadPage(); }).catch(err => { submitBtn.disabled = false; submitBtn.textContent = MC.t('common.save', 'Save'); showToast(err.message || MC.t('common.action_failed', 'Action failed'), 'error'); });
        });
      });
      // Cancel button closes dialog
      dialog.querySelectorAll('[data-action="show-create"]').forEach(cancelBtn => {
        cancelBtn.addEventListener('click', () => dialog.close());
      });
      // Populate project selects (generic — works on all pages)
      dialog.querySelectorAll('select[data-project-select]').forEach(sel => {
        fetch('/p/projects-plugin/action/list_projects', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': ctx.csrf },
          body: JSON.stringify({ status: 'active' }),
        }).then(r => r.json()).then(data => {
          const projects = data.projects || [];
          for (const p of projects) {
            const opt = document.createElement('option');
            opt.value = String(p.id);
            opt.textContent = p.name;
            sel.appendChild(opt);
          }
        }).catch(() => {});
      });
      // Plugin-specific select population (e.g. status, type selects)
      if (typeof MC.populateDialogSelects === 'function') MC.populateDialogSelects(dialog, ctx);
      // Bind icon picker inside dialog
      bindIconPicker(dialog);
      bindImageUpload(dialog);
    });
  });

  // Delete buttons — styled confirmation dialog
  el.querySelectorAll('[data-action="delete"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const id = Number(btn.dataset.id);
      const tool = btn.dataset.tool;
      const itemName = btn.dataset.name || '';
      const confirmInput = btn.dataset.confirmInput;

      const opts = {
        title: MC.t('confirm.delete_item', 'Delete item'),
        message: itemName
          ? MC.t('confirm.delete_message', 'Are you sure you want to delete this item? This action cannot be undone.').replace('this item', `"${itemName}"`)
          : MC.t('confirm.delete_message', 'Are you sure you want to delete this item? This action cannot be undone.'),
      };

      if (confirmInput) {
        opts.inputMatch = confirmInput;
        opts.inputLabel = `Type "${confirmInput}" to confirm deletion`;
        opts.title = `Delete "${confirmInput}"`;
        opts.message = 'This will permanently delete this item and all associated data. This action cannot be undone.';
      }

      MC.confirm(opts).then(ok => {
        if (!ok) return;
        callAction(ctx, tool, { id }).then(reloadPage);
      });
    });
  });

  // Generic tool call buttons (start/stop timer, etc.)
  el.querySelectorAll('[data-action="call-tool"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const tool = btn.dataset.tool;
      const params = JSON.parse(btn.dataset.params || '{}');
      btn.disabled = true;
      callAction(ctx, tool, params).then(reloadPage).catch(() => { btn.disabled = false; });
    });
  });

  // Extension action buttons (cross-plugin tool calls)
  el.querySelectorAll('[data-action="ext-action"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const extPlugin = btn.dataset.extPlugin;
      const extTool = btn.dataset.extTool;
      const itemId = Number(btn.dataset.itemId);
      if (!extPlugin || !extTool) return;
      btn.disabled = true;
      const actionUrl = `/p/${extPlugin}/action/`;
      // Map extension action names to MCP tools with appropriate params
      const toolMap = {
        'start_timer': { tool: 'start_session', params: Object.assign({ session_type: 'task', task_id: itemId }, ctx.projectId ? { project_id: ctx.projectId } : {}) },
        'stop_timer': { tool: 'stop_session', params: { task_id: itemId } },
        'pause_timer': { tool: 'pause_session', params: { task_id: itemId } },
        'resume_timer': { tool: 'resume_session', params: { task_id: itemId } },
      };
      const mapped = toolMap[extTool];
      const tool = mapped ? mapped.tool : extTool;
      const params = mapped ? mapped.params : { id: itemId };
      fetch(actionUrl + tool, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': ctx.csrf },
        body: JSON.stringify(params),
      }).then(r => { if (!r.ok) throw new Error(); return r.json(); })
        .then(reloadPage)
        .catch(() => { btn.disabled = false; });
    });
  });

  // Plugin-specific action bindings
  if (typeof MC.bindKanbanActions === 'function') MC.bindKanbanActions(el, ctx);
  if (typeof MC.bindTimerActions === 'function') MC.bindTimerActions(el, ctx);
  if (typeof MC.bindProjectListActions === 'function') MC.bindProjectListActions(el, ctx);
  if (typeof MC.bindNotesActions === 'function') MC.bindNotesActions(el, ctx);

  // Live timer badge updates on cards
  const liveBadges = el.querySelectorAll('[data-timer-started]');
  if (liveBadges.length > 0) {
    const fmtDur = s => { if (s >= 3600) return Math.floor(s/3600)+'h '+Math.floor((s%3600)/60)+'m'; if (s >= 60) return Math.floor(s/60)+'m'; return s+'s'; };
    setInterval(() => {
      liveBadges.forEach(badge => {
        const started = badge.dataset.timerStarted;
        const pausedSec = parseInt(badge.dataset.timerPausedSec || '0', 10);
        const completedSec = parseInt(badge.dataset.timerCompletedSec || '0', 10);
        const pausedAt = badge.dataset.timerPausedAt;
        let activeSec;
        if (pausedAt) {
          activeSec = Math.max(0, Math.floor((new Date(pausedAt) - new Date(started)) / 1000) - pausedSec);
        } else {
          activeSec = Math.max(0, Math.floor((Date.now() - new Date(started).getTime()) / 1000) - pausedSec);
        }
        const total = completedSec + activeSec;
        const prefix = pausedAt ? '⏸ ' : '⏱ ';
        badge.textContent = prefix + fmtDur(total);
      });
    }, 1000);
  }
}

// --- Detect create/delete tools from manifest tools list ---

function findTool(tools, ...prefixes) {
  for (const p of prefixes) {
    const t = tools.find(t => t.name.startsWith(p));
    if (t) return t.name;
  }
  return null;
}

// --- Table renderer with CRUD ---

function renderTable(data, ctx) {
  let items = [];
  if (Array.isArray(data)) { items = data; }
  else if (data && typeof data === 'object') {
    const entries = Object.entries(data);
    const arrayEntry = entries.find(([, v]) => Array.isArray(v));
    if (arrayEntry) items = arrayEntry[1];
  }

  const createTool = findTool(ctx.tools, 'create_', 'add_', 'start_');
  const deleteTool = findTool(ctx.tools, 'delete_', 'remove_');
  const updateTool = findTool(ctx.tools, 'update_', 'edit_');

  let html = '<div class="page-toolbar">';
  if (createTool) {
    const schema = ctx.tools.find(t => t.name === createTool);
    html += `<button class="button button-primary" data-action="show-create">+ New</button>`;
    html += renderCreateForm(createTool, schema, ctx);
  }
  html += '</div>';

  if (items.length === 0) {
    html += `<p class="text-muted-foreground" style="margin-top:1rem;">${MC.t('table.no_items', 'No items yet')}</p>`;
    return html;
  }

  const rowLink = ctx.el.dataset.rowLink || '';
  const keys = Object.keys(items[0]).filter(k => !k.endsWith('_at') && k !== 'description' && k !== 'body');
  const skipKeys = new Set(['id', 'color', 'image', 'status']);
  const linkKeyIndex = rowLink
    ? keys.findIndex(k => !skipKeys.has(k) && !k.endsWith('_at') && k !== 'description' && k !== 'body')
    : -1;
  html += `<div class="page-table-wrap"><table class="page-table">
    <thead><tr>${keys.map(k => `<th>${esc(k.replace(/_/g, ' '))}</th>`).join('')}${deleteTool ? '<th></th>' : ''}</tr></thead>
    <tbody>${items.map(row => {
      const cells = keys.map((k, ki) => {
        const v = row[k];
        if (v == null) return `<td><span class="text-muted-foreground">—</span></td>`;
        if (k === 'color' && String(v).startsWith('#')) {
          return `<td><span class="color-swatch" style="background:${esc(String(v))}"></span></td>`;
        }
        if (k === 'image') {
          if (String(v).startsWith('data:')) return `<td><img src="${esc(String(v))}" width="32" height="32" style="border-radius:4px;"/></td>`;
          return `<td><span class="text-muted-foreground">—</span></td>`;
        }
        const cls = k === 'status' ? ` class="badge badge-${esc(String(v))}"` : '';
        const text = esc(String(v ?? '—'));
        if (rowLink && ki === linkKeyIndex) {
          const href = esc(rowLink.replace('{id}', row.id));
          return `<td><a href="${href}">${text}</a></td>`;
        }
        return `<td><span${cls}>${text}</span></td>`;
      }).join('');
      const rowName = row.name || row.title || '';
      const needsInputConfirm = deleteTool && deleteTool.includes('project');
      const confirmAttr = needsInputConfirm && rowName ? ` data-confirm-input="${esc(rowName)}"` : '';
      const nameAttr = rowName ? ` data-name="${esc(rowName)}"` : '';
      const actions = deleteTool ? `<td class="text-right"><button class="button button-ghost button-sm" data-action="delete" data-tool="${esc(deleteTool)}" data-id="${row.id}"${nameAttr}${confirmAttr}>×</button></td>` : '';
      return `<tr>${cells}${actions}</tr>`;
    }).join('')}</tbody>
  </table></div>`;
  return html;
}

MC.registerRenderer('table', renderTable);

function bindIconPicker(container) {
  container.querySelectorAll('.icon-picker-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      const picker = btn.closest('.icon-picker');
      picker.querySelectorAll('.icon-picker-btn').forEach(b => b.classList.remove('selected'));
      btn.classList.add('selected');
      const hidden = picker.parentElement.querySelector('input[type="hidden"][data-field]');
      if (hidden) hidden.value = btn.dataset.icon;
    });
  });
}

function bindImageUpload(container) {
  container.querySelectorAll('.image-upload-area').forEach(area => {
    const btn = area.querySelector('.image-upload-btn');
    const fileInput = area.querySelector('.image-upload-input');
    const preview = area.querySelector('.image-upload-preview');
    const hidden = area.parentElement.querySelector('.image-upload-value');
    if (!btn || !fileInput || !hidden) return;

    btn.addEventListener('click', () => fileInput.click());

    fileInput.addEventListener('change', () => {
      const file = fileInput.files[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = () => {
        const img = new Image();
        img.onload = () => {
          // Crop to center square, then resize to 64x64
          const size = Math.min(img.width, img.height);
          const sx = (img.width - size) / 2;
          const sy = (img.height - size) / 2;
          const canvas = document.createElement('canvas');
          canvas.width = 64;
          canvas.height = 64;
          const cCtx = canvas.getContext('2d');
          cCtx.drawImage(img, sx, sy, size, size, 0, 0, 64, 64);
          const dataUrl = canvas.toDataURL('image/png');
          hidden.value = dataUrl;
          preview.innerHTML = `<img src="${dataUrl}" width="64" height="64" style="border-radius:var(--radius);border:1px solid hsl(var(--border));"/>`;
          btn.textContent = MC.t('common.change_image', 'Change image');
        };
        img.src = reader.result;
      };
      reader.readAsDataURL(file);
    });
  });
}

function renderCreateForm(toolName, schema, ctx) {
  const props = schema && schema.input_schema && schema.input_schema.properties ? schema.input_schema.properties : {};
  const required = schema && schema.input_schema && schema.input_schema.required ? schema.input_schema.required : [];
  const fields = Object.entries(props).filter(([k]) => k !== 'id');
  const csrfToken = (ctx && ctx.csrf) ? ctx.csrf : '';

  if (fields.length === 0) return '';

  let html = '<div class="create-form hidden card" style="margin-top:0.75rem;padding:1rem;">';
  html += '<div class="create-form-grid">';
  for (const [key, spec] of fields) {
    const label = key.replace(/_/g, ' ');
    const req = required.includes(key) ? ' required' : '';
    if (spec.enum) {
      html += `<label class="create-form-label">${esc(label)}<select data-field="${esc(key)}" class="form-select"${req}>${spec.enum.map(v => `<option value="${esc(v)}">${esc(v)}</option>`).join('')}</select></label>`;
    } else if (spec.format === 'status-select') {
      html += `<label class="create-form-label">${esc(label)}<select data-field="${esc(key)}" class="form-select" data-status-select="true"></select></label>`;
    } else if (spec.format === 'type-select') {
      html += `<label class="create-form-label">${esc(label)}<select data-field="${esc(key)}" class="form-select" data-type-select="true"></select></label>`;
    } else if (spec.format === 'project-select') {
      if (ctx && ctx.projectId) {
        // Inside project context — lock to current project (hidden input, no UI)
        html += `<input type="hidden" data-field="${esc(key)}" data-project-select="true" value="${ctx.projectId}"/>`;
      } else {
        html += `<label class="create-form-label">${esc(label)}<select data-field="${esc(key)}" class="form-select" data-project-select="true"><option value="">— no project —</option></select></label>`;
      }
    } else if (spec.format === 'image-upload') {
      html += `<label class="create-form-label" style="grid-column:1/-1">${esc(label)}
        <input type="hidden" data-field="${esc(key)}" class="image-upload-value"/>
        <div class="image-upload-area">
          <div class="image-upload-preview"></div>
          <div class="image-upload-controls">
            <input type="file" accept="image/*" class="image-upload-input" style="display:none"/>
            <button type="button" class="button button-ghost button-sm image-upload-btn">Choose image</button>
            <span class="text-muted-foreground text-xs">${MC.t('common.crop_hint', 'Will be cropped to 64×64')}</span>
          </div>
        </div>
      </label>`;
    } else if (spec.format === 'markdown') {
      html += `<label class="create-form-label" style="grid-column:1/-1">${esc(label)}<div data-editor data-field-name="${esc(key)}" data-preview-url="/api/preview-markdown" data-csrf-token="${esc(csrfToken)}"${req}></div></label>`;
    } else if (spec.format === 'textarea') {
      html += `<label class="create-form-label" style="grid-column:1/-1">${esc(label)}<textarea data-field="${esc(key)}" class="form-input" rows="4"${req}></textarea></label>`;
    } else if (spec.format === 'date') {
      html += `<label class="create-form-label">${esc(label)}<input type="date" data-field="${esc(key)}" class="form-input"${req}/></label>`;
    } else if (spec.format === 'color') {
      const defaultVal = spec.default || '#6366f1';
      html += `<label class="create-form-label">${esc(label)}<input type="color" data-field="${esc(key)}" class="form-input" value="${esc(defaultVal)}"${req}/></label>`;
    } else if (spec.type === 'integer' || spec.type === 'number') {
      const defVal = spec.default != null ? ` value="${esc(String(spec.default))}"` : '';
      html += `<label class="create-form-label">${esc(label)}<input type="number" data-field="${esc(key)}" class="form-input"${defVal}${req}/></label>`;
    } else if (spec.type === 'boolean') {
      html += `<label class="create-form-label"><input type="checkbox" data-field="${esc(key)}"/> ${esc(label)}</label>`;
    } else if (spec.type === 'array') {
      html += `<label class="create-form-label">${esc(label)}<input type="text" data-field="${esc(key)}" class="form-input" placeholder="${MC.t('common.comma_separated', 'comma-separated')}"/></label>`;
    } else {
      html += `<label class="create-form-label">${esc(label)}<input type="text" data-field="${esc(key)}" class="form-input"${req}/></label>`;
    }
  }
  html += '</div>';
  html += `<div style="margin-top:0.75rem;display:flex;gap:0.5rem;"><button class="button button-primary" data-action="submit-create" data-tool="${esc(toolName)}">${MC.t('common.save', 'Save')}</button><button class="button button-ghost" data-action="show-create">${MC.t('common.cancel', 'Cancel')}</button></div>`;
  html += '</div>';
  return html;
}

// --- Extension point rendering ---

function renderExtensionBadges(extensions, pointID, itemID) {
  if (!extensions || extensions.length === 0) return '';
  let html = '';
  for (const ext of extensions) {
    if (ext.point !== pointID) continue;
    const data = ext.data;
    if (!data) continue;
    // Extension returns {items: {id: {text, variant}}} for per-item badges
    if (data.items && data.items[String(itemID)]) {
      const badge = data.items[String(itemID)];
      const variant = badge.variant || 'default';
      const liveAttrs = badge.started_at ? ` data-timer-started="${esc(badge.started_at)}" data-timer-paused-sec="${esc(badge.total_paused_sec || '0')}" data-timer-completed-sec="${esc(badge.completed_sec || '0')}"${badge.paused_at ? ' data-timer-paused-at="' + esc(badge.paused_at) + '"' : ''}` : '';
      html += ` <span class="badge badge-${esc(variant)}" title="${esc(ext.source_plugin)}"${liveAttrs}>${esc(badge.text)}</span>`;
    }
    // Extension returns {all: {text, variant}} for a badge on every item
    if (data.all) {
      const badge = data.all;
      const variant = badge.variant || 'default';
      html += ` <span class="badge badge-${esc(variant)}" title="${esc(ext.source_plugin)}">${esc(badge.text)}</span>`;
    }
  }
  return html;
}

// --- Extension action rendering ---

function renderExtensionActions(extensions, pointID, itemID) {
  if (!extensions || extensions.length === 0) return '';
  let html = '';
  for (const ext of extensions) {
    if (ext.point !== pointID) continue;
    const data = ext.data;
    if (!data) continue;
    const action = (data.items && data.items[String(itemID)]) || data.all;
    if (!action || !action.action) continue;
    const label = action.label || action.action;
    const sourcePlugin = ext.source_plugin || '';
    html += ` <button class="button button-ghost button-xs" data-action="ext-action" data-ext-plugin="${esc(sourcePlugin)}" data-ext-tool="${esc(action.action)}" data-item-id="${itemID}" title="${esc(label)}">${esc(label)}</button>`;
  }
  return html;
}

// --- Server heartbeat ---
(function() {
  var alive = true;
  function heartbeat() {
    fetch('/health', { method: 'GET', cache: 'no-store' })
      .then(function(r) {
        if (!r.ok) throw new Error(r.status);
        if (!alive) {
          alive = true;
          setStatus(true);
        }
      })
      .catch(function() {
        if (alive) {
          alive = false;
          setStatus(false);
        }
      });
  }
  function setStatus(online) {
    var dot = document.querySelector('.sidebar-footer-status .status-dot');
    var text = document.querySelector('.footer-status-text');
    if (dot) {
      dot.className = online
        ? 'status-dot active animate-pulse'
        : 'status-dot error';
    }
    if (text) {
      text.textContent = online
        ? MC.t('nav.system_active', 'System Active')
        : MC.t('nav.system_offline', 'System Offline');
    }
  }
  setInterval(heartbeat, 5000);
})();

