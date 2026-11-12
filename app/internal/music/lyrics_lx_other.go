package music

import (
 "bytes"
 "compress/zlib"
 "context"
 "crypto/aes"
 "crypto/md5"
 "encoding/base64"
 "encoding/binary"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "regexp"
 "strconv"
 "strings"
 "time"
 "unicode/utf16"
)

func doLyricRaw(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body io.Reader) ([]byte,error) {
 if ctx==nil { ctx=context.Background() }; if client==nil { client=&http.Client{Timeout:18*time.Second} }
 req,err:=http.NewRequestWithContext(ctx,method,rawURL,body); if err!=nil{return nil,err}
 req.Header.Set("User-Agent","lx-music request")
 for k,v:=range headers { req.Header.Set(k,v) }
 resp,err:=client.Do(req); if err!=nil{return nil,err}; defer resp.Body.Close()
 data,err:=io.ReadAll(io.LimitReader(resp.Body,8<<20)); if err!=nil{return nil,err}
 if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("音乐平台返回 HTTP %d",resp.StatusCode)}
 return data,nil
}
func lyricJSON(ctx context.Context,client *http.Client,method,endpoint string,headers map[string]string,body io.Reader)(map[string]any,error){
 raw,err:=doLyricRaw(ctx,client,method,endpoint,headers,body);if err!=nil{return nil,err}
 var v map[string]any;if err=json.Unmarshal(raw,&v);err!=nil{return nil,err};return v,nil
}
func lyricStr(v any)string{if s,ok:=v.(string);ok{return s};return ""}
func lyricMap(v any)map[string]any{m,_:=v.(map[string]any);return m}
func lyricInt(v any)int64{switch n:=v.(type){case float64:return int64(n);case json.Number:i,_:=n.Int64();return i;case string:i,_:=strconv.ParseInt(n,10,64);return i};return 0}

// Kuwo: LX's mobi.s endpoint returns TP=content, deflate-compressed data;
// the payload is Base64 text XORed with the repeating "yeelion" key.
func GetKuwoMusicLyrics(ctx context.Context,client *http.Client,track Track)(string,error){
 id:=strings.TrimSpace(track.SourceData["songmid"]);if id==""{return "",errors.New("酷我歌词请求缺少 songmid")}
 endpoint:="https://mlyric.kuwo.cn/mobi.s?f=web&type=lyric&lrcx=1&rid="+url.QueryEscape(id)+"&encode=utf8"
 raw,err:=doLyricRaw(ctx,client,http.MethodGet,endpoint,nil,nil);if err!=nil{return "",err}
 if len(raw)<10||!strings.EqualFold(string(raw[:10]),"tp=content"){return "",errors.New("酷我歌词响应格式无效")}
 sep:=bytes.Index(raw,[]byte("\r\n\r\n"));if sep<0{return "",errors.New("酷我歌词响应缺少内容分隔符")}
 zr,err:=zlib.NewReader(bytes.NewReader(raw[sep+4:]));if err!=nil{return "",err}
 decoded,readErr:=io.ReadAll(io.LimitReader(zr,4<<20));closeErr:=zr.Close();if readErr!=nil{return "",readErr};if closeErr!=nil{return "",closeErr}
 payload,err:=base64.StdEncoding.DecodeString(strings.TrimSpace(string(decoded)));if err!=nil{return "",err}
 key:=[]byte("yeelion");for i:=range payload{payload[i]^=key[i%len(key)]}
 return parseLXPlainLyric(string(payload)),nil
}
func parseLXPlainLyric(value string)string{
 var out strings.Builder
 for _,line:=range strings.Split(strings.ReplaceAll(value,"\r",""),"\n"){
  line=strings.TrimSpace(line);if line==""{continue}
  if strings.HasPrefix(line,"[offset"){out.WriteString(line+"\n");continue}
  if !strings.HasPrefix(line,"["){continue};end:=strings.IndexByte(line,']');if end<0{continue}
  text:=regexp.MustCompile("<-?\\d+,-?\\d+(?:,-?\\d+)?>").ReplaceAllString(line[end+1:],"")
  out.WriteString(line[:end+1]+text+"\n")
 }
 return strings.TrimSpace(out.String())
}

