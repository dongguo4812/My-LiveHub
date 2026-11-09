package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryStore struct { data []byte }
func (s *memoryStore) Save(data []byte) error { s.data=append([]byte(nil),data...); return nil }
func (s *memoryStore) Load() ([]byte,error) { if len(s.data)==0{return nil,ErrCredentialNotFound}; return append([]byte(nil),s.data...),nil }
func (s *memoryStore) Delete() error { s.data=nil; return nil }

func TestBeginLoginAndPollSuccess(t *testing.T) {
	store:=&memoryStore{}
	var server *httptest.Server
	server=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path,"qrcode/generate"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]string{"url":"https://passport.bilibili.com/scan?key=abc","qrcode_key":"qr-key"}})
		case strings.Contains(r.URL.Path,"qrcode/poll"):
			http.SetCookie(w,&http.Cookie{Name:"SESSDATA",Value:"session-secret",Path:"/"})
			http.SetCookie(w,&http.Cookie{Name:"DedeUserID",Value:"12345",Path:"/"})
			http.SetCookie(w,&http.Cookie{Name:"bili_jct",Value:"csrf-secret",Path:"/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]any{"code":0,"refresh_token":"refresh-secret"}})
		case strings.Contains(r.URL.Path,"web-interface/nav"):
			if !strings.Contains(r.Header.Get("Cookie"),"SESSDATA=session-secret") { t.Errorf("session cookie not forwarded") }
			_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]any{"isLogin":true,"mid":json.Number("12345"),"uname":"test-user","face":"https://example.com/avatar.png"}})
		default: http.NotFound(w,r)
		}
	}))
	defer server.Close()
	client:=server.Client()
	// Redirect API endpoints to the local test server through a transport rewrite.
	client.Transport=roundTripFunc(func(req *http.Request)(*http.Response,error){ clone:=req.Clone(req.Context()); u:=*req.URL; u.Scheme="http";u.Host=strings.TrimPrefix(server.URL,"http://");clone.URL=&u;return http.DefaultTransport.RoundTrip(clone) })
	manager,_:=NewAuthManager(store,client)
	manager.now=func()time.Time{return time.Unix(1000,0)}
	login,err:=manager.BeginLogin(context.Background())
	if err!=nil {t.Fatal(err)}
	if !strings.HasPrefix(login.QRCodeDataURL,"data:image/png;base64,"){t.Fatalf("unexpected QR image: %q",login.QRCodeDataURL[:20])}
	status,err:=manager.PollLogin(context.Background(),login.ID)
	if err!=nil {t.Fatal(err)}
	if status.Status!="success" || status.Account==nil || status.Account.UID!="12345" {t.Fatalf("unexpected login status: %#v",status)}
	if len(store.data)==0 || strings.Contains(string(store.data),"session-secret") {t.Fatal("credentials were not stored as opaque test-store payload")}
}

func TestPollExpiredSession(t *testing.T) {
	manager,_:=NewAuthManager(&memoryStore{},nil)
	manager.now=func()time.Time{return time.Unix(1000,0)}
	manager.pending["session"]=pendingLogin{key:"key",expiresAt:time.Unix(999,0)}
	status,err:=manager.PollLogin(context.Background(),"session")
	if err!=nil || status.Status!="expired" {t.Fatalf("got %#v, %v",status,err)}
}

func TestCurrentAccountClearsExpiredCredentials(t *testing.T) {
	store:=&memoryStore{data:[]byte(`{"cookie":"expired","uid":"1"}`)}
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_ = json.NewEncoder(w).Encode(map[string]any{"code":-101,"message":"not login","data":map[string]any{"isLogin":false}})}))
	defer server.Close()
	client:=server.Client()
	client.Transport=roundTripFunc(func(req *http.Request)(*http.Response,error){clone:=req.Clone(req.Context());u:=*req.URL;u.Scheme="http";u.Host=strings.TrimPrefix(server.URL,"http://");clone.URL=&u;return http.DefaultTransport.RoundTrip(clone)})
	manager,_:=NewAuthManager(store,client)
	account,err:=manager.CurrentAccount(context.Background())
	if err!=nil || account!=nil || len(store.data)!=0 {t.Fatalf("expected expired credentials to be cleared, got %#v, %v",account,err)}
}

func TestMissingCredentials(t *testing.T) {
	manager,_:=NewAuthManager(&memoryStore{},nil)
	account,err:=manager.CurrentAccount(context.Background())
	if err!=nil || account!=nil {t.Fatalf("got %#v, %v",account,err)}
}

type roundTripFunc func(*http.Request)(*http.Response,error)
func (f roundTripFunc) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}

var _ = errors.Is
