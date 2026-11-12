package desktop

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const hostQueueMarker = "__my_livehub_host_playlist__"

type HostPlaylistSnapshot struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Tracks []music.Track `json:"tracks"`
	Mode string `json:"mode"`
}
type HostPlaylistSummary struct {
	ID string `json:"id"`
	Name string `json:"name"`
	TrackCount int `json:"track_count"`
	Mode string `json:"mode"`
}
type hostPlaylistFile struct {
	ActiveID string `json:"active_id"`
	Playlists []HostPlaylistSnapshot `json:"playlists"`
}
type hostPlaylistState struct {
	mu sync.Mutex
	loaded bool
	playlists []HostPlaylistSnapshot
	activeID string
	index int
	resumeItem *music.QueueItem
	resumePosition int64
	resumePending bool
	resumeWasPaused bool
}
var hostPlaylistStates sync.Map

func hostStateFor(s *MusicService) *hostPlaylistState {
	if v,ok:=hostPlaylistStates.Load(s);ok{return v.(*hostPlaylistState)}
	st:=&hostPlaylistState{index:-1}
	actual,_:=hostPlaylistStates.LoadOrStore(s,st)
	return actual.(*hostPlaylistState)
}
func(s *MusicService)hostPlaylistPath()(string,error){
	root,err:=os.UserConfigDir();if err!=nil{return "",err}
	dir:=filepath.Join(root,"My-LiveHub")
	if err=os.MkdirAll(dir,0755);err!=nil{return "",err}
	return filepath.Join(dir,"host-playlists.json"),nil
}
func(s *MusicService)loadHostPlaylistLocked(st *hostPlaylistState)error{
	if st.loaded{return nil}
	path,err:=s.hostPlaylistPath();if err!=nil{return err}
	data,err:=os.ReadFile(path)
	if err!=nil&&!errors.Is(err,os.ErrNotExist){return err}
	if err==nil {
		var saved hostPlaylistFile
		if err=json.Unmarshal(data,&saved);err!=nil{return fmt.Errorf("读取主播歌单失败：%w",err)}
		st.playlists=saved.Playlists
		st.activeID=saved.ActiveID
	}
	if len(st.playlists)==0 {
		st.playlists=[]HostPlaylistSnapshot{{ID:"playlist-default",Name:"默认歌单",Tracks:[]music.Track{},Mode:"list"}}
		st.activeID=st.playlists[0].ID
	}
	for i:=range st.playlists {
		if st.playlists[i].Tracks==nil { st.playlists[i].Tracks=[]music.Track{} }
		if !validHostPlayMode(st.playlists[i].Mode) { st.playlists[i].Mode="list" }
		if strings.TrimSpace(st.playlists[i].Name)=="" { st.playlists[i].Name=fmt.Sprintf("歌单 %d",i+1) }
	}
	if findHostPlaylist(st,st.activeID)<0 { st.activeID=st.playlists[0].ID }
	st.loaded=true
	return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)saveHostPlaylistLocked(st *hostPlaylistState)error{
	path,err:=s.hostPlaylistPath();if err!=nil{return err}
	data,err:=json.MarshalIndent(hostPlaylistFile{ActiveID:st.activeID,Playlists:st.playlists},"","  ");if err!=nil{return err}
	tmp:=path+".tmp";if err=os.WriteFile(tmp,data,0600);err!=nil{return err}
	if err=os.Rename(tmp,path);err!=nil{_ = os.Remove(tmp);return err}
	return nil
}

