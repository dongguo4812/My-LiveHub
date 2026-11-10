package imagecache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	routePrefix = "/__local_cache/images/"
	maxImageSize = 8 << 20
)

type URLPolicy func(*url.URL) bool
type downloadTask struct { platformID string; resourceID string; raw string }
type Manager struct {
	root string
	fallback http.Handler
	client *http.Client
	queue chan downloadTask
	mu sync.RWMutex
	inflight map[string]bool
	failedUntil map[string]time.Time
	ready map[string]string
	policies map[string]URLPolicy
	statsMu sync.RWMutex
	stats CacheStats
	statsRefreshing bool
}

func BilibiliURLPolicy(u *url.URL) bool { if u==nil||u.Scheme!="https"||u.User!=nil||u.Port()!=""{return false};h:=strings.ToLower(u.Hostname());return h=="bilibili.com"||strings.HasSuffix(h,".bilibili.com")||h=="hdslb.com"||strings.HasSuffix(h,".hdslb.com") }
func New(root string,fallback http.Handler)(*Manager,error){return NewWithPolicies(root,fallback,map[string]URLPolicy{"bilibili":BilibiliURLPolicy})}
func NewWithPolicies(root string,fallback http.Handler,policies map[string]URLPolicy)(*Manager,error){if strings.TrimSpace(root)==""{return nil,errors.New("图片缓存目录不能为空")};if err:=os.MkdirAll(root,0o755);err!=nil{return nil,fmt.Errorf("创建图片缓存目录: %w",err)};m:=&Manager{root:root,fallback:fallback,policies:make(map[string]URLPolicy),client:&http.Client{Timeout:8*time.Second},queue:make(chan downloadTask,64),inflight:make(map[string]bool),failedUntil:make(map[string]time.Time),ready:make(map[string]string)};for id,p:=range policies{if strings.TrimSpace(id)!=""&&p!=nil{m.policies[id]=p}};for i:=0;i<3;i++{go m.worker()};return m,nil}
var cacheExtensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".img"}

func imageExtension(contentType string, sniffed string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil { return "", false }
	if strings.HasPrefix(sniffed, "image/") { mediaType = sniffed } else if sniffed != "application/octet-stream" { return "", false }
	switch strings.ToLower(mediaType) {
	case "image/png": return ".png", true
	case "image/jpeg", "image/jpg": return ".jpg", true
	case "image/gif": return ".gif", true
	case "image/webp": return ".webp", true
	case "image/avif": return ".avif", true
	case "image/bmp", "image/x-ms-bmp": return ".bmp", true
	default: return "", false
	}
}

func existingCachePath(platformRoot, key, preferredExt string) (string, string) {
	try := func(ext string) (string, string) {
		path := filepath.Join(platformRoot, key+ext)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() { return path, ext }
		return "", ""
	}
	if path, ext := try(preferredExt); path != "" { return path, ext }
	for _, ext := range cacheExtensions { if ext == preferredExt { continue }; if path, foundExt := try(ext); path != "" { return path, foundExt } }
	return "", ""
}

func cacheKey(raw string,p URLPolicy)(string,string,error){return cacheKeyForResource(raw,"",p)}
func cacheKeyForResource(raw, _ string, p URLPolicy)(string,string,error){
	u,err:=url.Parse(strings.TrimSpace(raw))
	if err!=nil||u.Scheme!="https"||u.Host==""||p==nil||!p(u){return "","",errors.New("图片地址不符合平台 HTTPS 域名策略")}
	// File identity follows the exact source URL, not the logical resource ID.
	// A replaced image receives a new immutable path, preserving historical files.
	sum:=sha256.Sum256([]byte(strings.TrimSpace(raw)))
	ext:=strings.ToLower(filepath.Ext(u.Path))
	if at:=strings.IndexByte(ext,'@');at>=0{ext=ext[:at]}
	switch ext{case ".png",".jpg",".jpeg",".gif",".webp",".avif",".bmp":default:ext=".img"}
	return hex.EncodeToString(sum[:]),ext,nil
}

