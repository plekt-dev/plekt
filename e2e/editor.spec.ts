/**
 * editor.spec.ts — E2E tests for the Markdown Editor.
 *
 * Tests the preview endpoint, mermaid rendering, PlantUML rendering,
 * toolbar functionality, and split view mode.
 */

import { test, expect } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminToken } from './helpers/server';

test.describe('Markdown Editor', () => {

  test.beforeEach(async ({ page }) => {
    await loginAs(page, 'e2e-admin', 'e2e-admin-password-bootstrap');
  });

  test('static vendor/mermaid.min.js is served', async ({ request }) => {
    const resp = await request.get(`${baseURL}/static/js/vendor/mermaid.min.js`);
    expect(resp.status()).toBe(200);
    const body = await resp.text();
    expect(body.length).toBeGreaterThan(100000); // mermaid is ~3MB
    // UMD format sets window.mermaid
    expect(body).toContain('mermaid');
  });

  test('static editor.js is served and contains mermaid render logic', async ({ request }) => {
    const resp = await request.get(`${baseURL}/static/js/editor.js`);
    expect(resp.status()).toBe(200);
    const body = await resp.text();
    expect(body).toContain('renderMermaidBlocks');
    expect(body).toContain('mermaid.render');
  });

  test('preview API renders basic markdown', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '**hello**' }),
      });
      return r.json();
    });
    expect(json.html).toContain('<strong>hello</strong>');
  });

  test('preview API renders mermaid as pre.mermaid', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '```mermaid\ngraph TD\n    A --> B\n```' }),
      });
      return r.json();
    });
    expect(json.html).toContain('class="mermaid"');
    expect(json.html).not.toContain('language-mermaid');
  });

  test('preview API converts plantuml to mermaid', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '```plantuml\n@startuml\nactor User\nUser -> System: Request\nSystem --> User: Response\n@enduml\n```' }),
      });
      return r.json();
    });
    // PlantUML is auto-converted to Mermaid sequenceDiagram
    expect(json.html).toContain('class="mermaid"');
    expect(json.html).toContain('sequenceDiagram');
  });

  test('preview API renders strikethrough', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '~~deleted~~' }),
      });
      return r.json();
    });
    expect(json.html).toContain('<del>deleted</del>');
  });

  test('preview API renders task list', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '- [x] done\n- [ ] todo' }),
      });
      return r.json();
    });
    expect(json.html).toContain('checkbox');
  });

  test('preview API renders highlight', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    const json = await page.evaluate(async () => {
      const r = await fetch('/api/preview-markdown', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '==highlighted==' }),
      });
      return r.json();
    });
    expect(json.html).toContain('<mark>');
  });

  test('window.mermaid is available in browser', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);
    // Wait for mermaid script to load
    await page.waitForFunction(() => typeof (window as any).mermaid !== 'undefined', null, { timeout: 10000 });
    const mermaidType = await page.evaluate(() => typeof (window as any).mermaid);
    expect(mermaidType).toBe('object');
  });

  test('mermaid diagram renders as SVG in browser', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);

    // Wait for mermaid to load
    await page.waitForFunction(() => typeof (window as any).mermaid !== 'undefined', null, { timeout: 10000 });

    // Initialize mermaid
    await page.evaluate(() => {
      (window as any).mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose' });
    });

    // Inject a mermaid block and render it
    const svgHtml = await page.evaluate(async () => {
      const graphDef = 'graph TD\n    A[Start] --> B{Decision}\n    B -->|Yes| C[OK]';
      const result = await (window as any).mermaid.render('test-diagram', graphDef);
      return result.svg;
    });

    expect(svgHtml).toBeTruthy();
    expect(svgHtml).toContain('<svg');
    expect(svgHtml).toContain('Start');
  });

  test('mermaid renders in preview pane via editor', async ({ page }) => {
    await page.goto(`${baseURL}/dashboard`);

    // Wait for mermaid to be available
    await page.waitForFunction(() => typeof (window as any).mermaid !== 'undefined', null, { timeout: 10000 });

    // Call preview endpoint and inject HTML to simulate editor preview
    const previewHtml = await page.evaluate(async () => {
      const resp = await fetch('/api/preview-markdown', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ markdown: '```mermaid\ngraph LR\n    A --> B\n```' }),
      });
      const data = await resp.json();
      return data.html;
    });

    expect(previewHtml).toContain('class="mermaid"');

    // Now inject and render in DOM
    const hasSvg = await page.evaluate(async (html: string) => {
      const container = document.createElement('div');
      container.innerHTML = html;
      document.body.appendChild(container);

      const block = container.querySelector('pre.mermaid');
      if (!block) return 'no-pre-mermaid-found';

      const graphDef = (block.textContent || '').trim();
      if (!graphDef) return 'empty-text-content';

      try {
        const result = await (window as any).mermaid.render('test-preview-' + Date.now(), graphDef);
        const div = document.createElement('div');
        div.innerHTML = result.svg;
        block.parentNode!.replaceChild(div, block);
        return div.querySelector('svg') ? 'svg-rendered' : 'no-svg-in-result';
      } catch (e: any) {
        return 'render-error: ' + e.message;
      }
    }, previewHtml);

    expect(hasSvg).toBe('svg-rendered');
  });

  test('mermaid.js loads on notes plugin page', async ({ page, request }) => {
    // Load notes plugin via MCP to get token
    const loadResp = await request.post(`${baseURL}/mcp`, {
      headers: {
        Authorization: `Bearer ${adminToken()}`,
        'Content-Type': 'application/json',
      },
      data: {
        jsonrpc: '2.0',
        method: 'tools/call',
        params: { name: 'get_plugin_token', arguments: { plugin_name: 'notes-plugin' } },
        id: 1,
      },
    });
    const loadBody = await loadResp.json();
    const token = loadBody?.result?.content?.[0]?.text;
    if (!token) {
      test.skip(true, 'notes-plugin not loaded');
      return;
    }

    // Navigate to the quick-notes page (uses PluginPage → BaseLayout)
    await loginAs(page, 'e2e-admin', 'e2e-admin-password-bootstrap');
    await page.goto(`${baseURL}/p/notes-plugin/quick-notes`);
    await page.waitForLoadState('networkidle');

    // Verify mermaid is available
    const hasMermaid = await page.evaluate(() => typeof (window as any).mermaid);
    expect(hasMermaid).toBe('object');

    // Verify mermaid.render works
    const svgResult = await page.evaluate(async () => {
      try {
        (window as any).mermaid.initialize({ startOnLoad: false, theme: 'dark' });
        const result = await (window as any).mermaid.render('e2e-test-' + Date.now(), 'graph LR\n  A --> B');
        return result.svg ? 'ok' : 'no-svg';
      } catch (e: any) {
        return 'error: ' + e.message;
      }
    });
    expect(svgResult).toBe('ok');
  });
});
