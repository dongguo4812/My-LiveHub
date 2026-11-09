package resource
import("sort";"strings";"sync";"time")
type Type string
const(TypeEmoticon Type="emoticon";TypeGift Type="gift")
type Resource struct{PlatformID string `json:"platform_id"`;SourceID string `json:"source_id,omitempty"`;Type Type `json:"resource_type"`;ID string `json:"resource_id,omitempty"`;Name string `json:"name"`;ImageURL string `json:"image_url"`;Width int `json:"width,omitempty"`;Height int `json:"height,omitempty"`;FirstSeenAt time.Time `json:"first_seen_at"`;UpdatedAt time.Time `json:"updated_at"`}
type Catalog struct{mu sync.RWMutex;items map[string]Resource}
func NewCatalog()*Catalog{return &Catalog{items:make(map[string]Resource)}}
func identity(r Resource)string{return strings.Join([]string{r.PlatformID,r.SourceID,string(r.Type),r.ID},"\x00")}
func(c *Catalog)Register(r Resource)bool{if c==nil{return false};r.PlatformID=strings.TrimSpace(r.PlatformID);r.SourceID=strings.TrimSpace(r.SourceID);r.ID=strings.TrimSpace(r.ID);r.Name=strings.TrimSpace(r.Name);r.ImageURL=strings.TrimSpace(r.ImageURL);if r.PlatformID==""||(r.Type!=TypeEmoticon&&r.Type!=TypeGift)||r.ImageURL==""{return false};if r.ID==""{r.ID=r.ImageURL};now:=time.Now();c.mu.Lock();defer c.mu.Unlock();key:=identity(r);if old,ok:=c.items[key];ok{r.FirstSeenAt=old.FirstSeenAt;if r.FirstSeenAt.IsZero(){r.FirstSeenAt=now};if old.Name!=""&&r.Name==""{r.Name=old.Name}}else{r.FirstSeenAt=now};r.UpdatedAt=now;c.items[key]=r;return true}
func(c *Catalog)List(platformID,sourceID string,kind Type)[]Resource{if c==nil{return nil};c.mu.RLock();out:=make([]Resource,0);for _,r:=range c.items{if platformID!=""&&r.PlatformID!=platformID{continue};if sourceID!=""&&r.SourceID!=sourceID{continue};if kind!=""&&r.Type!=kind{continue};out=append(out,r)};c.mu.RUnlock();sort.Slice(out,func(i,j int)bool{if out[i].Name!=out[j].Name{return out[i].Name<out[j].Name};return out[i].ID<out[j].ID});return out}
