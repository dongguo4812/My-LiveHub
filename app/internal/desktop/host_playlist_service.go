package desktop

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "math/rand"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"
 "github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const hostQueueMarker = "__my_livehub_host_playlist__"

type HostPlaylistSnapshot struct {
 Tracks []music.Track `json:"tracks"`
 Mode string `json:"mode"`
}
type hostPlaylistState struct {
 mu sync.Mutex
 loaded bool
 tracks []music.Track
 mode string
 index int
 resumeItem *music.QueueItem
 resumePosition int64
 resumePending bool
}
var hostPlaylistStates sync.Map
func hostStateFor(s *MusicService) *hostPlaylistState {
 if v,ok:=hostPlaylistStates.Load(s);ok{return v.(*hostPlaylistState)}
 st:=&hostPlaylistState{mode:"list",index:-1}; actual,_:=hostPlaylistStates.LoadOrStore(s,st);return actual.(*hostPlaylistState)
}
func(s *MusicService)hostPlaylistPath()(string,error){
 root,err:=os.UserConfigDir();if err!=nil{return "",err};dir:=filepath.Join(root,"My-LiveHub")
 if err=os.MkdirAll(dir,0755);err!=nil{return "",err};return filepath.Join(dir,"host-playlist.json"),nil
}
func(s *MusicService)loadHostPlaylistLocked(st *hostPlaylistState)error{
 if st.loaded{return nil};path,err:=s.hostPlaylistPath();if err!=nil{return err}
 data,err:=os.ReadFile(path);if err!=nil&&!errors.Is(err,os.ErrNotExist){return err}
 if err==nil{var saved HostPlaylistSnapshot;if err=json.Unmarshal(data,&saved);err!=nil{return fmt.Errorf("读取主播歌单失败：%w",err)};st.tracks=saved.Tracks;if saved.Mode=="list"||saved.Mode=="single"||saved.Mode=="shuffle"{st.mode=saved.Mode}}
 if st.tracks==nil{st.tracks=[]music.Track{}};st.loaded=true;return nil
}
func(s *MusicService)saveHostPlaylistLocked(st *hostPlaylistState)error{
 path,err:=s.hostPlaylistPath();if err!=nil{return err};data,err:=json.MarshalIndent(HostPlaylistSnapshot{Tracks:st.tracks,Mode:st.mode},"","  ");if err!=nil{return err}
 tmp:=path+".tmp";if err=os.WriteFile(tmp,data,0600);err!=nil{return err};if err=os.Rename(tmp,path);err!=nil{_ = os.Remove(tmp);return err};return nil
}
func(s *MusicService)GetHostPlaylist()(HostPlaylistSnapshot,error){
 if s==nil{return HostPlaylistSnapshot{},errors.New("音乐服务不可用")};st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
 if err:=s.loadHostPlaylistLocked(st);err!=nil{return HostPlaylistSnapshot{},err};return HostPlaylistSnapshot{Tracks:append([]music.Track(nil),st.tracks...),Mode:st.mode},nil
}
func(s *MusicService)AddHostTrack(t music.Track)error{
 if strings.TrimSpace(t.ID)==""||strings.TrimSpace(t.SourceID)==""||strings.TrimSpace(t.Title)==""{return errors.New("歌曲 ID、音源和名称不能为空")}
 if t.DurationMS<0{return errors.New("歌曲时长不能为负数")};st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
 if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};for _,v:=range st.tracks{if v.SourceID==t.SourceID&&v.ID==t.ID{return nil}}
 st.tracks=append(st.tracks,t);return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)RemoveHostTrack(sourceID,trackID string)error{
 st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock();if err:=s.loadHostPlaylistLocked(st);err!=nil{return err}
 for i,t:=range st.tracks{if t.SourceID==sourceID&&t.ID==trackID{st.tracks=append(st.tracks[:i],st.tracks[i+1:]...);if st.index>=len(st.tracks){st.index=len(st.tracks)-1};return s.saveHostPlaylistLocked(st)}};return errors.New("主播歌单中不存在这首歌曲")
}
func(s *MusicService)MoveHostTrack(from,to int)error{
 st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock();if err:=s.loadHostPlaylistLocked(st);err!=nil{return err}
 if from<0||from>=len(st.tracks)||to<0||to>=len(st.tracks){return errors.New("歌单排序位置无效")}
 t:=st.tracks[from];copy(st.tracks[from:],st.tracks[from+1:]);st.tracks=st.tracks[:len(st.tracks)-1];st.tracks=append(st.tracks,music.Track{});copy(st.tracks[to+1:],st.tracks[to:]);st.tracks[to]=t;return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)SetHostPlayMode(mode string)error{
 if mode!="list"&&mode!="single"&&mode!="shuffle"{return errors.New("播放模式仅支持 list、single、shuffle")};st:=hostStateFor(s);st.mu.Lock();defer st.mu.Unlock()
 if err:=s.loadHostPlaylistLocked(st);err!=nil{return err};st.mode=mode;return s.saveHostPlaylistLocked(st)
}
func(s *MusicService)PlayHostPlaylist()error{return s.PlayHostTrack(0)}
func(s *MusicService)PlayHostTrack(index int)error{
 st:=hostStateFor(s);st.mu.Lock();if err:=s.loadHostPlaylistLocked(st);err!=nil{st.mu.Unlock();return err}
 if len(st.tracks)==0{st.mu.Unlock();return errors.New("主播歌单为空")};if index<0||index>=len(st.tracks){st.mu.Unlock();return errors.New("主播歌单索引无效")}
 st.index=index;st.resumePending=false;st.resumeItem=nil;track:=st.tracks[index];st.mu.Unlock()
 s.cancelPlaybackWatcher();_ = s.engine.Stop(context.Background());s.queue.Clear();s.queue.FinishCurrent()
 item:=music.QueueItem{ID:fmt.Sprintf("host-%d-%d",time.Now().UnixNano(),index),Track:track,RequestedBy:hostQueueMarker,AddedAt:time.Now().UTC()}
 if err:=s.queue.SetCurrent(item);err!=nil{return err};return s.PlayNext()
}
func(s *MusicService)advanceHostPlaylist()error{
 st:=hostStateFor(s);st.mu.Lock();if err:=s.loadHostPlaylistLocked(st);err!=nil{st.mu.Unlock();return err}
 n,cur,mode:=len(st.tracks),st.index,st.mode;next:=cur+1
 switch mode{case "single":next=cur;case "shuffle":if n>1{next=rand.New(rand.NewSource(time.Now().UnixNano())).Intn(n-1);if next>=cur{next++}};case "list":if next>=n{next=0}}
 if next>=n{st.mu.Unlock();return nil};st.index=next;st.mu.Unlock();return s.PlayHostTrack(next)
}
func(s *MusicService)resumeHostPlaylist()bool{
 st:=hostStateFor(s);st.mu.Lock();if !st.resumePending||st.resumeItem==nil{st.mu.Unlock();return false}
 item,pos:=*st.resumeItem,st.resumePosition;st.resumePending=false;st.resumeItem=nil;st.resumePosition=0;st.mu.Unlock()
 if err:=s.queue.SetCurrent(item);err!=nil{s.setLastError(err);return false};if err:=s.PlayNext();err!=nil{s.setLastError(err);return false};if pos>0{time.Sleep(300*time.Millisecond);_ = s.Seek(pos)};return true
}
func(s *MusicService)StopHostPlayback()error{
 st:=hostStateFor(s);st.mu.Lock();st.resumePending=false;st.resumeItem=nil;st.mu.Unlock()
 if cur:=s.queue.Snapshot().Current;cur!=nil&&cur.RequestedBy==hostQueueMarker{return s.Stop()};return nil
}