func moveHostTrackBetweenPlaylists(st *hostPlaylistState, fromID, toID, sourceID, trackID string) error {
	if fromID == "" || toID == "" || fromID == toID { return errors.New("请选择两个不同的歌单") }
	fromIndex, toIndex := findHostPlaylist(st, fromID), findHostPlaylist(st, toID)
	if fromIndex < 0 || toIndex < 0 { return errors.New("找不到源歌单或目标歌单") }
	from, to := &st.playlists[fromIndex], &st.playlists[toIndex]
	sourceIndex := -1
	var moving music.Track
	for i, track := range from.Tracks { if track.SourceID == sourceID && track.ID == trackID { sourceIndex, moving = i, track; break } }
	if sourceIndex < 0 { return errors.New("源歌单中不存在这首歌曲") }
	for _, track := range to.Tracks { if track.SourceID == moving.SourceID && track.ID == moving.ID { return errors.New("目标歌单已经有这首歌曲") } }
	// If the moved song is before or is the current song, the cursor must
	// point to the item immediately before the next song to be played.
	if fromID == st.activeID && sourceIndex <= st.index { st.index-- }
	from.Tracks = append(from.Tracks[:sourceIndex], from.Tracks[sourceIndex+1:]...)
	if fromID == st.activeID && st.index >= len(from.Tracks) { st.index = len(from.Tracks)-1 }
	to.Tracks = append(to.Tracks, moving)
	return nil
}

func validHostPlayMode(mode string)bool{return mode=="list"||mode=="single"||mode=="shuffle"}
func findHostPlaylist(st *hostPlaylistState,id string)int{for i,p:=range st.playlists{if p.ID==id{return i}};return -1}
func activeHostPlaylist(st *hostPlaylistState)*HostPlaylistSnapshot{i:=findHostPlaylist(st,st.activeID);if i<0{return nil};return &st.playlists[i]}
func playlistSummary(p HostPlaylistSnapshot)HostPlaylistSummary{return HostPlaylistSummary{ID:p.ID,Name:p.Name,TrackCount:len(p.Tracks),Mode:p.Mode}}
func(s *MusicService)GetHostPlaylists()([]HostPlaylistSummary,string,error){
	if s==nil{return nil,"",errors.New("音乐服务不可用")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return nil,"",err}
	out:=make([]HostPlaylistSummary,0,len(st.playlists));for _,p:=range st.playlists{out=append(out,playlistSummary(p))}
	return out,st.activeID,nil
}
func(s *MusicService)SelectHostPlaylist(id string)error{
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err}
	if findHostPlaylist(st,id)<0{return errors.New("找不到指定歌单")}
	st.activeID=id;st.index=-1
	return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)CreateHostPlaylist(name string)(HostPlaylistSummary,error){
	name=strings.TrimSpace(name);if name==""{return HostPlaylistSummary{},errors.New("请输入歌单名称")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return HostPlaylistSummary{},err}
	for _,p:=range st.playlists{if strings.EqualFold(p.Name,name){return HostPlaylistSummary{},errors.New("已存在同名歌单")}}
	p:=HostPlaylistSnapshot{ID:fmt.Sprintf("playlist-%d",time.Now().UnixNano()),Name:name,Tracks:[]music.Track{},Mode:"list"}
	st.playlists=append(st.playlists,p);if err:=s.saveHostPlaylistLocked(st);err!=nil{return HostPlaylistSummary{},err}
	return playlistSummary(p),nil
}
func(s *MusicService)RenameHostPlaylist(id,name string)error{
	name=strings.TrimSpace(name);if name==""{return errors.New("请输入歌单名称")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	for j,p:=range st.playlists{if j!=i&&strings.EqualFold(p.Name,name){return errors.New("已存在同名歌单")}}
	st.playlists[i].Name=name;return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)DeleteHostPlaylist(id string)error{
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err}
	if len(st.playlists)<=1{return errors.New("至少保留一个歌单")}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	st.playlists=append(st.playlists[:i],st.playlists[i+1:]...)
	if st.activeID==id{st.activeID=st.playlists[0].ID;st.index=-1}
	return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)GetHostPlaylistByID(id string)(HostPlaylistSnapshot,error){
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return HostPlaylistSnapshot{},err}
	i:=findHostPlaylist(st,id);if i<0{return HostPlaylistSnapshot{},errors.New("找不到指定歌单")}
	p:=st.playlists[i];p.Tracks=append([]music.Track(nil),p.Tracks...);return p,nil
}
func(s *MusicService)GetHostPlaylist()(HostPlaylistSnapshot,error){
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return HostPlaylistSnapshot{},err}
	p:=activeHostPlaylist(st);if p==nil{return HostPlaylistSnapshot{},errors.New("当前歌单不存在")}
	out:=*p;out.Tracks=append([]music.Track(nil),p.Tracks...);return out,nil
}
func(s *MusicService)AddHostTrack(t music.Track)error{return s.AddHostTrackToPlaylist("",t)}
func(s *MusicService)AddHostTrackToPlaylist(id string,t music.Track)error{
	if strings.TrimSpace(t.ID)==""||strings.TrimSpace(t.SourceID)==""||strings.TrimSpace(t.Title)==""{return errors.New("歌曲 ID、音源和名称不能为空")}
	if t.DurationMS<0{return errors.New("歌曲时长不能为负数")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};if id==""{id=st.activeID}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	for _,v:=range st.playlists[i].Tracks{if v.SourceID==t.SourceID&&v.ID==t.ID{return nil}}
	st.playlists[i].Tracks=append(st.playlists[i].Tracks,t);return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)RemoveHostTrack(sourceID,trackID string)error{return s.RemoveHostTrackFromPlaylist("",sourceID,trackID)}
