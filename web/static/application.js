/* gopaste - vanilla frontend (no framework, no CDN).
   Wire contract:
     POST /api/pastes             -> { id }
     GET  /api/pastes/:id         -> { id, content }
     GET  /api/pastes/:id/raw     -> text/plain
   Served at "/" and "/:id" (SPA). */

/* global hljs */
(function () {
  'use strict';

  // File extension -> highlight.js language. Keys are alphabetized; every value
  // is a grammar the bundled highlight.js actually registers. Also used in
  // reverse to pick a display extension for a detected language.
  var EXT_MAP = {
    bash: 'bash',
    c: 'c',
    cc: 'cpp',
    coffee: 'coffeescript',
    cpp: 'cpp',
    cs: 'csharp',
    css: 'css',
    diff: 'diff',
    go: 'go',
    h: 'cpp',
    htm: 'xml',
    html: 'xml',
    http: 'http',
    ini: 'ini',
    java: 'java',
    js: 'javascript',
    json: 'json',
    kt: 'kotlin',
    less: 'less',
    lua: 'lua',
    m: 'objectivec',
    make: 'makefile',
    md: 'markdown',
    nginx: 'nginx',
    patch: 'diff',
    php: 'php',
    pl: 'perl',
    properties: 'properties',
    py: 'python',
    rb: 'ruby',
    rs: 'rust',
    scss: 'scss',
    sh: 'bash',
    sql: 'sql',
    swift: 'swift',
    toml: 'ini',
    ts: 'typescript',
    txt: '',
    xml: 'xml',
    yaml: 'yaml',
    yml: 'yaml'
  };

  function extForLanguage(lang) {
    for (var k in EXT_MAP) { if (EXT_MAP[k] === lang) return k; }
    return lang;
  }
  function languageForExt(ext) {
    return Object.prototype.hasOwnProperty.call(EXT_MAP, ext) ? EXT_MAP[ext] : ext;
  }

  function htmlEscape(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  // Highlight across old/new highlight.js APIs.
  function highlight(code, lang) {
    try {
      if (lang === 'txt' || lang === '') return { value: htmlEscape(code), language: 'text' };
      if (lang && hljs.getLanguage && hljs.getLanguage(lang)) {
        try { return hljs.highlight(code, { language: lang, ignoreIllegals: true }); }
        catch (e) { return hljs.highlight(lang, code); } // legacy 2-arg signature
      }
      return hljs.highlightAuto(code);
    } catch (e) {
      return { value: htmlEscape(code), language: '' };
    }
  }

  function App() {
    this.editor = document.getElementById('editor');
    this.box = document.getElementById('box');
    this.code = this.box.querySelector('code');
    this.gutter = document.getElementById('linenos');
    this.baseUrl = (window.GOPASTE_BASE_URL || '/');
    this.locked = false;
    this.key = null;
    this.data = '';
    this.bindButtons();
    this.bindShortcuts();
    this.bindEditor();
    this.initTheme();
  }

  // ---- networking ----
  App.prototype.request = function (method, path, body, onOk, onErr) {
    var xhr = new XMLHttpRequest();
    xhr.open(method, this.baseUrl + path, true);
    if (body != null) xhr.setRequestHeader('Content-Type', 'text/plain; charset=utf-8');
    xhr.onreadystatechange = function () {
      if (xhr.readyState !== 4) return;
      var json = null;
      try { json = JSON.parse(xhr.responseText); } catch (e) { /* not json */ }
      if (xhr.status >= 200 && xhr.status < 300) onOk(json, xhr);
      else if (onErr) onErr(json, xhr);
    };
    xhr.send(body == null ? null : body);
  };

  // ---- view state ----
  App.prototype.setMode = function (mode) {
    var el = document.getElementById('st-mode');
    el.textContent = mode;
    el.classList.toggle('view', mode === 'VIEW');
  };

  App.prototype.enable = function (acts) {
    var btns = document.querySelectorAll('.cmd-actions .btn');
    for (var i = 0; i < btns.length; i++) {
      var a = btns[i].getAttribute('data-act');
      btns[i].classList.toggle('disabled', acts.indexOf(a) === -1);
    }
  };

  App.prototype.setSaveState = function (saved) {
    var save = document.querySelector('[data-act="save"]');
    if (saved) {
      save.classList.add('success');
      save.classList.remove('primary');
      save.querySelector('.lbl').textContent = 'Saved';
    } else {
      save.classList.add('primary');
      save.classList.remove('success');
      save.querySelector('.lbl').textContent = 'Save';
    }
  };

  App.prototype.setStatus = function (opts) {
    // Build nodes with textContent (never innerHTML) so URL-derived key/lang
    // values can never be interpreted as markup.
    function boldSeg(id, show, prefixText, ledClass, value) {
      var el = document.getElementById(id);
      el.hidden = !show;
      if (!show) return;
      el.textContent = '';
      if (ledClass) {
        var led = document.createElement('span');
        led.className = ledClass;
        el.appendChild(led);
      }
      if (prefixText) el.appendChild(document.createTextNode(prefixText));
      var b = document.createElement('b');
      b.textContent = value || '';
      el.appendChild(b);
    }
    boldSeg('st-key', !!opts.key, 'key ', null, opts.key);
    boldSeg('st-lang', !!opts.lang, null, 'led blue', opts.lang);
    document.getElementById('st-count').textContent = opts.lines + ' lines / ' + opts.chars + ' chars';
    document.getElementById('st-hint').hidden = opts.mode === 'VIEW';
  };

  App.prototype.renderGutter = function (lineCount) {
    if (lineCount == null) { this.gutter.classList.add('prompt'); this.gutter.textContent = '>'; return; }
    this.gutter.classList.remove('prompt');
    var s = '';
    for (var i = 1; i <= lineCount; i++) s += i + '\n';
    this.gutter.textContent = s;
  };

  App.prototype.updateCounts = function () {
    var v = this.editor.value;
    var lines = v.length ? v.split('\n').length : 0;
    this.setStatus({ mode: 'NEW', lines: lines, chars: v.length });
  };

  // ---- actions ----
  App.prototype.newDocument = function (skipHistory) {
    this.locked = false; this.key = null; this.data = '';
    this.box.style.display = 'none';
    this.editor.style.display = '';
    this.editor.value = '';
    this.renderGutter(null);
    this.setMode('NEW');
    this.setSaveState(false);
    this.enable(['save', 'new']);
    this.setStatus({ mode: 'NEW', lines: 0, chars: 0 });
    if (!skipHistory) window.history.pushState(null, 'gopaste', this.baseUrl);
    document.title = 'gopaste';
    this.editor.focus();
  };

  App.prototype.showDocument = function (data, key, lang, pushExt) {
    var hi = highlight(data, lang);
    this.locked = true; this.key = key; this.data = data;
    this.code.innerHTML = hi.value;
    this.box.className = 'hljs';
    this.editor.style.display = 'none';
    this.box.style.display = 'block'; // explicit: #box CSS default is display:none
    var lineCount = data.split('\n').length;
    this.renderGutter(lineCount);
    this.setMode('VIEW');
    this.setSaveState(true);
    this.enable(['new', 'duplicate', 'raw', 'copy']);
    var language = hi.language || lang || '';
    this.setStatus({ mode: 'VIEW', key: key, lang: language || null, lines: lineCount, chars: data.length });
    document.title = 'gopaste - ' + key;
    if (pushExt !== false) {
      var url = this.baseUrl + key + (language ? '.' + extForLanguage(language) : '');
      window.history.pushState(null, 'gopaste-' + key, url);
    }
  };

  App.prototype.save = function () {
    if (this.locked) return;
    var data = this.editor.value;
    if (data.replace(/^\s+|\s+$/g, '') === '') return;
    var _this = this;
    this.request('POST', 'api/pastes', data, function (res) {
      _this.showDocument(data, res.id, null);
    }, function (res) {
      _this.message((res && res.error) || 'Something went wrong!', 'error');
    });
  };

  App.prototype.loadDocument = function (raw) {
    var parts = raw.split('.', 2);
    var key = parts[0];
    var lang = languageForExt(parts[1]);
    var _this = this;
    this.request('GET', 'api/pastes/' + key, null, function (res) {
      _this.showDocument(res.content, key, lang, false);
    }, function () {
      _this.newDocument();
    });
  };

  App.prototype.duplicate = function () {
    if (!this.locked) return;
    var data = this.data;
    this.newDocument();
    this.editor.value = data;
    this.updateCounts();
  };

  App.prototype.raw = function () {
    if (this.key) window.location.href = this.baseUrl + 'api/pastes/' + this.key + '/raw';
  };

  App.prototype.copyLink = function () {
    if (!this.key) return;
    var url = window.location.href;
    var _this = this;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(function () { _this.message('Link copied to clipboard', 'info'); },
        function () { _this.message('Copy failed - ' + url, 'error'); });
    } else {
      this.message(url, 'info');
    }
  };

  App.prototype.message = function (text, cls) {
    var li = document.createElement('li');
    li.className = cls || 'info';
    li.textContent = text;
    var box = document.getElementById('messages');
    box.insertBefore(li, box.firstChild);
    setTimeout(function () { li.parentNode && li.parentNode.removeChild(li); }, 3000);
  };

  // ---- wiring ----
  App.prototype.bindButtons = function () {
    var _this = this;
    var map = {
      save: function () { _this.save(); },
      new: function () { _this.newDocument(); },
      duplicate: function () { _this.duplicate(); },
      raw: function () { _this.raw(); },
      copy: function () { _this.copyLink(); }
    };
    var btns = document.querySelectorAll('.cmd-actions .btn');
    Array.prototype.forEach.call(btns, function (btn) {
      btn.addEventListener('click', function (e) {
        e.preventDefault();
        if (btn.classList.contains('disabled')) return;
        map[btn.getAttribute('data-act')]();
      });
    });
  };

  App.prototype.bindShortcuts = function () {
    var _this = this;
    document.addEventListener('keydown', function (e) {
      var code = e.keyCode;
      if (e.ctrlKey && !e.shiftKey && code === 83) { e.preventDefault(); _this.save(); }          // Ctrl+S
      else if (e.ctrlKey && code === 78) { e.preventDefault(); _this.newDocument(); }              // Ctrl+N
      else if (_this.locked && e.ctrlKey && code === 68) { e.preventDefault(); _this.duplicate(); } // Ctrl+D
      else if (e.ctrlKey && e.shiftKey && code === 82) { e.preventDefault(); _this.raw(); }         // Ctrl+Shift+R
      else if (e.ctrlKey && e.shiftKey && code === 67 && _this.locked) { e.preventDefault(); _this.copyLink(); } // Ctrl+Shift+C
    });
  };

  App.prototype.bindEditor = function () {
    var _this = this;
    this.editor.addEventListener('input', function () { _this.updateCounts(); });
    // Tab inserts two spaces.
    this.editor.addEventListener('keydown', function (e) {
      if (e.keyCode !== 9) return;
      e.preventDefault();
      var s = this.selectionStart, en = this.selectionEnd, top = this.scrollTop;
      this.value = this.value.substring(0, s) + '  ' + this.value.substring(en);
      this.selectionStart = this.selectionEnd = s + 2;
      this.scrollTop = top;
    });
  };

  App.prototype.initTheme = function () {
    // The server injects the theme config as data-* attributes on <html>:
    // the available list, the default for new visitors, and an optional forced
    // theme that locks the switcher. First paint already uses data-theme.
    var root = document.documentElement;
    var btn = document.getElementById('themeBtn');
    var THEMES = (root.getAttribute('data-themes') || 'rake').split(',');
    var deflt = root.getAttribute('data-default-theme') || 'rake';
    var forced = root.getAttribute('data-forced-theme') || '';

    if (forced) {
      // Operator-locked theme: apply it, ignore any stored choice, hide the switch.
      root.setAttribute('data-theme', forced);
      var seg = btn && btn.closest ? btn.closest('.seg') : null;
      if (seg) seg.style.display = 'none'; else if (btn) btn.style.display = 'none';
      return;
    }

    var saved = null;
    try { saved = window.localStorage.getItem('gopaste-theme'); } catch (e) { /* ignore */ }
    var cur = THEMES.indexOf(saved) >= 0 ? saved : deflt;
    function apply(name) {
      root.setAttribute('data-theme', name);
      btn.textContent = 'theme: ' + name;
      try { window.localStorage.setItem('gopaste-theme', name); } catch (e) { /* ignore */ }
    }
    apply(cur);
    btn.addEventListener('click', function () {
      var i = THEMES.indexOf(cur);
      cur = THEMES[(i + 1) % THEMES.length];
      apply(cur);
    });
  };

  // ---- boot ----
  function currentKey() {
    var path = window.location.pathname.replace(/^\//, '');
    return path; // "" for root, else "key" or "key.ext"
  }

  document.addEventListener('DOMContentLoaded', function () {
    var app = new App();
    window.gopaste = app;

    window.addEventListener('popstate', function () {
      var k = currentKey();
      if (!k) app.newDocument(true); else app.loadDocument(k);
    });

    var k = currentKey();
    if (!k) app.newDocument(true); else app.loadDocument(k);
  });
})();
