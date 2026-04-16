/**
 * Plekt — Markdown Editor (Production)
 * Vanilla JS, no external dependencies.
 *
 * Features:
 * - Extended toolbar: headings, bold, italic, strikethrough, highlight,
 *   code, link, image, lists, task lists, blockquote, HR, table, diagrams
 * - Keyboard shortcuts: Ctrl+B/I/K, Ctrl+Shift+X/H
 * - Split view mode (write + preview side by side)
 * - Mermaid.js client-side diagram rendering
 * - Auto-growing textarea
 *
 * Initialises all [data-editor] containers. Also exposes
 * window.initEditor(container) for dynamically created editors.
 */

(function () {
  'use strict';

  // Toolbar insertion snippets keyed by data-insert value.
  const SNIPPETS = {
    bold:          { before: '**', after: '**', placeholder: 'bold text' },
    italic:        { before: '_',  after: '_',  placeholder: 'italic text' },
    strikethrough: { before: '~~', after: '~~', placeholder: 'strikethrough' },
    highlight:     { before: '==', after: '==', placeholder: 'highlighted' },
    code:          { before: '`',  after: '`',  placeholder: 'code' },
    codeblock:     { before: '```\n', after: '\n```', placeholder: 'code here' },
    link:          { before: '[',  after: '](https://)', placeholder: 'link text' },
    image:         { before: '![', after: '](https://)', placeholder: 'alt text' },
    ul:            { before: '- ', after: '',   placeholder: 'list item', linePrefix: true },
    ol:            { before: '1. ', after: '',  placeholder: 'list item', linePrefix: true },
    tasklist:      { before: '- [ ] ', after: '', placeholder: 'task', linePrefix: true },
    blockquote:    { before: '> ', after: '',   placeholder: 'quote', linePrefix: true },
    hr:            { before: '\n---\n', after: '', placeholder: '', noSelect: true },
    table: {
      before: '\n| Column 1 | Column 2 | Column 3 |\n|----------|----------|----------|\n| ',
      after:  ' |          |          |\n',
      placeholder: 'cell',
      noSelect: false,
    },
    mermaid: {
      before: '```mermaid\ngraph TD\n    A[Start] --> B{Decision}\n    B -->|Yes| C[',
      after:  ']\n    B -->|No| D[Cancel]\n```',
      placeholder: 'OK',
    },
    plantuml: {
      before: '```plantuml\n@startuml\nactor User\nUser -> System: ',
      after:  '\nSystem --> User: Response\n@enduml\n```',
      placeholder: 'Request',
    },
  };

  // Keyboard shortcut map: key combo → snippet name.
  const SHORTCUTS = {
    'ctrl+b':       'bold',
    'ctrl+i':       'italic',
    'ctrl+k':       'link',
    'ctrl+shift+x': 'strikethrough',
    'ctrl+shift+h': 'highlight',
    'ctrl+shift+c': 'codeblock',
    'ctrl+shift+7': 'ol',
    'ctrl+shift+8': 'ul',
    'ctrl+shift+9': 'tasklist',
  };

  /**
   * Build the editor HTML structure inside an empty [data-editor] container.
   * Mirrors the server-side MarkdownEditor Templ component.
   * @param {HTMLElement} container
   * @param {string} fieldName
   * @param {string} initialValue
   */
  function buildEditorHTML(container, fieldName, initialValue) {
    const req = container.dataset.required === 'true' ? ' required' : '';
    container.className = (container.className + ' editor-container').trim();
    container.innerHTML = `
      <div class="editor-tabs">
        <button type="button" class="editor-tab" data-tab="write">Write</button>
        <button type="button" class="editor-tab" data-tab="preview">Preview</button>
        <button type="button" class="editor-tab active" data-tab="split">Split</button>
      </div>
      <div class="editor-toolbar" data-pane="write">
        <select class="editor-toolbar-select" data-insert="heading" title="Heading level">
          <option value="">H</option>
          <option value="1">H1</option>
          <option value="2">H2</option>
          <option value="3">H3</option>
          <option value="4">H4</option>
          <option value="5">H5</option>
          <option value="6">H6</option>
        </select>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="bold" title="Bold (Ctrl+B)"><strong>B</strong></button>
        <button type="button" class="editor-toolbar-btn" data-insert="italic" title="Italic (Ctrl+I)"><em>I</em></button>
        <button type="button" class="editor-toolbar-btn" data-insert="strikethrough" title="Strikethrough (Ctrl+Shift+X)"><s>S</s></button>
        <button type="button" class="editor-toolbar-btn" data-insert="highlight" title="Highlight"><mark>H</mark></button>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="code" title="Inline code">\`</button>
        <button type="button" class="editor-toolbar-btn" data-insert="codeblock" title="Code block">\`\`\`</button>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="link" title="Link (Ctrl+K)">&#128279;</button>
        <button type="button" class="editor-toolbar-btn" data-insert="image" title="Image">&#128247;</button>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="ul" title="Unordered list">&#8226;&#8212;</button>
        <button type="button" class="editor-toolbar-btn" data-insert="ol" title="Ordered list">1.</button>
        <button type="button" class="editor-toolbar-btn" data-insert="tasklist" title="Task list">&#9745;</button>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="blockquote" title="Blockquote">&#10077;</button>
        <button type="button" class="editor-toolbar-btn" data-insert="hr" title="Horizontal rule">&#8213;</button>
        <button type="button" class="editor-toolbar-btn" data-insert="table" title="Table">&#9638;</button>
        <span class="editor-toolbar-sep"></span>
        <button type="button" class="editor-toolbar-btn" data-insert="mermaid" title="Mermaid diagram">&#9672;</button>
        <button type="button" class="editor-toolbar-btn" data-insert="plantuml" title="PlantUML diagram">&#9670;</button>
      </div>
      <div class="editor-body" data-editor-body>
        <textarea
          name="${fieldName}"
          data-field="${fieldName}"
          class="form-input editor-textarea"
          data-pane="write"
          ${req}
        >${initialValue || ''}</textarea>
        <div class="editor-preview" data-pane="preview" style="display:none;"></div>
      </div>
    `;
  }

  /**
   * Initialise a single [data-editor] container.
   * @param {HTMLElement} container
   */
  function initEditor(container) {
    if (!container || container.__mcEditorInit) return;
    container.__mcEditorInit = true;

    const previewURL   = container.dataset.previewUrl || '/api/preview-markdown';
    const csrfToken    = container.dataset.csrfToken  || '';
    const fieldName    = container.dataset.fieldName  || '';

    // Read initial value from <script type="text/plain" data-editor-content> if present
    // (safe for multiline content with quotes, backticks, angle brackets).
    // Falls back to data-initial-value attribute for backward compatibility.
    let initialValue = '';
    const contentScript = container.querySelector('script[data-editor-content]');
    if (contentScript) {
      // Content is base64-encoded to safely handle </script> and special chars.
      try {
        initialValue = decodeURIComponent(escape(atob(contentScript.textContent.trim())));
      } catch (_) {
        initialValue = contentScript.textContent;
      }
      contentScript.remove();
    } else if (container.dataset.initialValue) {
      initialValue = container.dataset.initialValue;
    }

    // If the container has no textarea (injected empty by renderCreateForm),
    // build the full editor HTML structure before binding events.
    if (!container.querySelector('textarea')) {
      buildEditorHTML(container, fieldName, initialValue);
    }

    const textarea    = container.querySelector('textarea[data-field]') ||
                        container.querySelector('textarea');
    const previewPane = container.querySelector('[data-pane="preview"]');
    const toolbar     = container.querySelector('.editor-toolbar');
    const tabs        = container.querySelectorAll('.editor-tab');
    const editorBody  = container.querySelector('[data-editor-body]') || container;

    if (!textarea) return;

    // If the textarea was injected by renderCreateForm it will not have a name.
    // Apply fieldName from the container attribute.
    if (fieldName && !textarea.name) {
      textarea.name = fieldName;
    }
    if (fieldName && !textarea.dataset.field) {
      textarea.dataset.field = fieldName;
    }

    // --- Auto-grow textarea ---
    function autoGrow() {
      textarea.style.height = 'auto';
      textarea.style.height = Math.max(150, textarea.scrollHeight) + 'px';
    }
    textarea.addEventListener('input', autoGrow);

    // Track current mode — default to split view.
    let currentMode = 'split';

    // --- Tab switching ---
    tabs.forEach(tab => {
      tab.addEventListener('click', () => {
        const target = tab.dataset.tab;
        tabs.forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        currentMode = target;

        if (target === 'write') {
          showWritePane();
        } else if (target === 'preview') {
          showPreviewPane();
        } else if (target === 'split') {
          showSplitPane();
        }
      });
    });

    function showWritePane() {
      editorBody.classList.remove('editor-split');
      textarea.style.display = '';
      if (toolbar) toolbar.style.display = '';
      if (previewPane) previewPane.style.display = 'none';
      autoGrow();
    }

    function showPreviewPane() {
      editorBody.classList.remove('editor-split');
      textarea.style.display = 'none';
      if (toolbar) toolbar.style.display = 'none';
      if (previewPane) {
        previewPane.style.display = '';
        fetchPreview(textarea.value, previewPane, previewURL, csrfToken);
      }
    }

    function showSplitPane() {
      editorBody.classList.add('editor-split');
      textarea.style.display = '';
      if (toolbar) toolbar.style.display = '';
      if (previewPane) {
        previewPane.style.display = '';
        fetchPreview(textarea.value, previewPane, previewURL, csrfToken);
      }
      autoGrow();
    }

    // --- Live preview in split mode ---
    let splitTimer = null;
    textarea.addEventListener('input', () => {
      if (currentMode === 'split' && previewPane) {
        clearTimeout(splitTimer);
        splitTimer = setTimeout(() => {
          fetchPreview(textarea.value, previewPane, previewURL, csrfToken);
        }, 400);
      }
    });

    // --- Toolbar ---
    if (toolbar) {
      toolbar.addEventListener('click', e => {
        const btn = e.target.closest('[data-insert]');
        if (!btn || btn.tagName === 'SELECT') return;
        e.preventDefault();
        insertSnippet(textarea, btn.dataset.insert);
        textarea.focus();
      });

      // Heading selector.
      const headingSelect = toolbar.querySelector('[data-insert="heading"]');
      if (headingSelect) {
        headingSelect.addEventListener('change', () => {
          const level = headingSelect.value;
          if (!level) return;
          const prefix = '#'.repeat(parseInt(level, 10)) + ' ';
          insertLinePrefix(textarea, prefix);
          headingSelect.value = '';
          textarea.focus();
        });
      }
    }

    // --- Keyboard shortcuts ---
    textarea.addEventListener('keydown', e => {
      const key = buildShortcutKey(e);
      const snippetName = SHORTCUTS[key];
      if (snippetName) {
        e.preventDefault();
        insertSnippet(textarea, snippetName);
      }

      // Tab key inserts spaces in textarea.
      if (e.key === 'Tab' && !e.shiftKey) {
        e.preventDefault();
        insertAtCursor(textarea, '    ');
      }
    });

    // --- Default to split mode ---
    showSplitPane();

    // --- Form submit sync ---
    const form = container.closest('form');
    if (form) {
      form.addEventListener('submit', () => {
        // textarea already holds the authoritative value.
      }, { capture: true });
    }
  }

  /**
   * Build a normalized shortcut key string from a KeyboardEvent.
   * @param {KeyboardEvent} e
   * @returns {string}
   */
  function buildShortcutKey(e) {
    const parts = [];
    if (e.ctrlKey || e.metaKey) parts.push('ctrl');
    if (e.shiftKey) parts.push('shift');
    if (e.altKey) parts.push('alt');
    parts.push(e.key.toLowerCase());
    return parts.join('+');
  }

  /**
   * Insert raw text at the current cursor position.
   * @param {HTMLTextAreaElement} ta
   * @param {string} text
   */
  function insertAtCursor(ta, text) {
    const start = ta.selectionStart;
    ta.value = ta.value.slice(0, start) + text + ta.value.slice(start);
    const pos = start + text.length;
    ta.setSelectionRange(pos, pos);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  /**
   * Insert a line prefix at the beginning of the current line.
   * @param {HTMLTextAreaElement} ta
   * @param {string} prefix
   */
  function insertLinePrefix(ta, prefix) {
    const start = ta.selectionStart;
    const before = ta.value.slice(0, start);
    const lineStart = before.lastIndexOf('\n') + 1;
    ta.value = ta.value.slice(0, lineStart) + prefix + ta.value.slice(lineStart);
    const pos = start + prefix.length;
    ta.setSelectionRange(pos, pos);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  /**
   * Insert a markdown snippet around the current cursor selection.
   * @param {HTMLTextAreaElement} ta
   * @param {string} type
   */
  function insertSnippet(ta, type) {
    const snip = SNIPPETS[type];
    if (!snip) return;

    const start  = ta.selectionStart;
    const end    = ta.selectionEnd;
    const before = ta.value.slice(0, start);
    const sel    = ta.value.slice(start, end) || snip.placeholder;
    const after  = ta.value.slice(end);

    ta.value = before + snip.before + sel + snip.after + after;

    if (snip.noSelect) {
      const cursorPos = start + snip.before.length + sel.length + snip.after.length;
      ta.setSelectionRange(cursorPos, cursorPos);
    } else {
      // Select the inserted placeholder/text for easy replacement.
      const selStart = start + snip.before.length;
      const selEnd   = selStart + sel.length;
      ta.setSelectionRange(selStart, selEnd);
    }

    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  /**
   * Fetch rendered HTML for the preview pane.
   * After rendering, triggers mermaid rendering if available.
   * @param {string} markdown
   * @param {HTMLElement} pane
   * @param {string} previewURL
   * @param {string} csrfToken
   */
  function fetchPreview(markdown, pane, previewURL, csrfToken) {
    pane.innerHTML = '<span style="color: var(--muted-foreground); font-size: 0.8125rem;">Loading\u2026</span>';

    fetch(previewURL, {
      method:  'POST',
      headers: {
        'Content-Type':  'application/json',
        'X-CSRF-Token':  csrfToken,
      },
      body: JSON.stringify({ markdown }),
    })
      .then(res => {
        if (!res.ok) throw new Error('Preview failed (' + res.status + ')');
        return res.json();
      })
      .then(data => {
        pane.innerHTML = '<div class="markdown-body">' +
          (data.html || '<span style="color: var(--muted-foreground);">Nothing to preview</span>') +
          '</div>';
        // Render diagrams client-side.
        renderMermaidBlocks(pane);
        renderPlantUMLBlocks(pane);
      })
      .catch(err => {
        pane.innerHTML = '<span class="editor-preview-error">Preview error: ' + escapeHTML(err.message) + '</span>';
      });
  }

  /**
   * Render all <pre class="mermaid"> blocks using mermaid.render().
   * Uses per-block rendering for maximum reliability.
   * @param {HTMLElement} container
   */
  function renderMermaidBlocks(container) {
    if (typeof window.mermaid === 'undefined') return;
    var blocks = container.querySelectorAll('pre.mermaid');
    if (blocks.length === 0) return;

    var counter = 0;
    blocks.forEach(function (block) {
      // textContent decodes HTML entities: &gt; → >, &amp; → &
      var graphDef = (block.textContent || '').trim();
      if (!graphDef) return;

      var id = 'mc-mermaid-' + Date.now() + '-' + (counter++);

      window.mermaid.render(id, graphDef).then(function (result) {
        var div = document.createElement('div');
        div.className = 'mermaid-rendered';
        div.innerHTML = result.svg;
        if (block.parentNode) {
          block.parentNode.replaceChild(div, block);
        }
        if (result.bindFunctions) {
          result.bindFunctions(div);
        }
      }).catch(function (err) {
        console.error('[MC] mermaid render failed:', err);
        block.classList.add('mermaid-error');
        block.setAttribute('title', 'Diagram error: ' + err.message);
      });
    });
  }

  /**
   * Re-render PlantUML blocks client-side.
   * If a local PlantUML server is configured (data-plantuml-url on body),
   * encode and render as <img>. Otherwise keep the server-rendered output.
   * @param {HTMLElement} container
   */
  function renderPlantUMLBlocks(container) {
    // PlantUML is rendered server-side — nothing to do client-side
    // unless we need to re-encode for a local server.
  }

  function escapeHTML(str) {
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  // --- Boot ---

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('[data-editor]').forEach(initEditor);

    // Initialize mermaid.js (loaded synchronously from /static/js/vendor/).
    if (typeof window.mermaid !== 'undefined') {
      window.mermaid.initialize({
        startOnLoad: false,
        theme: 'dark',
        securityLevel: 'loose',
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      });
    }
  });

  // Expose for dynamic injection (renderCreateForm in main.js).
  window.initEditor = initEditor;
  window.fetchMarkdownPreview = fetchPreview;
}());