func(s *MusicService)RemoveHostTrackFromPlaylist(id,sourceID,trackID string)error{
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};if id==""{id=st.activeID}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	tracks:=st.playlists[i].Tracks
	for j,t:=range tracks{if t.SourceID==sourceID&&t.ID==trackID{st.playlists[i].Tracks=append(tracks[:j],tracks[j+1:]...);if id==st.activeID&&st.index>=len(st.playlists[i].Tracks){st.index=len(st.playlists[i].Tracks)-1};return s.saveHostPlaylistLocked(st)}}
	return errors.New("歌单中不存在这首歌曲")
}

func (s *MusicService) MoveHostTrackBetweenPlaylists(fromID, toID, sourceID, trackID string) error {
	if s == nil { return errors.New("音乐服务不可用") }
	st := hostStateFor(s)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.loadHostPlaylistLocked(st); err != nil { return err }
	if err := moveHostTrackBetweenPlaylists(st, fromID, toID, sourceID, trackID); err != nil { return err }
	return s.saveHostPlaylistLocked(st)
}

func(s *MusicService)MoveHostTrack(from,to int)error{return s.MoveHostTrackInPlaylist("",from,to)}
func(s *MusicService)MoveHostTrackInPlaylist(id string,from,to int)error{
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};if id==""{id=st.activeID}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	tracks:=st.playlists[i].Tracks;if from<0||from>=len(tracks)||to<0||to>=len(tracks){return errors.New("歌单排序位置无效")}
	t:=tracks[from];copy(tracks[from:],tracks[from+1:]);tracks=tracks[:len(tracks)-1];tracks=append(tracks,music.Track{});copy(tracks[to+1:],tracks[to:]);tracks[to]=t;st.playlists[i].Tracks=tracks
	return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)SetHostPlayMode(mode string)error{return s.SetHostPlaylistPlayMode("",mode)}
func(s *MusicService)SetHostPlaylistPlayMode(id,mode string)error{
	if !validHostPlayMode(mode){return errors.New("播放模式仅支持 list、single、shuffle")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};if id==""{id=st.activeID}
	i:=findHostPlaylist(st,id);if i<0{return errors.New("找不到指定歌单")}
	st.playlists[i].Mode=mode;return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)PlayHostPlaylist()error{return s.PlayHostPlaylistByID("")}
