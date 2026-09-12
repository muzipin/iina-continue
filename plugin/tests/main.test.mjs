import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');

test('manifest has one English fallback and localized metadata', () => {
  const x = JSON.parse(read('../../Info.json'));
  assert.equal(x.version, '0.1.1');
  assert.equal(x.name, 'Continue');
  assert.equal(x.author.name, 'MZZP');
  assert.equal(x.entry, 'main.js');
  assert.equal(x.sidebarTab.name, 'Continue');
  assert.equal(x.description, x.localized.en.description);
  assert.doesNotMatch(x.description, /[\u3400-\u9fff]/);
  assert.equal(x.localized.en.name, 'Continue');
  assert.equal(x.localized.en.sidebarTab.name, 'Continue');
  assert.equal(x.localized['zh-Hans'].name, 'Continue');
  assert.equal(x.localized['zh-Hans'].sidebarTab.name, 'Continue');
  assert.equal(x.localized.en.description, 'Continue what is playing in IINA on another device.');
  assert.equal(x.localized['zh-Hans'].description, '在另一个设备继续 IINA 正在播放的内容');
  assert.equal(x.ghRepo, 'muzipin/iina-continue');
  assert.equal(x.ghVersion, 2);
  assert.match(read('../../helper/main.go'), /version\s*=\s*"0\.1\.1"/);
  assert.deepEqual(x.permissions, ['file-system']);
});

test('phone page is self-contained and uses only native media controls', () => {
  const page = read('../../helper/web/player.html');
  assert.match(page, /<video[^>]+controls/);
  assert.doesNotMatch(page, /https?:\/\/|id="start"|id="seekbar"|id="progress"|id="rewind"|id="forward"/);
  assert.equal((page.match(/<button\b/g) || []).length, 1);
  assert.match(page, /<button id="end"/);
});

test('the bundled helper owns the private control channel', () => {
  const main = read('../main.js');
  assert.match(main, /continue-helper/);
  assert.match(main, /"client"/);
  assert.match(main, /file\.exists\(cached\+"\/continue-helper"\)/);
  assert.doesNotMatch(main, /iina\.http/);
});

test('IINA 1.4 is supported and newer localization remains optional', () => {
  const main = read('../main.js');
  const readme = read('../../README.md');
  assert.match(main, /core\.getVersion/);
  assert.match(main, /preferredLocalizations==="function"/);
  assert.match(main, />=4/);
  assert.match(readme, /IINA 1\.4\.0/);
  assert.match(readme, /IINA 1\.5/);
});

async function compatibilityState(version, arm64) {
  const handlers = {}, messages = [], menuItems = [], events = {};
  const sidebar = {
    loadFile() {}, show() {},
    onMessage(name, callback) { handlers[name] = callback; },
    postMessage(name, data) { messages.push({name, data}); },
  };
  const context = {
    iina: {
      core: {getVersion: () => ({iina: version}), status: {}},
      mpv: {}, event: {on: (name, callback) => { events[name] = callback; }},
      menu: {item: (title, action) => ({title, action}), addItem: item => menuItems.push(item)},
      utils: {exec: () => Promise.resolve({status: 0, stdout: arm64 ? '1\n' : '0\n'})},
      file: {}, sidebar, preferences: {}, console: {log() {}},
    },
    setTimeout, clearTimeout, JSON, String, Number, Object, Array, RegExp, decodeURIComponent,
  };
  vm.runInNewContext(read('../main.js'), context);
  await new Promise(resolve => setImmediate(resolve));
  handlers.getState();
  return {state: messages.at(-1).data, menuItems, handlers, messages};
}

test('runtime compatibility guard accepts IINA 1.4 on M chips and blocks Intel', async () => {
  const arm = await compatibilityState('1.4.0', true);
  assert.equal(arm.state.phase, 'idle');
  assert.equal(arm.menuItems[0].title, 'Continue on');
  const intel = await compatibilityState('1.5.0-beta1', false);
  assert.equal(intel.state.phase, 'unsupported');
  assert.match(intel.state.message, /Apple silicon/);
  const old = await compatibilityState('1.3.5', true);
  assert.equal(old.state.phase, 'unsupported');
  assert.match(old.state.message, /IINA 1\.4\.0/);

  arm.handlers.language('zh-CN');
  arm.handlers.getState();
  assert.equal(arm.menuItems[0].title, '开始续播');
  assert.equal(arm.messages.at(-1).data.language, 'zh');
  assert.match(arm.messages.at(-1).data.message, /播放画质/);
});