// NetEase: LX uses the EAPI AES-128-ECB signature and requests YRC/LRC,
// translation and romanization in one response.
func neteaseLyricSongID(track Track) string {
 for _, key := range []string{"songmid", "songId", "song_id", "id"} {
  if value := strings.TrimSpace(track.SourceData[key]); value != "" {
   return value
  }
 }
 // LX playlist IDs use the provider prefix (for example wy_1500569811).
 // Keep this fallback at the provider adapter boundary as well as import time:
 // it protects lyrics when metadata was dropped by an intermediate snapshot.
 id := strings.TrimSpace(track.ID)
 if len(id) > 3 && strings.EqualFold(id[:3], "wy_") {
  return strings.TrimSpace(id[3:])
 }
 return ""
}

func GetNeteaseMusicLyrics(ctx context.Context,client *http.Client,track Track)(string,error){
 id:=neteaseLyricSongID(track);if id==""{return "",errors.New("网易云歌词请求缺少 songmid")}
 path:="/api/song/lyric/v1";payload:=map[string]any{"id":id,"cp":false,"tv":0,"lv":0,"rv":0,"kv":0,"yv":0,"ytv":0,"yrv":0}
 plain,err:=json.Marshal(payload);if err!=nil{return "",err}
 sum:=md5.Sum([]byte("nobody"+path+"use"+string(plain)+"md5forencrypt"))
 text:=path+"-36cd479b6b5-"+string(plain)+"-36cd479b6b5-"+hex.EncodeToString(sum[:])
 block,err:=aes.NewCipher([]byte("e82ckenh8dichen8"));if err!=nil{return "",err}
 pad:=aes.BlockSize-len(text)%aes.BlockSize;input:=append([]byte(text),bytes.Repeat([]byte{byte(pad)},pad)...)
 encrypted:=make([]byte,len(input));for i:=0;i<len(input);i+=aes.BlockSize{block.Encrypt(encrypted[i:i+aes.BlockSize],input[i:i+aes.BlockSize])}
 form:=url.Values{"params":{strings.ToUpper(hex.EncodeToString(encrypted))}}
 result,err:=lyricJSON(ctx,client,http.MethodPost,"https://interface3.music.163.com/eapi/song/lyric/v1",map[string]string{"Origin":"https://music.163.com","Content-Type":"application/x-www-form-urlencoded"},strings.NewReader(form.Encode()))
 if err!=nil{return "",err};if lyricInt(result["code"])!=200{return "",fmt.Errorf("网易云歌词接口状态异常：%v",result["code"])}
 lrc:=lyricStr(lyricMap(result["lrc"])["lyric"]);if strings.TrimSpace(lrc)==""{return "",nil}
 // Preserve the independent synchronized streams returned by LX's EAPI.
 var out strings.Builder;out.WriteString(lrc)
 for _,key:=range []string{"tlyric"}{v:=lyricStr(lyricMap(result[key])["lyric"]);if v!=""{out.WriteString("\n");out.WriteString(v)}}
 return strings.TrimSpace(out.String()),nil
}