func(s *MusicService)PlayHostPlaylistByID(id string)error{return s.PlayHostTrackFromPlaylist(id,0)}
func(s *MusicService)PlayHostTrack(index int)error{return s.PlayHostTrackFromPlaylist("",index)}
func(s *MusicService)PlayHostTrackFromPlaylist(id string,index int)error{
	st:=hostStateFor(s);st.mu.Lock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{st.mu.Unlock();return err}
	if id==""{id=st.activeID}
	i:=findHostPlaylist(st,id);if i<0{st.mu.Unlock();return errors.New("找不到指定歌单")}
	p:=&st.playlists[i]
	if len(p.Tracks)==0{st.mu.Unlock();return errors.New("主播歌单为空")}
	if index<0||index>=len(p.Tracks){st.mu.Unlock();return errors.New("歌单索引无效")}
	st.activeID=id;st.index=index;st.resumePending=false;st.resumeItem=nil;track:=p.Tracks[index]
	if err:=s.saveHostPlaylistLocked(st);err!=nil{st.mu.Unlock();return err}
	st.mu.Unlock()
	s.cancelPlaybackWatcher();_ = s.engine.Stop(context.Background());s.queue.Clear();s.queue.FinishCurrent()
	item:=music.QueueItem{ID:fmt.Sprintf("host-%d-%d",time.Now().UnixNano(),index),Track:track,RequestedBy:hostQueueMarker,AddedAt:time.Now().UTC()}
	if err:=s.queue.SetCurrent(item);err!=nil{return err};return s.PlayNext()
}
func(s *MusicService)advanceHostPlaylist()error{
	st:=hostStateFor(s);st.mu.Lock()
	if err:=s.loadHostPlaylistLocked(st);err!=nil{st.mu.Unlock();return err}
	p:=activeHostPlaylist(st);if p==nil||len(p.Tracks)==0{st.mu.Unlock();return nil}
	n,cur,mode:=len(p.Tracks),st.index,p.Mode;next:=cur+1
	switch mode{case "single":next=cur;case "shuffle":if n>1{next=rand.New(rand.NewSource(time.Now().UnixNano())).Intn(n-1);if next>=cur{next++}};case "list":if next>=n{next=0}}
	st.mu.Unlock()
	if next>=n{return nil}
	return s.PlayHostTrackFromPlaylist("",next)
}
func(s *MusicService)resumeHostPlaylist()bool{
	st:=hostStateFor(s);st.mu.Lock()
	if !st.resumePending||st.resumeItem==nil{st.mu.Unlock();return false}
	item,pos,wasPaused:=*st.resumeItem,st.resumePosition,st.resumeWasPaused
	st.resumePending=false;st.resumeItem=nil;st.resumePosition=0;st.resumeWasPaused=false;st.mu.Unlock()
	if err:=s.queue.SetCurrent(item);err!=nil{s.setLastError(err);return false}
	if err:=s.PlayNext();err!=nil{s.setLastError(err);return false}
	if pos>0{time.Sleep(300*time.Millisecond);_ = s.Seek(pos)}
	if wasPaused{_ = s.Pause()}
	return true
}
func(s *MusicService)StopHostPlayback()error{
	st:=hostStateFor(s);st.mu.Lock();st.resumePending=false;st.resumeItem=nil;st.mu.Unlock()
	if cur:=s.queue.Snapshot().Current;cur!=nil&&cur.RequestedBy==hostQueueMarker{return s.Stop()}
	return nil
}

