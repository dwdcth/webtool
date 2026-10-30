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
    spawn: function () {
      return Promise.reject(new Error('node.spawn 尚未实现'));
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

  document.dispatchEvent(new CustomEvent('webtool:bridge-ready'));
})();
