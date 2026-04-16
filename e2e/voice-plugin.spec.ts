// voice-plugin.spec.ts — E2E tests for Slice 4a/4b voice-plugin frontend.
//
// The tests mock browser audio APIs (getUserMedia + MediaRecorder) and the
// plugin's action endpoints so no real microphone or upstream Whisper server
// is needed.
//
// Pre-reqs:
//   - voice-plugin is copied into e2e/test-plugins/voice-plugin
//   - e2e/test-config.yaml enables auto_load_on_startup, so the plugin boots
//     with the test core.

import { test, expect, Page } from '@playwright/test';
import { loginAs } from './helpers/auth';
import { baseURL, adminUsername, adminPassword } from './helpers/server';

const VOICE_DIR = './e2e/test-plugins/voice-plugin';

// ---------- Mocks ----------------------------------------------------------

// Injects fake getUserMedia / MediaRecorder into the page BEFORE any page
// script runs. The recorder immediately emits a tiny blob on stop() so the
// full state machine (idle -> recording -> transcribing -> idle) runs
// deterministically.
const FAKE_MEDIA_SCRIPT = `
(function(){
  function FakeTrack(){ this.stop = function(){}; this.kind = 'audio'; }
  function FakeStream(){
    var t = new FakeTrack();
    this.getTracks = function(){ return [t]; };
    this.getAudioTracks = function(){ return [t]; };
  }
  if (!navigator.mediaDevices) navigator.mediaDevices = {};
  navigator.mediaDevices.getUserMedia = function(){ return Promise.resolve(new FakeStream()); };

  function FakeRecorder(stream, opts){
    this.stream = stream;
    this.mimeType = (opts && opts.mimeType) || 'audio/webm';
    this.state = 'inactive';
    this.ondataavailable = null;
    this.onstop = null;
    this.onerror = null;
  }
  FakeRecorder.prototype.start = function(){ this.state = 'recording'; };
  FakeRecorder.prototype.stop = function(){
    var self = this;
    this.state = 'inactive';
    setTimeout(function(){
      if (self.ondataavailable) {
        try {
          self.ondataavailable({ data: new Blob([new Uint8Array([1,2,3,4])], { type: self.mimeType }) });
        } catch(e){}
      }
      if (self.onstop) { try { self.onstop(); } catch(e){} }
    }, 5);
  };
  FakeRecorder.isTypeSupported = function(){ return true; };
  window.MediaRecorder = FakeRecorder;
})();
`;

async function installFakeMedia(page: Page) {
  await page.addInitScript(FAKE_MEDIA_SCRIPT);
}

// Route /p/voice-plugin/action/<tool> with a stub response per tool name.
async function mockVoiceActions(
  page: Page,
  overrides: { getPrefs?: any; transcribe?: any; transcribeStatus?: number } = {},
) {
  const prefs = overrides.getPrefs ?? {
    whisper_url: 'http://test-whisper.internal:9000',
    language: 'auto',
    local_mode: true,
  };
  const transcribe = overrides.transcribe ?? { text: 'hello world' };
  const transcribeStatus = overrides.transcribeStatus ?? 200;

  await page.route('**/p/voice-plugin/action/voice.get_prefs', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(prefs),
    });
  });
  await page.route('**/p/voice-plugin/action/voice.transcribe', async route => {
    await route.fulfill({
      status: transcribeStatus,
      contentType: 'application/json',
      body: JSON.stringify(transcribe),
    });
  });
}

async function ensureLoaded(page: Page, name: string, dir: string): Promise<void> {
  const list = await page.request.get(`${baseURL}/admin/plugins`);
  const body = await list.text();
  if (body.includes(`/admin/plugins/${name}`)) return;
  await page.goto(`${baseURL}/admin/plugins`);
  const csrf = await page.inputValue('#plugin-load-form input[name="csrf_token"]');
  await page.request.post(`${baseURL}/admin/plugins/load`, {
    form: { csrf_token: csrf, dir },
    failOnStatusCode: false,
  });
}

// ---------- Tests ----------------------------------------------------------

test.beforeEach(async ({ page }) => {
  await installFakeMedia(page);
  await loginAs(page, adminUsername(), adminPassword());
  await ensureLoaded(page, 'voice-plugin', VOICE_DIR);
});

test('voice-plugin global.js injects MCVoice and mic button appears on textareas', async ({ page }) => {
  await mockVoiceActions(page);
  // Go to a page with a textarea. The plugins admin load form has a text input.
  await page.goto(`${baseURL}/admin/plugins`);
  // Wait for MCVoice bootstrap.
  await page.waitForFunction(() => typeof (window as any).MCVoice === 'object');
  // At least one mic button should be attached (class .mc-voice-btn).
  const btn = page.locator('button.mc-voice-btn').first();
  await expect(btn).toBeAttached({ timeout: 5_000 });
});