// ImportLXPlaylistFile imports LX Music playlist backup files (.lxmc) and JSON exports.
// LXMC is a gzip-compressed JSON envelope. Only playlist data is imported.
func(s *MusicService)ImportLXPlaylistFile(filename,contentBase64 string)([]HostPlaylistSummary,error){
	raw,err:=base64.StdEncoding.DecodeString(contentBase64);if err!=nil{return nil,errors.New("歌单文件编码无效")}
	if strings.EqualFold(filepath.Ext(filename),".lxmc")||len(raw)>2&&raw[0]==0x1f&&raw[1]==0x8b{
		zr,e:=gzip.NewReader(bytes.NewReader(raw));if e!=nil{return nil,fmt.Errorf("无法解压洛雪歌单：%w",e)}
		raw,e=io.ReadAll(io.LimitReader(zr,64<<20));_ = zr.Close();if e!=nil{return nil,e}
	}
	var envelope map[string]json.RawMessage
	if err=json.Unmarshal(raw,&envelope);err!=nil{return nil,fmt.Errorf("无法解析歌单文件：%w",err)}
	var kind string;_ = json.Unmarshal(envelope["type"],&kind)
	var listData json.RawMessage
	switch kind{
	case "playList_v2","playList","playListPart_v2","playListPart":
		listData=envelope["data"]
	case "allData_v2","allData":
		var all struct{PlayList json.RawMessage `json:"playList"`}
		if err=json.Unmarshal(raw,&all);err!=nil{return nil,err};listData=all.PlayList
	case "":
		// Accept a plain JSON array of exported playlist objects.
		listData=raw
	default:
		return nil,fmt.Errorf("不支持的洛雪备份类型：%s（请选择歌单备份或完整备份）",kind)
	}
	var rawLists []json.RawMessage
	if len(listData)==0||string(listData)=="null"{return nil,errors.New("文件中没有歌单数据")}
	if listData[0]=='['{if err=json.Unmarshal(listData,&rawLists);err!=nil{return nil,err}}else{rawLists=[]json.RawMessage{listData}}
	imported:=make([]HostPlaylistSnapshot,0,len(rawLists))
	for _,entry:=range rawLists{
		var obj map[string]json.RawMessage;if json.Unmarshal(entry,&obj)!=nil{continue}
		var name,id string;_ = json.Unmarshal(obj["name"],&name);_ = json.Unmarshal(obj["id"],&id)
		var songs []map[string]json.RawMessage;if json.Unmarshal(obj["list"],&songs)!=nil{continue}
		tracks:=make([]music.Track,0,len(songs))
		for _,song:=range songs{if t,ok:=lxBackupTrack(song);ok{tracks=append(tracks,t)}}
		if len(tracks)==0{continue}
		if strings.TrimSpace(name)==""{name="洛雪导入歌单"}
		imported=append(imported,HostPlaylistSnapshot{ID:id,Name:name,Tracks:tracks,Mode:"list"})
	}
	if len(imported)==0{return nil,errors.New("没有找到可导入的在线歌曲；本地文件歌曲无法迁移到另一台播放器")}
	st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
	if err=s.loadHostPlaylistLocked(st);err!=nil{return nil,err}
	created:=make([]HostPlaylistSummary,0,len(imported))
	for i,p:=range imported{
		base:=p.Name;name:=base;suffix:=2
		for nameExists(st,name){name=fmt.Sprintf("%s (%d)",base,suffix);suffix++}
		p.Name=name;p.ID=fmt.Sprintf("lx-import-%d-%d",time.Now().UnixNano(),i)
		st.playlists=append(st.playlists,p);created=append(created,playlistSummary(p))
	}
	if err=s.saveHostPlaylistLocked(st);err!=nil{return nil,err}
	return created,nil
}
func nameExists(st *hostPlaylistState,name string)bool{for _,p:=range st.playlists{if strings.EqualFold(p.Name,name){return true}};return false}
func lxBackupTrack(song map[string]json.RawMessage)(music.Track,bool){
	read:=func(keys ...string)string{for _,key:=range keys{if b:=song[key];len(b)>0{var v string;if json.Unmarshal(b,&v)==nil&&v!=""{return v};var n json.Number;if json.Unmarshal(b,&n)==nil{return n.String()}}};return ""}
	id:=read("id","songmid","hash")
	source:=read("source","source_id")
	title:=read("name","title")
	if id==""{var meta map[string]json.RawMessage;_ = json.Unmarshal(song["meta"],&meta);for _,k:=range []string{"songId","songmid","hash","audioId"}{if b:=meta[k];len(b)>0{_ = json.Unmarshal(b,&id);if id!=""{break}}}}
	if title==""||id==""||source==""||source=="local"{return music.Track{},false}
	artist:=read("singer","artist");album:=read("albumName","album")
	track:=music.Track{ID:id,SourceID:source,LXSourceKey:source,Title:title,Artist:artist,Album:album}
	track.SourceData=map[string]string{}
	for k,v:=range song{if value:=lxBackupScalarString(v);value!=""{track.SourceData[k]=value}}
	var meta map[string]json.RawMessage;_ = json.Unmarshal(song["meta"],&meta)
	for k,v:=range meta{if value:=lxBackupScalarString(v);value!=""{track.SourceData[k]=value}}
	// LX backup formats are not consistent: NetEase identifiers may be stored
	// as meta.songId, while the lyric adapter expects SourceData["songmid"].
	// Normalize the provider-native identifier at import time so the persisted
	// Track is immediately usable by the same lyric path as search results.
	if track.SourceData["songmid"]=="" {
		for _,key:=range []string{"songId","song_id","id"} {
			if value:=strings.TrimSpace(track.SourceData[key]);value!="" { track.SourceData["songmid"]=value;break }
		}
	}
	if track.SourceData["songmid"]=="" { track.SourceData["songmid"]=track.ID }
	_ = json.Unmarshal(meta["picUrl"],&track.CoverURL)
	if d:=read("duration_ms");d!=""{track.DurationMS,_=strconv.ParseInt(d,10,64)} else if interval:=read("interval");interval!=""{track.DurationMS=parseLXInterval(interval)}
	var qualities []map[string]json.RawMessage
	if json.Unmarshal(meta["qualitys"],&qualities)==nil{
		for _,quality:=range qualities{
			var kind,size,hash string
			_ = json.Unmarshal(quality["type"],&kind);_ = json.Unmarshal(quality["size"],&size);_ = json.Unmarshal(quality["hash"],&hash)
			q:=music.TrackQuality{Type:kind,Size:size,SourceData:map[string]string{}}
			if hash!=""{q.SourceData["hash"]=hash}
			if kind!=""{track.Qualities=append(track.Qualities,q)}
		}
	}
	return track,true
}
// lxBackupScalarString preserves provider metadata whether LX serialized a
// native identifier as a JSON string or number.
func lxBackupScalarString(raw json.RawMessage) string {
	if len(raw)==0 { return "" }
	var value string
	if json.Unmarshal(raw,&value)==nil { return value }
	var number json.Number
	if json.Unmarshal(raw,&number)==nil { return number.String() }
	return ""
}

