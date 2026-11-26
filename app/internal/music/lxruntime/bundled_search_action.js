/**
 * My-LiveHub built-in QuickJS search actions.
 * The five LX provider search implementations live here and use only lx.request.
 */
(() => {
  // QuickJS exposes Uint8Array but not Node's Buffer global. The native
  // crypto bridge returns Buffer.from(...) values, so install the small
  // compatible byte wrapper required by the existing LX crypto contract.
  if (typeof globalThis.Buffer === 'undefined') {
    globalThis.Buffer = class Buffer extends Uint8Array {
      static from(value, encoding) {
        if (typeof value === 'string') {
          if (encoding === 'hex') {
            const out = [];
            for (let i = 0; i + 1 < value.length; i += 2) {
              const byte = parseInt(value.slice(i, i + 2), 16);
              if (!Number.isFinite(byte)) break;
              out.push(byte);
            }
            return new Buffer(out);
          }
          return new Buffer(new TextEncoder().encode(value));
        }
        if (value instanceof ArrayBuffer) return new Buffer(new Uint8Array(value));
        if (ArrayBuffer.isView(value)) return new Buffer(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
        if (Array.isArray(value)) return new Buffer(value);
        throw new TypeError('Buffer.from expects a string or byte array');
      }
      toString(encoding) {
        if (encoding === 'hex') return Array.from(this, byte => byte.toString(16).padStart(2, '0')).join('');
        return new TextDecoder().decode(this);
      }
    };
  }
  const sources = ['kw', 'kg', 'tx', 'wy', 'mg'];
  const defaults = {
    kw: 'http://search.kuwo.cn/r.s',
    kg: 'http://songsearch.kugou.com/song_search_v2',
    kgV3: 'http://mobilecdn.kugou.com/api/v3/search/song',
    tx: 'https://u.y.qq.com/cgi-bin/musicu.fcg',
    txLegacy: 'https://c.y.qq.com/soso/fcgi-bin/client_search_cp',
    wy: 'http://interface.music.163.com/eapi/batch',
    mg: 'https://jadeite.migu.cn/music_search/v3/search/searchAll',
    mgFallback: 'https://c.musicapp.migu.cn/v1.0/content/search_all.do'
  };

  function value(obj, ...keys) {
    for (const key of keys) {
      const v = obj && obj[key];
      if (v !== undefined && v !== null && String(v).trim() !== '') return v;
    }
    return '';
  }
  function str(v) { return v == null ? '' : String(v); }
  function num(v) { const n = Number(v); return Number.isFinite(n) ? n : 0; }
  function qs(params) {
    return Object.keys(params).map(k => encodeURIComponent(k) + '=' + encodeURIComponent(str(params[k]))).join('&');
  }
  function appendQuery(raw, params) {
    return raw + (raw.includes('?') ? '&' : '?') + qs(params);
  }
  function decodeName(v) {
    return str(v).replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&#39;/g, "'");
  }
  function size(bytes) {
    const n = num(bytes);
    if (!n) return '0 B';
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    const e = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
    return (n / Math.pow(1024, e)).toFixed(2) + ' ' + units[e];
  }
  function quality(type, bytes, sourceData) {
    const q = { type };
    if (bytes !== undefined && bytes !== null && num(bytes) > 0) q.size = size(bytes);
    if (sourceData && Object.keys(sourceData).length) q.source_data = sourceData;
    return q;
  }
  function track(source, nativeID, title, artist, album, duration, cover, sourceData, qualities) {
    if (!nativeID || !title) return null;
    const id = source === 'kg'
      ? str(nativeID) + '_' + str(sourceData && sourceData.hash || 'undefined')
      : source + '_' + str(nativeID);
    return {
      id, source_id: source, lx_source_key: source,
      title: decodeName(title), artist: decodeName(artist), album: decodeName(album),
      duration_ms: Math.max(0, num(duration)), cover_url: cover || '',
      source_data: sourceData || {}, qualities: qualities || []
    };
  }
  function page(source, pageNo, total, tracks, limit) {
    const count = Math.max(0, num(total));
    return {
      tracks: tracks.filter(Boolean), total: count, page: pageNo, page_size: limit,
      max_page: count ? Math.ceil(count / limit) : 1, source
    };
  }
  function http(url, options) {
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
      // Dispatch cancellation is propagated by the host; keep the cancel
      // handle reachable to prevent accidental loss of the native abort path.
      if (!cancel) reject(new Error('lx.request did not return a cancellation handle'));
    });
  }
  async function getJSON(url, headers) {
    const body = await http(url, { method: 'get', headers: headers || {}, timeout: 15000 });
    if (!body || typeof body !== 'object') throw new Error('platform returned a non-JSON response');
    return body;
  }
  async function postJSON(url, body, headers) {
    const result = await http(url, { method: 'post', headers: headers || {}, body, timeout: 15000 });
    if (!result || typeof result !== 'object') throw new Error('platform returned a non-JSON response');
    return result;
  }

  function kuwoCover(raw) {
    let v = str(raw).trim();
    if (!v) return '';
    if (v.startsWith('//')) v = 'https:' + v;
    if (/^https?:\/\//i.test(v)) {
      try { v = new URL(v).pathname; } catch (_) { return ''; }
    }
    const parts = v.replace(/^\/+|\/+$/g, '').split('/');
    const index = parts.findIndex(p => p.toLowerCase() === 'albumcover');
    const tail = index >= 0 ? parts.slice(index + 1) : parts.slice(1);
    return tail.length ? 'https://img3.kuwo.cn/star/albumcover/300/' + tail.join('/') : '';
  }
  async function searchKuwo(keyword, pageNo, limit, endpoints) {
    const endpoint = endpoints.kw || defaults.kw;
    const url = appendQuery(endpoint, {
      client: 'kt', all: keyword, pn: pageNo - 1, rn: limit, uid: '794762570',
      ver: 'kwplayer_ar_9.2.2.1', vipver: '1', show_copyright_off: '1',
      newver: '1', ft: 'music', cluster: '0', strategy: '2012', encoding: 'utf8',
      rformat: 'json', vermerge: '1', mobi: '1', issubtitle: '1'
    });
    const raw = await getJSON(url);
    const total = num(raw.TOTAL);
    if (str(raw.TOTAL) !== '0' && str(raw.SHOW) === '0') throw new Error('酷我搜索未返回有效结果');
    const tracks = [];
    for (const item of raw.abslist || []) {
      if (!str(item.N_MINFO).trim()) throw new Error('酷我搜索结果缺少音质元数据');
      const id = str(item.MUSICRID).replace(/^MUSIC_/, '');
      const title = str(item.SONGNAME);
      if (!id || !title) continue;
      const qualities = [];
      for (const token of str(item.N_MINFO).split(';')) {
        const match = /level:([^,]+),bitrate:([^,]+),format:([^,]+),size:([^,]+)/.exec(token);
        if (!match) continue;
        const kind = match[2] === '4000' || match[2] === '2000' ? 'flac' : match[2] === '320' ? '320k' : match[2] === '128' ? '128k' : '';
        if (kind && !qualities.some(q => q.type === kind)) qualities.push({ type: kind, size: match[4] });
      }
      qualities.reverse();
      const cover = kuwoCover(value(item, 'web_albumpic_short', 'ALBUMPIC', 'albumpic', 'albumPic', 'pic', 'cover'));
      const data = { songmid: id, album_id: decodeName(str(item.ALBUMID)) };
      if (cover) data.cover_url = cover;
      tracks.push(track('kw', id, title, decodeName(str(item.ARTIST)).replace(/&/g, '、'),
        item.ALBUM, num(item.DURATION) * 1000, cover, data, qualities));
    }
    return page('kw', pageNo, total, tracks, limit);
  }

  function kugouCover(raw) {
    let v = str(raw).trim().replace(/\{size\}|\{SIZE\}/g, '400');
    if (v.startsWith('//')) return 'https:' + v;
    return /^https?:\/\//i.test(v) ? v : '';
  }
  function cleanHighlight(v) {
    return decodeName(str(v).replace(/<\/?(?:em|strong)>/gi, ''));
  }
  function kugouV3Tracks(items) {
    const tracks = [], seen = Object.create(null);
    for (const item of items || []) {
      const id = str(value(item, 'audio_id', 'Audioid', 'ID', 'MixSongID', 'hash', 'FileHash'));
      const hash = str(value(item, 'hash', 'FileHash', 'Hash'));
      const key = id + ':' + hash;
      if (!id || seen[key]) continue;
      seen[key] = true;
      const title = cleanHighlight(value(item, 'songname', 'songname_original', 'SongName', 'OriSongName', 'filename', 'FileName'));
      const artist = cleanHighlight(value(item, 'singername', 'SingerName'));
      const album = cleanHighlight(value(item, 'album_name', 'AlbumName', 'Album'));
      const qualities = [];
      for (const spec of [
        ['hash', 'filesize', '128k'], ['320hash', '320filesize', '320k'],
        ['sqhash', 'sqfilesize', 'flac'], ['reshash', 'resfilesize', 'hires']
      ]) {
        const bytes = num(value(item, spec[1], spec[1].toUpperCase()));
        const fileHash = str(value(item, spec[0], spec[0].toUpperCase()));
        if (bytes > 0 && !qualities.some(q => q.type === spec[2])) {
          qualities.push(quality(spec[2], bytes, { hash: fileHash }));
        }
      }
      const cover = kugouCover(value(item, 'img', 'image', 'album_img', 'album_cover', 'Image', 'Img'));
      const sourceData = {
        songmid: id, hash,
        album_id: str(value(item, 'album_id', 'AlbumID')),
        album_audio_id: str(value(item, 'album_audio_id', 'MixSongID'))
      };
      if (cover) sourceData.cover_url = cover;
      tracks.push(track('kg', id, title, artist, album,
        num(value(item, 'duration', 'Duration')) * 1000, cover, sourceData, qualities));
    }
    return tracks;
  }
  async function searchKugou(keyword, pageNo, limit, endpoints) {
    const headers = {
      'User-Agent': 'Mozilla/5.0', Referer: 'https://www.kugou.com/',
      Accept: 'application/json, text/plain, */*'
    };
    const url = appendQuery(endpoints.kg || defaults.kg, {
      platform: 'WebFilter', iscorrection: '1', keyword, hifiquality: '0',
      pagesize: limit, privilege_filter: '0', filter: '2', tag: 'em',
      userid: '-1', clientver: '', page: pageNo
    });
    const raw = await getJSON(url, headers);
    if (num(raw.error_code) !== 0) throw new Error('酷狗搜索失败，错误码：' + raw.error_code);
    const data = raw.data || {}, seen = Object.create(null), tracks = [];
    for (const item of data.lists || []) {
      const id = str(value(item, 'Audioid', 'ID', 'MixSongID'));
      const hash = str(value(item, 'FileHash', 'Hash'));
      const key = id + ':' + hash;
      if (!id || seen[key]) continue;
      seen[key] = true;
      let title = cleanHighlight(value(item, 'OriSongName', 'SongName', 'FileName'));
      const suffix = cleanHighlight(item.Suffix);
      if (suffix) title = (title + ' ' + suffix).trim();
      const artists = [];
      for (const singer of item.Singers || []) {
        const name = cleanHighlight(value(singer, 'name', 'SingerName'));
        if (name) artists.push(name);
      }
      if (!artists.length && item.SingerName) artists.push(cleanHighlight(item.SingerName));
      if (!artists.length) {
        const fileName = cleanHighlight(item.FileName), split = fileName.indexOf(' - ');
        if (split > 0) artists.push(fileName.slice(0, split).trim());
      }
      const qualities = [];
      for (const spec of [
        ['FileHash', 'FileSize', '128k'], ['HQFileHash', 'HQFileSize', '320k'],
        ['SQFileHash', 'SQFileSize', 'flac'], ['ResFileHash', 'ResFileSize', 'hires']
      ]) {
        const bytes = num(item[spec[1]]);
        if (bytes > 0 && !qualities.some(q => q.type === spec[2])) {
          qualities.push(quality(spec[2], bytes, { hash: str(item[spec[0]]) }));
        }
      }
      const cover = kugouCover(value(item, 'Image', 'Img', 'AlbumImg', 'AlbumImage', 'album_img', 'cover', 'Cover', 'pic'));
      const sourceData = {
        songmid: id, hash, album_id: str(value(item, 'AlbumID', 'album_id')),
        album_audio_id: str(item.MixSongID)
      };
      if (cover) sourceData.cover_url = cover;
      tracks.push(track('kg', id, title, artists.join('、'),
        value(item, 'AlbumName', 'Album'), num(item.Duration) * 1000,
        cover, sourceData, qualities));
    }
    const total = num(data.total);
    if (!tracks.length && total > (pageNo - 1) * limit) {
      const fallbackURL = appendQuery(endpoints.kgV3 || defaults.kgV3, {
        format: 'json', keyword, page: pageNo, pagesize: limit, showtype: '1'
      });
      const fallback = await getJSON(fallbackURL, headers);
      const fallbackData = fallback.data || {};
      const fallbackTracks = kugouV3Tracks(fallbackData.info || fallbackData.lists || []);
      if (fallbackTracks.length) {
        return page('kg', pageNo, num(fallbackData.total) || total, fallbackTracks, limit);
      }
      throw new Error('酷狗第' + pageNo + '页 WebFilter 返回空结果，V3 分页接口也未返回歌曲');
    }
    return page('kg', pageNo, total, tracks, limit);
  }

  function qqTracks(items) {
    const tracks = [];
    for (const item of items || []) {
      const file = item.file || {}, media = str(file.media_mid);
      if (!media) continue;
      const qualities = [];
      for (const spec of [
        ['size_128mp3', '128k'], ['size_320mp3', '320k'],
        ['size_flac', 'flac'], ['size_hires', 'hires']
      ]) if (num(file[spec[0]]) > 0) qualities.push(quality(spec[1], num(file[spec[0]])));
      const album = item.album || {}, albumID = str(album.mid), artists = [];
      for (const singer of item.singer || []) if (singer.name) artists.push(str(singer.name));
      const id = str(value(item, 'mid', 'id'));
      const cover = albumID && albumID !== '空'
        ? 'https://y.gtimg.cn/music/photo_new/T002R500x500M000' + albumID + '.jpg'
        : (item.singer && item.singer[0] && item.singer[0].mid
          ? 'https://y.gtimg.cn/music/photo_new/T001R500x500M000' + item.singer[0].mid + '.jpg' : '');
      tracks.push(track('tx', id, item.title, artists.join('、'), album.name,
        num(item.interval) * 1000, cover, {
          songmid: id, song_id: str(item.id), media_mid: media, album_mid: albumID
        }, qualities));
    }
    return tracks;
  }
  function qqLegacyTracks(raw, pageNo, limit) {
    const song = raw.data && raw.data.song;
    if (!song || !Array.isArray(song.list)) throw new Error('QQ 旧版歌曲搜索接口未返回有效 song 数据');
    const tracks = [];
    for (const item of song.list) {
      const id = str(value(item, 'songmid', 'mid'));
      if (!id) continue;
      const file = item.file || {}, media = str(item.strMediaMid || file.media_mid);
      const albumID = str(item.albummid), artists = [];
      for (const singer of item.singer || []) {
        const name = str(value(singer, 'name', 'title'));
        if (name) artists.push(name);
      }
      const qualities = [];
      for (const spec of [
        [['size128', 'size_128mp3'], '128k'], [['size320', 'size_320mp3'], '320k'],
        [['sizeflac', 'size_flac'], 'flac'], [['sizeape'], 'ape']
      ]) {
        let bytes = 0;
        for (const key of spec[0]) bytes = num(item[key] || file[key]);
        if (bytes > 0) qualities.push(quality(spec[1], bytes));
      }
      const cover = albumID ? 'https://y.gtimg.cn/music/photo_new/T002R500x500M000' + albumID + '.jpg' : '';
      tracks.push(track('tx', id, item.songname, artists.join('、'), item.albumname,
        num(item.interval) * 1000, cover, {
          songmid: id, song_id: str(item.songid), media_mid: media, album_mid: albumID
        }, qualities));
    }
    if (!tracks.length) throw new Error('QQ 旧版歌曲搜索接口返回空列表（total=' + num(song.totalnum) + '），备用接口可能已失效');
    return page('tx', pageNo, num(song.totalnum), tracks, limit);
  }
  async function searchQQ(keyword, pageNo, limit, endpoints) {
    const payload = {
      'music.search.SearchCgiService': {
        method: 'DoSearchForQQMusicDesktop',
        module: 'music.search.SearchCgiService',
        param: { query: keyword, page: pageNo, num_per_page: limit, type: 0 }
      }
    };
    let lastStatus = '';
    for (let attempt = 0; attempt <= 5; attempt++) {
      const raw = await postJSON(endpoints.tx || defaults.tx, payload, {
        'User-Agent': 'Mozilla/5.0', 'Content-Type': 'application/json',
        Referer: 'https://y.qq.com/', Origin: 'https://y.qq.com'
      });
      const req = raw['music.search.SearchCgiService'] || raw.req_1 || {};
      if (Object.keys(req).length && num(raw.code) === 0 && num(req.code) === 0) {
        const data = req.data || {}, body = data.body || {}, song = body.song || {};
        return page('tx', pageNo, num((data.meta || {}).sum || song.totalnum),
          qqTracks(song.list || []), limit);
      }
      if (num(req.code) === 2001) {
        const legacyURL = appendQuery(endpoints.txLegacy || defaults.txLegacy, {
          ct: '24', qqmusic_ver: '1298', remoteplace: 'txt.yqq.song', t: '0',
          aggr: '1', cr: '1', lossless: '0', flag_qc: '0', p: pageNo, n: limit,
          w: keyword, g_tk: '5381', loginUin: '0', hostUin: '0', format: 'json',
          inCharset: 'utf8', outCharset: 'utf-8', notice: '0',
          platform: 'yqq.json', needNewCode: '0'
        });
        const legacy = await getJSON(legacyURL, {
          'User-Agent': 'Mozilla/5.0', Referer: 'https://y.qq.com/',
          Accept: 'application/json, text/plain, */*'
        });
        if (num(legacy.code) !== 0) throw new Error('QQ 旧版歌曲搜索返回错误码：' + legacy.code);
        return qqLegacyTracks(legacy, pageNo, limit);
      }
      lastStatus = 'top-level code=' + raw.code + ', provider code=' + req.code;
      if (attempt < 5) await new Promise(resolve => setTimeout(resolve, (attempt + 1) * 150));
    }
    throw new Error('QQ 音乐搜索响应状态异常（重试 6 次后仍失败：' + lastStatus + '）');
  }

  async function searchNetease(keyword, pageNo, limit, endpoints) {
    const path = '/api/search/song/list/page';
    const payload = {
      keyword, needCorrect: '1', channel: 'typing',
      offset: limit * (pageNo - 1), scene: 'normal', total: pageNo === 1, limit
    };
    const plain = JSON.stringify(payload);
    const digest = lx.utils.crypto.md5('nobody' + path + 'use' + plain + 'md5forencrypt');
    const text = path + '-36cd479b6b5-' + plain + '-36cd479b6b5-' + digest;
    const encrypted = lx.utils.crypto.aesEncrypt(
      Buffer.from(text), 'aes-128-ecb', Buffer.from('e82ckenh8dichen8'), new Uint8Array(0)
    ).toString('hex').toUpperCase();
    const raw = await http(endpoints.wy || defaults.wy, {
      method: 'post', headers: {
        'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/60.0.3112.90 Safari/537.36',
        Origin: 'https://music.163.com', 'Content-Type': 'application/x-www-form-urlencoded'
      }, form: { params: encrypted }, timeout: 15000
    });
    if (!raw || typeof raw !== 'object' || num(raw.code) !== 200) {
      throw new Error('网易云搜索失败，状态码：' + (raw && raw.code));
    }
    const data = raw.data || {}, tracks = [];
    for (const entry of data.resources || []) {
      const item = entry.baseInfo && entry.baseInfo.simpleSongData;
      if (!item) continue;
      const album = item.al || {}, privilege = item.privilege || {}, qualities = [];
      const maxLevel = str(privilege.maxBrLevel), maxBR = num(privilege.maxbr);
      if (maxLevel === 'hires') qualities.push(quality('hires', item.hr && item.hr.size));
      if (maxBR >= 999000) qualities.push(quality('flac', item.sq && item.sq.size));
      if (maxBR >= 320000) qualities.push(quality('320k', item.h && item.h.size));
      if (maxBR >= 128000) qualities.push(quality('128k', item.l && item.l.size));
      qualities.reverse();
      const artists = (item.ar || []).map(a => str(a.name)).filter(Boolean);
      const id = str(item.id), albumID = str(album.id);
      tracks.push(track('wy', id, item.name, artists.join('、'), album.name,
        num(item.dt), album.picUrl, { songmid: id, album_id: albumID }, qualities));
    }
    return page('wy', pageNo, num(data.totalCount), tracks, limit);
  }

  async function searchMigu(keyword, pageNo, limit, endpoints) {
    const now = String(Date.now()), device = '963B7AA0D21511ED807EE5846EC87D20';
    const signatureMD5 = '6cdc72a439cef99a3418d2a78aa28c73';
    const signature = lx.utils.crypto.md5(
      keyword + signatureMD5 + 'yyapp2d16148780a1dcc7408e06336b98cfd50' + device + now
    );
    const params = {
      isCorrect: '0', isCopyright: '1',
      searchSwitch: JSON.stringify({ song: 1, album: 0, singer: 0, tagSong: 1, mvSong: 0, bestShow: 1, songlist: 0, lyricSong: 0 }),
      pageSize: limit, text: keyword, pageNo: pageNo, sort: '0', sid: 'USS'
    };
    const headers = {
      uiVersion: 'A_music_3.6.1', deviceId: device, timestamp: now, sign: signature,
      channel: '0146921',
      'User-Agent': 'Mozilla/5.0 (Linux; U; Android 11.0.0; zh-cn; MI 11 Build/OPR1.170623.032) AppleWebKit/534.30 (KHTML, like Gecko) Version/4.0 Mobile Safari/534.30'
    };
    let primaryError;
    try {
      const raw = await getJSON(appendQuery(endpoints.mg || defaults.mg, params), headers);
      const result = parseMigu(raw, pageNo, limit);
      if (result.tracks.length) return result;
      primaryError = new Error('主搜索接口未返回歌曲');
    } catch (error) { primaryError = error; }
    const fallbackURL = appendQuery(endpoints.mgFallback || defaults.mgFallback, {
      ua: 'Android_migu', version: '5.0.1', text: keyword, pageNo: pageNo,
      pageSize: limit, searchSwitch: JSON.stringify({
        song: 1, album: 0, singer: 0, tagSong: 0, mvSong: 0, songlist: 0, bestShow: 1
      })
    });
    try {
      const fallback = await getJSON(fallbackURL, {
        'User-Agent': 'Mozilla/5.0 (Linux; Android 11; zh-cn) AppleWebKit/537.36 Chrome/120.0.0.0 Mobile Safari/537.36',
        channel: '0146921', Accept: 'application/json, text/plain, */*'
      });
      const result = parseMigu(fallback, pageNo, limit);
      if (!result.tracks.length) throw new Error('备用接口未返回歌曲');
      return result;
    } catch (error) {
      throw new Error('咪咕搜索主接口失败：' + primaryError.message + '；备用接口失败：' + error.message);
    }
  }
  function parseMigu(raw, pageNo, limit) {
    if (str(raw.code) !== '000000') throw new Error('咪咕搜索失败：' + str(raw.info));
    const data = raw.songResultData || {};
    const items = data.resultList || data.result || data.musics || [];
    const tracks = [], seen = Object.create(null);
    for (const group of items) {
      const entries = Array.isArray(group) ? group : [group];
      for (const item of entries) {
        if (!item || typeof item !== 'object') continue;
        const id = str(value(item, 'songId', 'contentId', 'id'));
        const copyright = str(value(item, 'copyrightId') || id);
        if (!id || seen[copyright]) continue;
        seen[copyright] = true;
        const qualities = [];
        for (const format of item.audioFormats || []) {
          const kind = ({ PQ: '128k', HQ: '320k', SQ: 'flac', ZQ24: 'flac' })[format.formatType] || '';
          if (kind) qualities.push(quality(kind, num(format.asize) || num(format.isize)));
        }
        if (!qualities.length && (item.mp3 || item.listenUrl || item.lisQq)) qualities.push(quality('128k'));
        let artists = (item.singerList || []).map(a => str(a.name)).filter(Boolean);
        if (!artists.length) artists = (item.singers || []).map(a => str(a.name)).filter(Boolean);
        const artist = artists.length ? artists.join('、') : str(value(item, 'artist', 'singer'));
        let album = str(value(item, 'album', 'albumName'));
        if (!album && item.albums && item.albums[0]) album = str(item.albums[0].name);
        let cover = str(value(item, 'img3', 'img2', 'img1'));
        if (!cover && item.imgItems && item.imgItems[0]) cover = str(value(item.imgItems[0], 'img', 'url'));
        if (cover && !/^https?:\/\//i.test(cover)) cover = 'https://d.musicapp.migu.cn' + cover;
        let duration = num(item.duration);
        if (duration > 0 && duration <= 10000) duration *= 1000;
        const sourceData = {
          songmid: id, content_id: str(item.contentId || id), copyright_id: copyright,
          album_id: str(value(item, 'albumId', 'albumid')),
          lrc_url: str(value(item, 'lrcUrl', 'lyricUrl')),
          mrc_url: str(item.mrcurl), trc_url: str(item.trcUrl)
        };
        tracks.push(track('mg', id, value(item, 'name', 'songName'), artist, album,
          duration, cover, sourceData, qualities));
      }
    }
    return page('mg', pageNo, num(data.totalCount || data.total), tracks, limit);
  }

  const handlers = {
    kw: searchKuwo, kg: searchKugou, tx: searchQQ, wy: searchNetease, mg: searchMigu
  };
  lx.on('request', async ({ source, action, info }) => {
    if (action !== 'search' || !handlers[source]) throw new Error('Unsupported built-in search action: ' + source + '/' + action);
    const keyword = str(value(info, 'text', 'keyword')).trim();
    const pageNo = Math.max(1, Math.floor(num(info.page) || 1));
    const limit = Math.max(1, Math.min(30, Math.floor(num(value(info, 'limit', 'pageSize')) || 30)));
    if (!keyword) throw new Error('请输入歌曲名称或歌手');
    const endpoints = info && info.__testEndpoints && typeof info.__testEndpoints === 'object'
      ? info.__testEndpoints : {};
    return handlers[source](keyword, pageNo, limit, endpoints);
  });
  const declarations = {};
  for (const source of sources) declarations[source] = { type: 'music', actions: ['search'], qualitys: [] };
  lx.send('inited', { sources: declarations });
})();