test('push-to-talk inserts transcript into the focused text field at caret', async ({ page }) => {
  await mockVoiceActions(page, { transcribe: { text: 'DICTATED' } });
  await page.goto(`${baseURL}/admin/plugins`);
  await page.waitForFunction(() => typeof (window as any).MCVoice === 'object');

  const dirInput = page.locator('input[name="dir"]').first();
  await dirInput.fill('before after');
  // Place caret between "before " and "after".
  await dirInput.evaluate((el: HTMLInputElement) => { el.setSelectionRange(7, 7); });

  // Find the mic button associated with this input. global.js tracks them
  // via WeakMap so we just pick the button closest to the input via
  // getBoundingClientRect comparison on the page side.
  await page.waitForFunction(() => document.querySelectorAll('button.mc-voice-btn').length > 0);
  const btnHandle = await page.evaluateHandle(() => {
    const input = document.querySelector('input[name="dir"]') as HTMLInputElement;
    const r = input.getBoundingClientRect();
    const btns = Array.from(document.querySelectorAll('button.mc-voice-btn')) as HTMLElement[];
    // closest by vertical proximity
    btns.sort((a, b) => {
      const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
      return Math.abs(ra.top - r.top) - Math.abs(rb.top - r.top);
    });
    return btns[0];
  });

  await btnHandle.dispatchEvent('pointerdown');
  // Give global.js a tick to transition into 'recording'.
  await page.waitForTimeout(50);
  await btnHandle.dispatchEvent('pointerup');

  // Wait for insertion.
  await expect(dirInput).toHaveValue('before DICTATEDafter', { timeout: 5_000 });
});

test('shows MC.confirm when whisper_url is empty', async ({ page }) => {
  await mockVoiceActions(page, {
    getPrefs: { whisper_url: '', language: 'auto', local_mode: true },
    transcribe: { error: 'not configured' },
    transcribeStatus: 400,
  });

  await page.goto(`${baseURL}/admin/plugins`);
  await page.waitForFunction(() => typeof (window as any).MCVoice === 'object');
  // Wait for prefs to be cached.
  await page.waitForFunction(() => (window as any).__voicePrefs !== undefined);

  const btnHandle = await page.evaluateHandle(() => {
    return document.querySelector('button.mc-voice-btn') as HTMLElement;
  });
  await btnHandle.dispatchEvent('pointerdown');
  await page.waitForTimeout(50);
  await btnHandle.dispatchEvent('pointerup');

  // MC.confirm renders <dialog id="mc-confirm-dialog"> with a message mentioning configuration.
  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible({ timeout: 5_000 });
  await expect(dialog).toContainText(/not configured|Whisper/i);
});

test('shows host grant prompt when transcribe reports host not granted', async ({ page }) => {
  await mockVoiceActions(page, {
    transcribe: { error: 'Host test-whisper.internal:9000 not granted — ask operator' },
  });
  await page.goto(`${baseURL}/admin/plugins`);
  await page.waitForFunction(() => typeof (window as any).MCVoice === 'object');
  await page.waitForFunction(() => (window as any).__voicePrefs && (window as any).__voicePrefs.whisper_url);

  const btnHandle = await page.evaluateHandle(
    () => document.querySelector('button.mc-voice-btn') as HTMLElement,
  );
  await btnHandle.dispatchEvent('pointerdown');
  await page.waitForTimeout(50);
  await btnHandle.dispatchEvent('pointerup');

  const dialog = page.locator('#mc-confirm-dialog');
  await expect(dialog).toBeVisible({ timeout: 5_000 });
  await expect(dialog).toContainText(/not granted|Permissions/i);
});

test('transcript insertion fires input event so forms remain submittable', async ({ page }) => {
  await mockVoiceActions(page, { transcribe: { text: 'X' } });
  await page.goto(`${baseURL}/admin/plugins`);
  await page.waitForFunction(() => typeof (window as any).MCVoice === 'object');

  // Attach an input event listener on the target field, verify it fires.
  const dirInput = page.locator('input[name="dir"]').first();
  await dirInput.fill('');
  await page.evaluate(() => {
    const el = document.querySelector('input[name="dir"]') as HTMLInputElement;
    (window as any).__inputEvents = 0;
    el.addEventListener('input', () => { (window as any).__inputEvents++; });
  });

  const btnHandle = await page.evaluateHandle(() => {
    const input = document.querySelector('input[name="dir"]') as HTMLInputElement;
    const r = input.getBoundingClientRect();
    const btns = Array.from(document.querySelectorAll('button.mc-voice-btn')) as HTMLElement[];
    btns.sort((a, b) => {
      const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
      return Math.abs(ra.top - r.top) - Math.abs(rb.top - r.top);
    });
    return btns[0];
  });
  await btnHandle.dispatchEvent('pointerdown');
  await page.waitForTimeout(50);
  await btnHandle.dispatchEvent('pointerup');

  await expect(dirInput).toHaveValue('X', { timeout: 5_000 });
  const count = await page.evaluate(() => (window as any).__inputEvents);
  expect(count).toBeGreaterThanOrEqual(1);
});