func parseLXInterval(value string)int64{
	parts:=strings.Split(strings.TrimSpace(value),":")
	if len(parts)==2{m,e1:=strconv.ParseInt(parts[0],10,64);sec,e2:=strconv.ParseInt(parts[1],10,64);if e1==nil&&e2==nil{return (m*60+sec)*1000}}
	if seconds,err:=strconv.ParseFloat(value,64);err==nil&&seconds>0{return int64(seconds*1000)}
	return 0
}


// HostTrackSourceResults keeps replacement candidates grouped by their native
// music platform, matching the source tabs used by LX Music.
type HostTrackSourceResults struct {
	SourceID string        `json:"source_id"`
	Name     string        `json:"name"`
	Tracks   []music.Track `json:"tracks"`
	Error    string        `json:"error,omitempty"`
}

// SearchHostTrackAlternatives searches only the currently enabled LX script
// sources. Each provider keeps its own native result order and metadata.
func (s *MusicService) SearchHostTrackAlternatives(track music.Track) ([]HostTrackSourceResults, error) {
	if s == nil || s.searcher == nil {
		return nil, errors.New("洛雪搜索 SDK 未初始化")
	}
	keyword := strings.TrimSpace(track.Title + " " + track.Artist)
	if strings.TrimSpace(track.Title) == "" {
		return nil, errors.New("原歌曲缺少歌名，无法换源搜索")
	}
	sourceIDs := s.activeLXSearchSourceIDs()
	if len(sourceIDs) == 0 {
		return []HostTrackSourceResults{}, nil
	}
	names := make(map[string]string)
	for _, source := range s.searcher.Sources() {
		names[source.ID] = source.Name
	}
	type outcome struct {
		result HostTrackSourceResults
	}
	results := make([]HostTrackSourceResults, len(sourceIDs))
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, sourceID := range sourceIDs {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			group := HostTrackSourceResults{SourceID: id, Name: names[id], Tracks: []music.Track{}}
			page, err := s.searcher.SearchProvider(ctx, id, keyword, 1)
			if err != nil {
				group.Error = err.Error()
			} else {
				group.Tracks = page.Tracks
			}
			results[index] = group
		}(i, sourceID)
	}
	wg.Wait()
	return results, nil
}

