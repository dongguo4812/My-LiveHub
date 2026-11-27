/**
 * Trusted built-in QuickJS lyric actions for the five supported providers.
 * These are script actions and use only the constrained lx.request host.
 */
(() => {
  const sources = ['tx', 'kg', 'kw', 'wy', 'mg'];
  const defaults = {
    tx: 'https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg',
    kgSearch: 'https://lyrics.kugou.com/search',
    kgDownload: 'https://lyrics.kugou.com/download',
    kw: 'https://m.kuwo.cn/newh5/singles/songinfoandlrc',
    wy: 'https://music.163.com/api/song/lyric',
    mg: 'https://music.migu.cn/v3/api/music/audioPlayer/getLyric'
  };
  const str = value => value == null ? '' : String(value).trim();
  const pick = (obj, ...keys) => {
    for (const key of keys) {
      const value = obj && obj[key];
      if (value !== undefined && value !== null && str(value) !== '') return value;
    }
    return '';
  };
  const encode = value => encodeURIComponent(str(value));
  const query = params => Object.keys(params).map(key => encode(key) + '=' + encode(params[key])).join('&');
  const append = (url, params) => url + (url.includes('?') ? '&' : '?') + query(params);
  function request(url, options) {
    return new Promise((resolve, reject) => {
      let settled = false;
      const cancel = lx.request(url, options || {}, (error, response, body) => {
        if (settled) return;
        settled = true;
        if (error) { reject(error); return; }
        if (!response || response.statusCode < 200 || response.statusCode >= 300) {
          reject(new Error('HTTP ' + (response ? response.statusCode : 'no response')));
          return;
        }
        resolve(body);
      });
      if (!cancel) reject(new Error('lx.request did not return a cancellation handle'));
    });
  }
  async function json(url, headers) {
    const body = await request(url, { method: 'get', headers: headers || {}, timeout: 12000 });
    if (typeof body === 'string') {
      try { return JSON.parse(body); } catch (_) { throw new Error('provider returned invalid JSON'); }
    }
    if (!body || typeof body !== 'object') throw new Error('provider returned an empty response');
    return body;
  }
  function lrcFromKuwo(items) {
    if (!Array.isArray(items)) return '';
    return items.map(item => {
      const time = str(item.time || item.begin);
      const lyric = str(item.lineLyric || item.lyric);
      if (!time || !lyric) return '';
      const normalized = time.includes(':') ? time : '00:' + time;
      return '[' + normalized + ']' + lyric;
    }).filter(Boolean).join('\n');
  }
  function decodeBase64(value) {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
    const input = str(value).replace(/[^A-Za-z0-9+/=]/g, '');
    const bytes = [];
    let buffer = 0, bits = 0;
    for (const char of input) {
      if (char === '=') break;
      const index = chars.indexOf(char);
      if (index < 0) continue;
      buffer = (buffer << 6) | index;
      bits += 6;
      if (bits >= 8) { bits -= 8; bytes.push((buffer >> bits) & 255); }
    }
    return new TextDecoder('utf-8').decode(new Uint8Array(bytes));
  }
  function normalizeLyric(body) {
    if (typeof body === 'string') return { lyric: body.trim(), tlyric: '' };
    if (!body || typeof body !== 'object') return { lyric: '', tlyric: '' };
    const data = body.data && typeof body.data === 'object' ? body.data : body;
    const lyric = str(pick(data, 'lyric', 'lrc', 'content', 'text'));
    const translated = str(pick(data, 'tlyric', 'trans', 'translation'));
    return { lyric, tlyric: translated };
  }
  function nativeInfo(info, ...keys) {
    const sourceData = info && (info.source_data || info.sourceData) || {};
    return pick(info, ...keys) || pick(sourceData, ...keys);
  }
  async function lyricKuwo(info, endpoints) {
    const id = str(nativeInfo(info, 'songmid', 'song_id', 'id'));
    if (!id) throw new Error('Kuwo lyric requires songmid');
    const body = await json(append(endpoints.kw || defaults.kw, { musicId: id }), {
      'User-Agent': 'Mozilla/5.0',
      Referer: 'https://www.kuwo.cn/'
    });
    const data = body.data || body;
    return { lyric: lrcFromKuwo(data.lrclist || data.lrcList || data.list), tlyric: '' };
  }
  async function lyricKugou(info, endpoints) {
    const hash = str(nativeInfo(info, 'hash', 'FileHash', 'filehash'));
    if (!hash) throw new Error('Kugou lyric requires native song hash');
    const title = str(pick(info, 'title', 'name'));
    const artist = str(pick(info, 'artist', 'singer'));
    const duration = Math.max(0, Math.floor(Number(pick(info, 'duration_ms', 'duration')) / 1000));
    const search = await json(append(endpoints.kgSearch || defaults.kgSearch, {
      ver: '1', man: 'yes', client: 'pc', keyword: title + ' - ' + artist,
      duration, hash
    }), { 'User-Agent': 'Mozilla/5.0' });
    const candidates = search.candidates || search.data || [];
    if (!Array.isArray(candidates) || !candidates.length) return { lyric: '', tlyric: '' };
    const candidate = candidates[0];
    const downloaded = await json(append(endpoints.kgDownload || defaults.kgDownload, {
      ver: '1', client: 'pc', id: pick(candidate, 'id'),
      accesskey: pick(candidate, 'accesskey'), fmt: 'lrc', charset: 'utf8'
    }), { 'User-Agent': 'Mozilla/5.0' });
    return { lyric: decodeBase64(downloaded.content), tlyric: '' };
  }
  async function lyricQQ(info, endpoints) {
    const mid = str(nativeInfo(info, 'songmid', 'song_mid'));
    const id = str(nativeInfo(info, 'song_id', 'songid', 'id'));
    if (!mid && !id) throw new Error('QQ lyric requires songmid/song_id');
    const body = await json(append(endpoints.tx || defaults.tx, {
      songmid: mid, songid: id, format: 'json', nobase64: '1',
      g_tk: '5381', inCharset: 'utf8', outCharset: 'utf-8', platform: 'yqq'
    }), { 'User-Agent': 'Mozilla/5.0', Referer: 'https://y.qq.com/' });
    return normalizeLyric(body);
  }
  async function lyricNetease(info, endpoints) {
    const id = str(nativeInfo(info, 'songmid', 'song_id', 'id')).replace(/^wy_/, '');
    if (!id) throw new Error('Netease lyric requires song id');
    const body = await json(append(endpoints.wy || defaults.wy, { id, lv: '-1', tv: '-1' }), {
      'User-Agent': 'Mozilla/5.0', Referer: 'https://music.163.com/'
    });
    const lrc = body.lrc || {};
    const translated = body.tlyric || {};
    return { lyric: str(lrc.lyric), tlyric: str(translated.lyric) };
  }
  async function lyricMigu(info, endpoints) {
    const sourceData = info.source_data || info.sourceData || {};
    const direct = str(pick(sourceData, 'lrc_url', 'lyric_url', 'lyricUrl'));
    if (direct) {
      const body = await request(direct, { method: 'get', headers: { 'User-Agent': 'Mozilla/5.0' }, timeout: 12000 });
      return normalizeLyric(body);
    }
    const copyright = str(pick(sourceData, 'copyright_id', 'content_id', 'songmid') || pick(info, 'copyright_id', 'id'));
    if (!copyright) throw new Error('Migu lyric requires copyright id');
    const body = await json(append(endpoints.mg || defaults.mg, { copyrightId: copyright }), {
      'User-Agent': 'Mozilla/5.0', Referer: 'https://music.migu.cn/'
    });
    return normalizeLyric(body.data || body);
  }
  const handlers = { tx: lyricQQ, kg: lyricKugou, kw: lyricKuwo, wy: lyricNetease, mg: lyricMigu };
  lx.on('request', async ({ source, action, info }) => {
    if (action !== 'lyric' || !handlers[source]) throw new Error('Unsupported built-in lyric action: ' + source + '/' + action);
    const track = info && (info.musicInfo || info.track) || {};
    const endpoints = info && info.__testEndpoints || {};
    return handlers[source](track, endpoints);
  });
  const declarations = {};
  for (const source of sources) declarations[source] = { type: 'music', actions: ['lyric'], qualitys: [] };
  lx.send('inited', { sources: declarations });
})();