test('native players own switching and the sidebar has one service action', () => {
  const main = read('../main.js');
  const html = read('../ui/continue.html');
  const ui = read('../ui/continue.js');
  const helper = read('../../helper/main.go');
  assert.match(main, /control\/continue/);
  assert.match(main, /control\/take-back/);
  assert.match(main, /view\.onMessage\("toggleSession",toggleSession\)/);
  assert.match(main, /event\.on\("mpv\.pause\.changed",onPauseChanged\)/);
  assert.match(main, /mpv\.getFlag\("pause"\)/);
  assert.equal((html.match(/<button\b/g) || []).length, 1);
  assert.match(html, /id="action"/);
  assert.doesNotMatch(html, /id="phase"/);
  assert.doesNotMatch(ui, /c\.phases|phase\.textContent/);
  assert.match(ui, /currentState\.active\?c\.stop:c\.start/);
  assert.match(helper, /case "request_to_phone"/);
  assert.doesNotMatch(main, /menu\.item\(t\("endMenu"\)/);
});

test('tapping native Play activates phone playback directly', () => {
  const phone = read('../../helper/web/player.js');
  assert.match(phone, /video\.addEventListener\('play'/);
  assert.match(phone, /send\('ready',null,false\)/);
  assert.match(phone, /send\('request_to_phone',0,true\)/);
  assert.match(phone, /state\.state==='MAC_ACTIVE'/);
  assert.match(phone, /state\.state==='PREPARING'/);
  assert.match(phone, /video\.play\(\)/);
  assert.match(phone, /suppress=false;startingPlayback=true/);
  assert.match(phone, /setInterval\(refresh,500\)/);
});

test('native seeking commits the latest HLS position and Mac return is phone-confirmed', () => {
  const phone = read('../../helper/web/player.js');
  const helper = read('../../helper/main.go');
  assert.match(phone, /video\.addEventListener\('seeking'/);
  assert.match(phone, /pendingSeek=absolutePosition\(\)/);
  assert.match(phone, /setTimeout\(commitSeek,160\)/);
  assert.match(phone, /send\('seek',target,!wasPlaying\)/);
  assert.match(phone, /s\.state==='RETURNING'/);
  assert.match(phone, /send\('return_to_mac',absolutePosition\(\),true\)/);
  assert.match(helper, /ss\.State = "RETURNING"/);
  assert.match(helper, /case "return_to_mac":/);
});

test('long HLS pauses reload the native player without adding custom controls', () => {
  const page = read('../../helper/web/player.html');
  const phone = read('../../helper/web/player.js');
  assert.match(page, /<video[^>]+controls/);
  assert.equal((page.match(/<button\b/g) || []).length, 1);
  assert.match(phone, /function recoverAfterPause\(\)/);
  assert.match(phone, /Date\.now\(\)-pausedAt>3000/);
  assert.match(phone, /video\.load\(\);suppress=false;video\.play\(\)/);
});

test('text links are removed and QR disclosure is private and accessible', () => {
  const html = read('../ui/continue.html');
  const ui = read('../ui/continue.js');
  const css = read('../ui/continue.css');
  assert.doesNotMatch(html, /id="url"|class="link"|clipboard|复制链接/);
  assert.doesNotMatch(ui, /clipboard|execCommand\('copy'\)/);
  assert.match(html, /id="qr" role="button" tabindex="0" aria-pressed="false"/);
  assert.match(ui, /qr\.onkeydown/);
  assert.match(ui, /currentState\.active&&!terminal&&currentState\.url/);
  assert.match(html, /<select id="quality">/);
  assert.match(css, /\.quality\{[^}]*display:flex/);
  assert.match(css, /-webkit-backdrop-filter:blur\(14px\)/);
  assert.match(css, /#qr:hover:after/);
  assert.match(css, /transition:opacity/);
  assert.match(css, /:focus-visible/);
  assert.match(css, /AccentColor/);
  assert.match(ui, /qr\.removeAttribute\('title'\)/);
  assert.doesNotMatch(html, /id="security"/);
  assert.doesNotMatch(ui, /\.title=|qrTitle|security:/);
  assert.doesNotMatch(html, /class="logo"/);
});

test('continue preserves subtitles, playback intent, and track updates', () => {
  const main = read('../main.js');
  const phone = read('../../helper/web/player.js');
  assert.match(main, /sub-delay/);
  assert.match(main, /secondary-sub-delay/);
  assert.doesNotMatch(main, /paused:false/);
  assert.match(main, /core\.seekTo\(e\.positionSeconds\);core\.resume\(\)/);
  assert.match(main, /mpv\.track-list\.changed/);
  assert.match(main, /helper&&helper\.state==="waiting"/);
  assert.match(phone, /track\.track\.mode='showing'/);
  assert.match(phone, /s\.revision<revision/);
});

test('Mac and phone keep accessible responsive shells', () => {
  const mac = read('../ui/continue.css');
  const phone = read('../../helper/web/player.css');
  for (const css of [mac, phone]) {
    assert.match(css, /border-radius:(?:7|13)px/);
    assert.match(css, /border:1px solid var\(--/);
    assert.match(css, /@media\(prefers-color-scheme:dark\)/);
    assert.match(css, /html,body\{(?:min-)?height:100%;overflow(?:-x)?:hidden/);
  }
  assert.match(mac, /h1\{[^}]*border-radius:8px[^}]*background:#111[^}]*color:#fff/);
});

test('Mac and phone use their own supported localization source', () => {
  const main = read('../main.js');
  const ui = read('../ui/continue.js');
  const phone = read('../../helper/web/player.js');
  assert.match(main, /preferredLocalizations/);
  assert.match(main, /view\.onMessage\("language",setLanguage\)/);
  assert.match(main, /(?:language:language|\.language=language)/);
  assert.match(ui, /applyLanguage\(s\.language\)/);
  assert.match(ui, /navigator\.language/);
  assert.match(phone, /navigator\.language/);
});

test('release tooling derives the version and labels the arm64 package', () => {
  const info = JSON.parse(read('../../Info.json'));
  const pack = read('../../scripts/pack.sh');
  const verify = read('../../scripts/verify-package.sh');
  const helper = read('../../scripts/build-helper.sh');
  assert.equal(info.identifier, 'dev.mzzp.iina-continue');
  assert.match(pack, /Info\.json/);
  assert.match(pack, /dist\/v\$\{version\}/);
  assert.match(pack, /iina-continue-v\$\{version\}-arm64\.iinaplgz/);
  assert.match(pack, /ffmpeg-9\.0\.1\.tar\.xz/);
  assert.match(pack, /SHA256SUMS/);
  assert.match(verify, /shasum -a 256 -c SHA256SUMS/);
  assert.match(verify, /minos 12\\\.0/);
  assert.match(helper, /minos 12\\\.0/);
  const makefile = read('../../Makefile');
  assert.doesNotMatch(makefile + helper, /\/private\/tmp|GOCACHE|GOPATH/);
  assert.match(makefile, /"\$\(GO_BIN\)" test/);
  assert.match(makefile, /GO_BIN="\$\(GO_BIN\)"/);
  assert.match(pack, /README\.md/);
  assert.match(pack, /"\$root\/plugin\/main\.js"/);
  assert.match(pack, /"\$root\/build\/helper\/continue-helper"/);
  assert.match(verify, /cmp "\$root\/plugin\/main\.js" "\$tmp\/main\.js"/);
  assert.match(verify, /GNU GENERAL PUBLIC LICENSE/);
});

test('lifecycle resets stale sessions and reports helper exit and expiry', () => {
  const main = read('../main.js');
  const helper = read('../../helper/main.go');
  assert.match(main, /iina\.file-loaded",resetForNewFile/);
  assert.match(main, /interrupted/);
  assert.match(main, /e\.type==="expired"/);
  assert.match(helper, /heartbeatLate = 10 \* time\.Second/);
  assert.match(helper, /func \(s \*server\) expireSession\(\) bool/);
});

test('connection and user-ended copy follow actual causes', () => {
  const main = read('../main.js');
  assert.match(main, /phase:helper\.connected\?"connected":"waiting"/);
  assert.match(main, /message=endingByUser\?t\("ended"\):t\("phoneEnded"\)/);
  assert.match(main, /if\(!wasMac\)\{core\.seekTo\(e\.positionSeconds\);core\.pause\(\)\}/);
});

test('the active session keeps its QR and binds to one phone', () => {
  const ui = read('../ui/continue.js');
  const helper = read('../../helper/main.go');
  assert.match(ui, /if\(currentState\.active&&!terminal&&currentState\.url\)\{setCode/);
  assert.match(ui, /previous==='waiting'.*classList\.add\('concealed'\)/);
  assert.match(helper, /ss\.clientIP = host/);
  assert.match(helper, /ss\.clientIP == "" \|\| ss\.clientIP == host/);
});

test('the helper exposes only the live control surface', () => {
  const helper = read('../../helper/main.go');
  assert.doesNotMatch(helper, /HandleFunc\("\/(?:health|control\/session|control\/events)/);
  assert.doesNotMatch(helper, /func \(s \*server\) (?:health|createSession|controlEvents)/);
  assert.doesNotMatch(helper, /Events\s+\[\]event|\.Events\s*=\s*append|json:"at"|connected\s+bool/);
  assert.match(helper, /func \(s \*server\) control\(/);
});

test('runtime assets do not call internet services', () => {
  const files = [read('../main.js'), read('../ui/continue.js'), read('../../helper/web/player.js')].join('\n');
  assert.doesNotMatch(files, /https:\/\//);
  assert.doesNotMatch(files, /XMLHttpRequest|WebSocket/);
  assert.match(read('../ui/qrcode-LICENSE.txt'), /MIT License/);
});

test('product files use the Continue name without retired Apple terminology', () => {
  const files = [
    read('../../Info.json'), read('../main.js'), read('../ui/continue.html'),
    read('../ui/continue.js'), read('../../helper/main.go'),
    read('../../helper/web/player.html'), read('../../helper/web/player.js'),
    read('../../README.md'), read('../../README.zh-CN.md'),
  ].join('\n');
  assert.doesNotMatch(files, new RegExp(['continu', 'ity|hand', 'off'].join(''), 'i'));
  assert.doesNotMatch(files, /Start Continue|End Continue|Current Video|Play a local video/);
});