// ReplaceHostTrackInPlaylist replaces one saved song with the complete Track
// returned by the provider search, retaining the original playlist position.
func (s *MusicService) ReplaceHostTrackInPlaylist(playlistID, oldSourceID, oldTrackID string, replacement music.Track) error {
	if s == nil {
		return errors.New("音乐服务不可用")
	}
	if err := musicTrackReplacementValid(replacement); err != nil {
		return err
	}
	st := hostStateFor(s)
	st.mu.Lock()
	if err := s.loadHostPlaylistLocked(st); err != nil {
		st.mu.Unlock()
		return err
	}
	if playlistID == "" {
		playlistID = st.activeID
	}
	playlistIndex := findHostPlaylist(st, playlistID)
	if playlistIndex < 0 {
		st.mu.Unlock()
		return errors.New("找不到指定歌单")
	}
	playlist := &st.playlists[playlistIndex]
	targetIndex := -1
	for i, item := range playlist.Tracks {
		if item.SourceID == oldSourceID && item.ID == oldTrackID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		st.mu.Unlock()
		return errors.New("原歌曲已不在当前歌单，请刷新后重试")
	}
	if replacement.SourceID == oldSourceID && replacement.ID == oldTrackID {
		st.mu.Unlock()
		return errors.New("所选歌曲与原歌曲相同，无需换源")
	}
	// Match LX behavior: if the selected candidate already exists elsewhere
	// in this playlist, remove that duplicate and keep the replacement at the
	// original song's position.
	duplicateIsCurrent := false
	for i, item := range playlist.Tracks {
		if i == targetIndex || item.SourceID != replacement.SourceID || item.ID != replacement.ID {
			continue
		}
		duplicateIsCurrent = playlistID == st.activeID && st.index == i
		playlist.Tracks = append(playlist.Tracks[:i], playlist.Tracks[i+1:]...)
		if i < targetIndex {
			targetIndex--
		}
		if playlistID == st.activeID && i < st.index {
			st.index--
		}
		break
	}
	playlist.Tracks[targetIndex] = replacement
	if duplicateIsCurrent {
		st.index = targetIndex
	}
	isCurrent := !duplicateIsCurrent && playlistID == st.activeID && st.index == targetIndex
	if err := s.saveHostPlaylistLocked(st); err != nil {
		st.mu.Unlock()
		return err
	}
	st.mu.Unlock()

	if isCurrent {
		current := s.queue.Snapshot().Current
		if current != nil && current.RequestedBy == hostQueueMarker &&
			current.Track.SourceID == oldSourceID && current.Track.ID == oldTrackID {
			snapshot, snapshotErr := s.engine.Snapshot(context.Background())
			if snapshotErr != nil {
				return fmt.Errorf("歌曲已在歌单中替换，但读取当前播放状态失败：%w", snapshotErr)
			}
			if _, err := s.queue.SetCurrentTrack(replacement, hostQueueMarker); err != nil {
				return err
			}
			s.cancelPlaybackWatcher()
			if err := s.PlayNext(); err != nil {
				return fmt.Errorf("歌曲已在歌单中替换，但切换当前播放失败：%w", err)
			}
			if snapshot.PositionMS > 0 {
				_ = s.Seek(snapshot.PositionMS)
			}
			if snapshot.State == music.PlaybackPaused {
				_ = s.Pause()
			}
		}
	}
	return nil
}

func musicTrackReplacementValid(track music.Track) error {
	if strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.SourceID) == "" || strings.TrimSpace(track.Title) == "" {
		return errors.New("候选歌曲缺少平台歌曲 ID、音源或歌名，无法换源")
	}
	if track.DurationMS < 0 {
		return errors.New("候选歌曲时长无效")
	}
	return nil
}
