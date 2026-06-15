/* gopaste - vanilla frontend (no framework, no CDN).
   Wire contract:
     POST /documents              -> { key }
     GET  /documents/:key         -> { data, key }
     GET  /raw/:key               -> text/plain
   Served at "/" and "/:key" (SPA). */

/* global hljs */
(function () {
  'use strict';

  // Map of file extension -> highlight.js language (and reverse for URLs).
  var EXT_MAP = {
    rb: 'ruby', py: 'python', pl: 'perl', php: 'php', scala: 'scala', go: 'go',
    xml: 'xml', html: 'xml', htm: 'xml', css: 'css', js: 'javascript', ts: 'typescript',
    vbs: 'vbscript', lua: 'lua', pas: 'delphi', java: 'java', cpp: 'cpp', cc: 'cpp',
    m: 'objectivec', vala: 'vala', sql: 'sql', sm: 'smalltalk', lisp: 'lisp', ini: 'ini',
    diff: 'diff', bash: 'bash', sh: 'bash', tex: 'tex', erl: 'erlang', hs: 'haskell',
    md: 'markdown', txt: '', coffee: 'coffee', swift: 'swift', json: 'json', yaml: 'yaml',
    yml: 'yaml', rs: 'rust', toml: 'ini', dockerfile: 'dockerfile'
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
    function seg(id, show, html) {
      var el = document.getElementById(id);
      el.hidden = !show;
      if (show && html != null) el.innerHTML = html;
    }
    seg('st-key', !!opts.key, 'key <b>' + (opts.key || '') + '</b>');
    seg('st-lang', !!opts.lang, '<span class="led blue"></span><b>' + (opts.lang || '') + '</b>');
    var count = document.getElementById('st-count');
    count.textContent = opts.lines + ' lines / ' + opts.chars + ' chars';
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
    this.box.style.display = '';
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
    this.request('POST', 'documents', data, function (res) {
      _this.showDocument(data, res.key, null);
    }, function (res) {
      _this.message((res && res.message) || 'Something went wrong!', 'error');
    });
  };

  App.prototype.loadDocument = function (raw) {
    var parts = raw.split('.', 2);
    var key = parts[0];
    var lang = languageForExt(parts[1]);
    var _this = this;
    this.request('GET', 'documents/' + key, null, function (res) {
      _this.showDocument(res.data, key, lang, false);
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
    if (this.key) window.location.href = this.baseUrl + 'raw/' + this.key;
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
    var THEMES = ['rake', 'arctic'];
    var btn = document.getElementById('themeBtn');
    var saved = null;
    try { saved = window.localStorage.getItem('gopaste-theme'); } catch (e) { /* ignore */ }
    var cur = THEMES.indexOf(saved) >= 0 ? saved : 'rake';
    function apply(name) {
      document.documentElement.setAttribute('data-theme', name);
      btn.textContent = 'theme: ' + name;
      try { window.localStorage.setItem('gopaste-theme', name); } catch (e) { /* ignore */ }
    }
    apply(cur);
    btn.addEventListener('click', function () {
      cur = THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length];
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