// Migu MRC is a 64-bit TEA stream; all arithmetic intentionally wraps uint64.
var miguLyricKey=[]uint64{27303562373562475,18014862372307051,22799692160172081,34058940340699235,30962724186095721,27303523720101991,27303523720101998,31244139033526382,28992395054481524}
func miguMix(z,y,sum uint64,p,e uint64)uint64{return (((z>>5)^(y<<2))+((y>>3)^(z<<4)))^((sum^y)+(miguLyricKey[(p&3)^e]^z))}
func decryptMiguLyric(value string)(string,error){
 if len(value)<32{return value,nil};if len(value)%16!=0{return "",errors.New("咪咕 MRC 长度无效")}
 n:=len(value)/16;data:=make([]uint64,n)
 for i:=range data{v,e:=strconv.ParseUint(value[i*16:(i+1)*16],16,64);if e!=nil{return "",e};data[i]=v}
 sum:=uint64(6+52/n)*2654435769;z:=data[0]
 for sum!=0{e:=(sum>>2)&3;for p:=n-1;p>0;p--{y:=data[p-1];z=data[p]-miguMix(z,y,sum,uint64(p),e);data[p]=z};y:=data[n-1];z=data[0]-miguMix(z,y,sum,0,e);data[0]=z;sum-=2654435769}
 units:=make([]uint16,0,n*4)
 for _,v:=range data{var b [8]byte;binary.LittleEndian.PutUint64(b[:],v);for j:=0;j<8;j+=2{u:=binary.LittleEndian.Uint16(b[j:j+2]);if u==0{return string(utf16.Decode(units)),nil};units=append(units,u)}}
 return string(utf16.Decode(units)),nil
}
func parseMiguLyric(value string)string{
 var out strings.Builder
 re:=regexp.MustCompile("^\\s*\\[(\\d+),\\d+\\]")
 words:=regexp.MustCompile("\\(\\d+,\\d+\\)")
 for _,line:=range strings.Split(strings.ReplaceAll(value,"\r",""),"\n"){m:=re.FindStringSubmatch(line);if len(m)!=2{continue};ms,e:=strconv.ParseInt(m[1],10,64);if e!=nil{continue};fmt.Fprintf(&out,"[%02d:%02d.%03d]%s\n",ms/60000,(ms/1000)%60,ms%1000,words.ReplaceAllString(re.ReplaceAllString(line,""),""))}
 return strings.TrimSpace(out.String())
}
func GetMiguMusicLyrics(ctx context.Context,client *http.Client,track Track)(string,error){
 mrc:=strings.TrimSpace(track.SourceData["mrc_url"]);lrc:=strings.TrimSpace(track.SourceData["lrc_url"]);trc:=strings.TrimSpace(track.SourceData["trc_url"])
 if mrc==""&&lrc==""{
  id:=strings.TrimSpace(track.SourceData["copyright_id"]);if id==""{id=track.SourceData["songmid"]};if id==""{return "",errors.New("咪咕歌词缺少歌曲 ID")}
  form:=url.Values{"resourceId":{id}}
  info,err:=lyricJSON(ctx,client,http.MethodPost,"https://c.musicapp.migu.cn/MIGUM2.0/v1.0/content/resourceinfo.do?resourceType=2",map[string]string{"Content-Type":"application/x-www-form-urlencoded","Referer":"https://app.c.nf.migu.cn/","channel":"0146921"},strings.NewReader(form.Encode()))
  if err!=nil{return "",err};items,_:=info["resource"].([]any);if len(items)>0{item:=lyricMap(items[0]);mrc=lyricStr(item["mrcUrl"]);lrc=lyricStr(item["lrcUrl"]);trc=lyricStr(item["trcUrl"])}
 }
 headers:=map[string]string{"Referer":"https://app.c.nf.migu.cn/","channel":"0146921","User-Agent":"Mozilla/5.0 (Linux; Android 5.1.1; Nexus 6) AppleWebKit/537.36 Chrome/59.0.3071.115 Mobile Safari/537.36"}
 var primary string
 if mrc!=""{raw,e:=doLyricRaw(ctx,client,http.MethodGet,mrc,headers,nil);if e!=nil{return "",e};plain,e:=decryptMiguLyric(strings.TrimSpace(string(raw)));if e!=nil{return "",e};primary=parseMiguLyric(plain)
 }else if lrc!=""{raw,e:=doLyricRaw(ctx,client,http.MethodGet,lrc,headers,nil);if e!=nil{return "",e};primary=strings.TrimSpace(string(raw))}
 if trc!=""{raw,e:=doLyricRaw(ctx,client,http.MethodGet,trc,headers,nil);if e==nil&&len(raw)>0{primary+="\n"+strings.TrimSpace(string(raw))}}
 return strings.TrimSpace(primary),nil
}