func(m *Manager)CachedURL(raw string)string{return m.CachedURLFor("bilibili",raw)}
func(m *Manager)Prefetch(raw string){m.PrefetchFor("bilibili",raw)}
func(m *Manager)CachedURLFor(platformID,raw string)string{return m.CachedURLForResource(platformID,"",raw)}
func(m *Manager)PrefetchFor(platformID,raw string){m.PrefetchForResource(platformID,"",raw)}
func resourceIdentity(platformID, _ ,raw string)string{return platformID+"\x00url\x00"+strings.TrimSpace(raw)}
func(m *Manager)CachedURLForResource(platformID,resourceID,raw string)string{if m==nil{return ""};stableID:=strings.TrimSpace(resourceID);if stableID!=""{stableID=platformID+":"+stableID};key,ext,err:=cacheKeyForResource(raw,stableID,m.policies[platformID]);if err!=nil{return ""};identity:=resourceIdentity(platformID,resourceID,raw);m.mu.RLock();path:=m.ready[identity];m.mu.RUnlock();if path==""{path,_=existingCachePath(filepath.Join(m.root,platformID),key,ext);if path==""{return ""};m.mu.Lock();m.ready[identity]=path;m.mu.Unlock()};return routePrefix+url.PathEscape(platformID)+"/"+key+filepath.Ext(path)}
func(m *Manager)PrefetchForResource(platformID,resourceID,raw string){if m==nil{return};stableID:=strings.TrimSpace(resourceID);if stableID!=""{stableID=platformID+":"+stableID};if _,_,err:=cacheKeyForResource(raw,stableID,m.policies[platformID]);err!=nil{return};identity:=resourceIdentity(platformID,resourceID,raw);m.mu.Lock();if m.ready[identity]!=""||m.inflight[identity]||time.Now().Before(m.failedUntil[identity]){m.mu.Unlock();return};m.inflight[identity]=true;m.mu.Unlock();select{case m.queue<-downloadTask{platformID:platformID,resourceID:strings.TrimSpace(resourceID),raw:raw}:default:m.mu.Lock();delete(m.inflight,identity);m.mu.Unlock()}}
func(m *Manager)worker(){for task:=range m.queue{err:=m.download(task);k:=resourceIdentity(task.platformID,task.resourceID,task.raw);m.mu.Lock();delete(m.inflight,k);if err!=nil{m.failedUntil[k]=time.Now().Add(30*time.Second)}else{delete(m.failedUntil,k)};m.mu.Unlock()}}
func(m *Manager)download(task downloadTask)error{p:=m.policies[task.platformID];stableID:=strings.TrimSpace(task.resourceID);if stableID!=""{stableID=task.platformID+":"+stableID};key,ext,err:=cacheKeyForResource(task.raw,stableID,p);if err!=nil{return err};platformRoot:=filepath.Join(m.root,task.platformID);if err:=os.MkdirAll(platformRoot,0o755);err!=nil{return err};identity:=resourceIdentity(task.platformID,task.resourceID,task.raw);if existing, _ := existingCachePath(platformRoot,key,ext); existing != "" { m.mu.Lock();m.ready[identity]=existing;m.mu.Unlock();return nil };ctx,cancel:=context.WithTimeout(context.Background(),8*time.Second);defer cancel();req,err:=http.NewRequestWithContext(ctx,http.MethodGet,task.raw,nil);if err!=nil{return err};req.Header.Set("User-Agent","My-LiveHub/1.0");client:=*m.client;client.CheckRedirect=func(req *http.Request,via []*http.Request)error{if len(via)>=3{return errors.New("图片重定向次数过多")};_,_,err:=cacheKey(req.URL.String(),p);return err};client.Transport=&http.Transport{DialContext:func(ctx context.Context,network,addr string)(net.Conn,error){host,port,err:=net.SplitHostPort(addr);if err!=nil{return nil,err};ips,err:=net.DefaultResolver.LookupIPAddr(ctx,host);if err!=nil{return nil,err};for _,item:=range ips{ip:=item.IP;if ip==nil||!ip.IsGlobalUnicast()||ip.IsPrivate()||ip.IsLoopback()||ip.IsLinkLocalUnicast()||ip.IsLinkLocalMulticast()||ip.IsUnspecified(){continue};return (&net.Dialer{Timeout:5*time.Second}).DialContext(ctx,network,net.JoinHostPort(ip.String(),port))};return nil,errors.New("图片域名解析到非公网地址")}};resp,err:=client.Do(req);if err!=nil{return err};defer resp.Body.Close();if resp.StatusCode<200||resp.StatusCode>=300{return fmt.Errorf("图片服务器返回无效响应")};tmp,err:=os.CreateTemp(platformRoot,".download-*");if err!=nil{return err};tmpName:=tmp.Name();defer os.Remove(tmpName);prefix:=make([]byte,512);prefixN,readErr:=io.ReadFull(resp.Body,prefix);if readErr!=nil&&readErr!=io.EOF&&readErr!=io.ErrUnexpectedEOF{tmp.Close();return readErr};prefix=prefix[:prefixN];detected:=http.DetectContentType(prefix);actualExt,ok:=imageExtension(resp.Header.Get("Content-Type"),detected);if !ok{tmp.Close();return fmt.Errorf("图片服务器返回不支持的图片格式")};n,copyErr:=io.Copy(tmp,io.LimitReader(io.MultiReader(bytes.NewReader(prefix),resp.Body),maxImageSize+1));if copyErr!=nil||n<=0||n>maxImageSize{tmp.Close();return fmt.Errorf("图片内容为空、过大或读取失败")};if err:=tmp.Sync();err!=nil{tmp.Close();return err};if err:=tmp.Close();err!=nil{return err};target:=filepath.Join(platformRoot,key+actualExt);if err:=os.Rename(tmpName,target);err!=nil{if info,e:=os.Stat(target);e!=nil||!info.Mode().IsRegular(){return err}};m.mu.Lock();m.ready[identity]=target;m.mu.Unlock();return nil}
// ServeHTTP serves only validated cache filenames and delegates all other paths to the app.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m != nil && strings.HasPrefix(r.URL.Path, routePrefix) {
		remainder := strings.TrimPrefix(r.URL.Path, routePrefix)
		parts := strings.SplitN(remainder, "/", 2)
		if len(parts) != 2 { http.NotFound(w, r); return }
		platformID, err := url.PathUnescape(parts[0])
		if err != nil || m.policies[platformID] == nil { http.NotFound(w, r); return }
		name := parts[1]
		if len(name) < 64 || len(name) > 70 { http.NotFound(w, r); return }
		hash, ext := name[:64], name[64:]
		if _, err := hex.DecodeString(hash); err != nil { http.NotFound(w, r); return }
		switch ext { case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".img": default: http.NotFound(w, r); return }
		path := filepath.Join(m.root, platformID, hash+ext)
		file, err := os.Open(path)
		if err != nil { http.NotFound(w, r); return }
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() { http.NotFound(w, r); return }
		contentType := mime.TypeByExtension(ext)
		if ext == ".img" || contentType == "" {
			prefix := make([]byte, 512)
			n, _ := file.ReadAt(prefix, 0)
			contentType = http.DetectContentType(prefix[:n])
		}
		if contentType == "" { contentType = "application/octet-stream" }
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, name, info.ModTime(), file)
		return
	}
	if m != nil && m.fallback != nil { m.fallback.ServeHTTP(w, r); return }
	http.NotFound(w, r)
}
