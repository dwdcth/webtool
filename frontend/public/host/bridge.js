// 宿主桥：注入到每个插件 HTML 的 <head> 中（由 Go 资产服务自动完成）。
// 在插件内提供 window.unihub / window.node 全局对象，
// 方法签名对齐 unihub 插件生态，底层经 /p/<token>/api/* 调用 Go 能力域。
(function () {
  // 错误捕获：宿主可读（同源 iframe）或经 postMessage 上报（沙箱 iframe）
  window.__webtoolErrors = [];
  function record(kind, msg) {
    try {
      window.__webtoolErrors.push(kind + ': ' + msg);
      if (window.parent && window.parent !== window) {
        window.parent.postMessage({ __webtoolPluginError: msg }, '*');
      }
    } catch (e) { /* 忽略上报失败 */ }
  }
  window.addEventListener('error', function (e) {
    record('ERR', (e.message || 'error') + ' @' + (e.filename || '').split('/').pop() + ':' + e.lineno);
  });
  window.addEventListener('unhandledrejection', function (e) {
    var r = e.reason;
    record('REJ', (r && r.message) ? r.message : String(r));
  });
  // 导航追踪：帧被导航离开前上报自身 location（诊断白屏/自跳转问题）
  window.addEventListener('beforeunload', function () {
    try {
      if (window.parent && window.parent !== window) {
        window.parent.postMessage({ __webtoolNavAway: String(location.href) }, '*');
      }
    } catch (e) { /* 忽略 */ }
  });

  // 外站链接拦截：插件页内点击指向外站的 <a>（如 Bitwarden 区域菜单的
  // 官方 vault 链接）会让 iframe 整页导航走，被对方 X-Frame-Options 拒绝
  // 后白屏。捕获阶段拦截默认导航，交给宿主在系统浏览器打开。
  // 注：preventDefault 不影响页面自身的 click 处理（如"自托管…"菜单项）。
  document.addEventListener('click', function (e) {
    try {
      if (e.defaultPrevented || e.button !== 0) return;
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      var a = e.target && e.target.closest ? e.target.closest('a[href]') : null;
      if (!a) return;
      var href = a.getAttribute('href') || '';
      if (!href || href.charAt(0) === '#' || href.indexOf('javascript:') === 0) return;
      var abs = new URL(href, location.href);
      if (abs.origin === location.origin) return;
      if (abs.protocol !== 'http:' && abs.protocol !== 'https:') return;
      e.preventDefault();
      if (window.unihub && window.unihub.system && window.unihub.system.openExternal) {
        window.unihub.system.openExternal(abs.href).catch(function () { /* 打开失败静默 */ });
      }
    } catch (err) { /* 拦截异常不影响页面 */ }
  }, true);

  if (window.__WEBTOOL_BRIDGE__) return;
  var m = location.pathname.match(/^\/p\/([0-9a-f]{16,64})\//);
  if (!m) return; // 不在插件资源空间内则不安装

  // __wt__ 为宿主保留前缀，避免与被镜像网站自身的 /api 路径冲突
  var base = '/p/' + m[1] + '/__wt__/api/';

  function call(domain, method, args) {
    return fetch(base + domain + '/' + method, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(args || {}),
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (j) {
        if (!j || j.ok !== true) {
          var err = new Error((j && j.error) || 'bridge: 调用失败');
          err.raw = j;
          throw err;
        }
        return j.data;
      });
  }

  var unihub = {
    // ---- 存储（按插件隔离）----
    storage: {
      get: function (k) { return call('storage', 'get', { key: k }); },
      set: function (k, v) { return call('storage', 'set', { key: k, value: v }); },
      'delete': function (k) { return call('storage', 'delete', { key: k }); },
      keys: function () { return call('storage', 'keys', {}); },
      clear: function () { return call('storage', 'clear', {}); },
      // 大对象（图片/笔记文本等）存宿主侧 SQLite，无 localStorage 配额限制
      setBlob: function (k, dataUrl) { return call('storage', 'set-blob', { key: k, dataUrl: dataUrl }); },
      getBlob: function (k) { return call('storage', 'get-blob', { key: k }); },
      delBlob: function (k) { return call('storage', 'del-blob', { key: k }); },
      blobKeys: function () { return call('storage', 'blob-keys', {}); },
    },
    // ---- 剪贴板 ----
    clipboard: {
      readText: function () {
        return call('clipboard', 'read-text', {}).then(function (d) { return d.text; });
      },
      writeText: function (text) { return call('clipboard', 'write-text', { text: text }); },
      onChange: function (cb) {
        var last = null, first = true;
        var timer = setInterval(function () {
          unihub.clipboard.readText().then(function (t) {
            if (first) { last = t; first = false; return; }
            if (t !== last) {
              last = t;
              try { cb({ content: t, timestamp: Date.now() }); } catch (e) { /* 回调异常不中断轮询 */ }
            }
          }).catch(function () { /* 轮询失败忽略（可能未授权） */ });
        }, 1000);
        return function () { clearInterval(timer); };
      },
    },
    // ---- 网络请求（宿主代理，绕 CORS）----
    http: {
      get: function (url, options) {
        return call('http', 'get', { url: url, headers: options && options.headers });
      },
      post: function (url, data, options) {
        return call('http', 'post', {
          url: url,
          data: data,
          contentType: options && options.contentType,
          headers: options && options.headers,
        });
      },
      request: function (opts) { return call('http', 'request', opts || {}); },
    },
    // ---- 文件系统（需 fs 权限）----
    fs: {
      readFile: function (path) { return call('fs', 'read-file', { path: path }); },
      writeFile: function (path, content) { return call('fs', 'write-file', { path: path, content: content }); },
      readDir: function (path) { return call('fs', 'read-dir', { path: path }); },
      exists: function (path) { return call('fs', 'exists', { path: path }); },
      stat: function (path) { return call('fs', 'stat', { path: path }); },
      selectFile: function () { return call('fs', 'select-file', {}); },
      selectDirectory: function () { return call('fs', 'select-directory', {}); },
    },
    // ---- 系统 ----
    system: {
      getInfo: function () { return call('system', 'get-info', {}); },
      openExternal: function (url) { return call('system', 'open-external', { url: url }); },
      showInFolder: function (path) { return call('system', 'show-in-folder', { path: path }); },
    },
  };

  // 沙箱版 node API：路径限制在插件目录内
  var node = {
    fs: {
      readFile: function (p) { return call('node', 'read-file', { path: p }); },
      writeFile: function (p, content) { return call('node', 'write-file', { path: p, content: content }); },
    },
    getPluginDir: function () { return call('node', 'get-plugin-dir', {}); },
    // sidecar：执行插件目录内可执行文件（stdin→stdout，需声明 spawn 权限）
    spawn: function (command, args, opts) {
      return call('node', 'spawn', {
        command: command,
        args: args || [],
        input: opts && opts.input,
        timeout: opts && opts.timeout,
        env: opts && opts.env,
      });
    },
  };

  window.unihub = unihub;
  window.node = node;
  window.__WEBTOOL_BRIDGE__ = true;

  // ---- navigator.clipboard 兜底 ----
  // WebKitGTK 等环境无剪贴板授权 UI，read()/readText() 直接 NotAllowedError。
  // 原生失败时回落到宿主能力 API（文本走 Wails，图片走 wl-paste/xclip），
  // 返回值对齐 ClipboardItem 形状，插件（如 heynote 粘贴图片）无感。
  try {
    if (navigator.clipboard) {
      var nativeRead = navigator.clipboard.read ? navigator.clipboard.read.bind(navigator.clipboard) : null;
      var nativeReadText = navigator.clipboard.readText ? navigator.clipboard.readText.bind(navigator.clipboard) : null;

      function dataUrlToBlob(dataUrl) {
        var parts = dataUrl.split(',');
        var mime = (parts[0].match(/data:([^;]+)/) || [])[1] || 'application/octet-stream';
        var bin = atob(parts[1] || '');
        var arr = new Uint8Array(bin.length);
        for (var i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
        return new Blob([arr], { type: mime });
      }

      navigator.clipboard.readText = function () {
        return nativeReadText().catch(function () {
          return call('clipboard', 'read-all', {}).then(function (d) { return d.text || ''; });
        });
      };

      navigator.clipboard.read = function () {
        var p = nativeRead ? nativeRead() : Promise.reject(new Error('no native'));
        return p.catch(function () {
          return call('clipboard', 'read-all', {}).then(function (d) {
            var items = [];
            if (d.dataUrl) {
              (function (url) {
                items.push({
                  types: ['image/png'],
                  getType: function (t) {
                    return t === 'image/png'
                      ? Promise.resolve(dataUrlToBlob(url))
                      : Promise.reject(new Error('type not found'));
                  },
                });
              })(d.dataUrl);
            }
            if (d.text !== null && d.text !== undefined && d.text !== '') {
              (function (text) {
                items.push({
                  types: ['text/plain'],
                  getType: function (t) {
                    return t === 'text/plain'
                      ? Promise.resolve(new Blob([text], { type: 'text/plain' }))
                      : Promise.reject(new Error('type not found'));
                  },
                });
              })(d.text);
            }
            if (!items.length) throw new Error('clipboard empty');
            return items;
          });
        });
      };
    }
  } catch (e) { /* 补丁失败不影响插件本体 */ }

  // ---- 上游代理包装 ----
  // manifest 声明 webtool.upstreamHosts 后，宿主在页面里注入
  // __WT_UPSTREAM_HOSTS__ 白名单；本包装把命中白名单的外部请求
  // （fetch / XMLHttpRequest）改写到 /p/<token>/__wt__/upstream?url=...，
  // 由 Go 侧代理转发，绕过 CORS（Bitwarden 等需直连官方 API 的插件）。
  // 整段 try/catch：任何异常都回退原生行为，不影响插件本体。
  try {
    var upstreamHosts = window.__WT_UPSTREAM_HOSTS__;
    if (Array.isArray(upstreamHosts) && upstreamHosts.length > 0) {
      // matchUpstreamHost 与 Go 侧语义一致：*.x.com 匹配任意层级子域
      // （含 x.com 本身）；不含 * 时精确匹配；否则 false
      var matchUpstreamHost = function (pattern, host) {
        if (!pattern || !host) return false;
        pattern = String(pattern).toLowerCase();
        host = String(host).toLowerCase();
        if (pattern === '*') return true; // 显式全通配（与 Go 侧语义一致）
        if (pattern.indexOf('*.') === 0) {
          var suffix = pattern.slice(1); // '.x.com'
          return host === pattern.slice(2) || host.slice(-suffix.length) === suffix;
        }
        return pattern === host;
      };

      // 命中白名单返回代理 URL（基于与 base 相同的 token 前缀），
      // 未命中或非 http(s) 返回 null（走原生）
      var upstreamProxyUrl = function (input) {
        var abs = new URL(String(input), location.href);
        // 同源请求不走代理（本地资源：locales/assets/lazy chunks），
        // 否则 wails:// 等自定义 scheme 会被 Go 代理拒收导致白屏
        if (abs.origin === location.origin) return null;
        if (abs.protocol !== 'http:' && abs.protocol !== 'https:') return null;
        for (var i = 0; i < upstreamHosts.length; i++) {
          if (matchUpstreamHost(upstreamHosts[i], abs.hostname)) {
            return '/p/' + m[1] + '/__wt__/upstream?url=' + encodeURIComponent(abs.href);
          }
        }
        return null;
      };

      // fetch 包装：改写字符串 / URL 对象 / Request 对象三种形式的地址。
      // Request 对象（Bitwarden ApiService 是 fetch(new Request(...))，全部
      // API 调用都是该形式）不能以 new Request(代理URL, 原Request) 重建：
      // 构造成功但 Chromium/WebKit 一律发送失败（实测 TypeError），需
      // clone() 读出 body 再以 init 形式重发（Blob 保二进制安全），
      // 否则跨域请求绕过代理直连上游被 CORS 拦截（Load failed）
      var nativeFetch = window.fetch.bind(window);
      window.fetch = function (input, init) {
        try {
          var proxied = null;
          if (typeof input === 'string' || input instanceof URL) {
            proxied = upstreamProxyUrl(String(input));
            if (proxied) return nativeFetch(proxied, init);
          } else if (input instanceof Request) {
            proxied = upstreamProxyUrl(input.url);
            if (proxied) {
              var req = input;
              var hasBody = req.method !== 'GET' && req.method !== 'HEAD';
              var bodyReady = hasBody
                ? req.clone().blob()
                : Promise.resolve(undefined);
              return bodyReady.then(function (body) {
                return nativeFetch(proxied, {
                  method: req.method,
                  headers: req.headers,
                  body: body,
                  credentials: req.credentials,
                  cache: req.cache,
                  redirect: req.redirect,
                  integrity: req.integrity,
                  signal: (init && init.signal) || req.signal,
                });
              });
            }
          }
        } catch (e) { /* 解析失败按原样走原生 */ }
        return nativeFetch(input, init);
      };

      // XHR 包装：只改写 open 的 URL，send / setRequestHeader 等不动
      var nativeOpen = XMLHttpRequest.prototype.open;
      XMLHttpRequest.prototype.open = function (method, url) {
        var args = Array.prototype.slice.call(arguments);
        try {
          if (typeof url === 'string' || url instanceof URL) {
            var proxied = upstreamProxyUrl(url);
            if (proxied) args[1] = proxied;
          }
        } catch (e) { /* 解析失败按原样走原生 */ }
        return nativeOpen.apply(this, args);
      };
    }
  } catch (e) { /* 包装失败回退原生行为 */ }

  document.dispatchEvent(new CustomEvent('webtool:bridge-ready'));
})();
