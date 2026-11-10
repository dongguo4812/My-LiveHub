package bilibili

import (
	"context"
	"encoding/json"
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
		case strings.Contains(r.URL.Path, "crossDomain"):
			if r.Header.Get("User-Agent") == "" {
				t.Error("ticket request must include browser User-Agent")
			}
			http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "session-secret", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "DedeUserID", Value: "12345", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "bili_jct", Value: "csrf-secret", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case strings.Contains(r.URL.Path,"qrcode/poll"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]any{"code":0,"url":"https://passport.biligame.com/x/passport-login/web/crossDomain?ticket=ticket-value","refresh_token":"refresh-secret"}})
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
	var saved Credentials
	if len(store.data)==0 || json.Unmarshal(store.data, &saved) != nil || saved.Cookie == "" || saved.RefreshToken != "refresh-secret" { t.Fatal("credentials were not saved through the credential store") }
}


func TestLoginWithBrowserCookie(t *testing.T) {
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "SESSDATA=session-value; bili_jct=csrf-value" { t.Errorf("unexpected forwarded cookie: %q", got) }
		_ = json.NewEncoder(w).Encode(map[string]any{"code":0,"data":map[string]any{"isLogin":true,"mid":json.Number("9876"),"uname":"browser-user","face":""}})
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response,error) { clone:=req.Clone(req.Context()); u:=*req.URL; u.Scheme="http"; u.Host=strings.TrimPrefix(server.URL,"http://"); clone.URL=&u; return http.DefaultTransport.RoundTrip(clone) })
	manager,_ := NewAuthManager(store,client)
	account,err := manager.LoginWithCookie(context.Background(),"SESSDATA=session-value; bili_jct=csrf-value; unrelated=discard-me")
	if err != nil { t.Fatal(err) }
	if account.UID != "9876" || account.Name != "browser-user" { t.Fatalf("unexpected account: %#v",account) }
	var saved Credentials
	if err:=json.Unmarshal(store.data,&saved); err!=nil { t.Fatal(err) }
	if strings.Contains(saved.Cookie,"unrelated") || saved.Cookie == "" { t.Fatalf("unexpected stored cookie fields: %q",saved.Cookie) }
}

func TestLoginWithInvalidCookieDoesNotOverwriteCredentials(t *testing.T) {
	store := &memoryStore{data: []byte("{\"cookie\":\"existing-valid-cookie\",\"uid\":\"1\"}")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) { _=json.NewEncoder(w).Encode(map[string]any{"code":-101,"message":"not login","data":map[string]any{"isLogin":false}}) }))
	defer server.Close()
	client:=server.Client()
	client.Transport=roundTripFunc(func(req *http.Request)(*http.Response,error) { clone:=req.Clone(req.Context()); u:=*req.URL; u.Scheme="http"; u.Host=strings.TrimPrefix(server.URL,"http://"); clone.URL=&u; return http.DefaultTransport.RoundTrip(clone) })
	manager,_:=NewAuthManager(store,client); before:=string(store.data)
	_,err:=manager.LoginWithCookie(context.Background(),"SESSDATA=expired")
	if err==nil || string(store.data)!=before { t.Fatal("invalid Cookie should not overwrite existing credentials") }
}

func TestNormalizeCookieRequiresSessdataAndFiltersFields(t *testing.T) {
	got,err:=normalizeCookie("SESSDATA=abc; buvid3=xyz; random=secret")
	if err!=nil { t.Fatal(err) }; if got!="SESSDATA=abc; buvid3=xyz" { t.Fatalf("got %q",got) }
	if _,err:=normalizeCookie("bili_jct=csrf"); err==nil { t.Fatal("expected missing SESSDATA error") }
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


func TestLogoutDeletesCredentialsAndPendingSessions(t *testing.T) {
	store := &memoryStore{data: []byte(`{"cookie":"session-secret","uid":"123"}`)}
	manager, err := NewAuthManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.pending["active"] = pendingLogin{key: "qr-key", expiresAt: time.Now().Add(time.Minute)}

	if err := manager.Logout(); err != nil {
		t.Fatalf("logout failed: %v", err)
	}
	if _, err := store.Load(); err != ErrCredentialNotFound {
		t.Fatalf("expected credentials to be deleted, got error: %v", err)
	}
	if len(manager.pending) != 0 {
		t.Fatalf("expected pending QR sessions to be cleared, got %d", len(manager.pending))
	}
}

func TestMissingCredentials(t *testing.T) {
	manager,_:=NewAuthManager(&memoryStore{},nil)
	account,err:=manager.CurrentAccount(context.Background())
	if err!=nil || account!=nil {t.Fatalf("got %#v, %v",account,err)}
}

type roundTripFunc func(*http.Request)(*http.Response,error)
func (f roundTripFunc) RoundTrip(r *http.Request)(*http.Response,error){return f(r)}



func TestLoadCredentialsPreservesCredentialsOnTemporaryNetworkFailure(t *testing.T) {
	store := &memoryStore{data: []byte(`{"cookie":"valid-session","uid":"123"}`)}
	before := string(store.data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(store, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.LoadCredentials(context.Background())
	if err == nil {
		t.Fatal("expected temporary network failure")
	}
	if string(store.data) != before {
		t.Fatal("temporary network failure must not delete saved credentials")
	}
}

func TestLoadCredentialsDeletesExpiredCredentials(t *testing.T) {
	store := &memoryStore{data: []byte(`{"cookie":"expired-session","uid":"123"}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": -101,
			"message": "not login",
			"data": map[string]any{"isLogin": false},
		})
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(store, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.LoadCredentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "登录已失效") {
		t.Fatalf("expected expired-login error, got %v", err)
	}
	if len(store.data) != 0 {
		t.Fatal("expired credentials should be removed")
	}
}


func TestExchangeLoginTicketCollectsCookiesAcrossRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/passport-login/web/crossDomain":
			http.SetCookie(w, &http.Cookie{Name: "bili_jct", Value: "redirect-csrf", Path: "/"})
			http.Redirect(w, r, "https://passport.bilibili.com/final", http.StatusFound)
		case "/final":
			http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "final-session", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "DedeUserID", Value: "456", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(&memoryStore{}, client)
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.exchangeLoginTicket(context.Background(), "https://passport.biligame.com/x/passport-login/web/crossDomain?ticket=one-time")
	if err != nil {
		t.Fatal(err)
	}
	if values["bili_jct"] != "redirect-csrf" || values["SESSDATA"] != "final-session" || values["DedeUserID"] != "456" {
		t.Fatalf("cookies from ticket redirect chain were not collected: %#v", values)
	}
}

func TestExchangeLoginTicketSupportsLegacyCookieURL(t *testing.T) {
	manager, err := NewAuthManager(&memoryStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.exchangeLoginTicket(context.Background(),
		"https://passport.biligame.com/crossDomain?SESSDATA=legacy-session&DedeUserID=789&bili_jct=legacy-csrf")
	if err != nil {
		t.Fatal(err)
	}
	if values["SESSDATA"] != "legacy-session" || values["DedeUserID"] != "789" || values["bili_jct"] != "legacy-csrf" {
		t.Fatalf("legacy cookie query was not parsed: %#v", values)
	}
}

func TestExchangeLoginTicketRejectsUntrustedURL(t *testing.T) {
	manager, err := NewAuthManager(&memoryStore{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"http://passport.biligame.com/crossDomain?ticket=x",
		"https://passport.biligame.com.evil.example/crossDomain?ticket=x",
		"https://evil.example/crossDomain?ticket=x",
		"https://user@passport.biligame.com/crossDomain?ticket=x",
	} {
		if _, err := manager.exchangeLoginTicket(context.Background(), raw); err == nil {
			t.Errorf("expected untrusted ticket URL to be rejected: %q", raw)
		}
	}
}

func TestPollLoginDoesNotSaveCredentialsForUntrustedTicket(t *testing.T) {
	store := &memoryStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "qrcode/poll") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"code": 0,
					"url": "https://attacker.example/crossDomain?ticket=steal",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "http"
		u.Host = strings.TrimPrefix(server.URL, "http://")
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})
	manager, err := NewAuthManager(store, client)
	if err != nil {
		t.Fatal(err)
	}
	manager.pending["login"] = pendingLogin{key: "qr", expiresAt: time.Now().Add(time.Minute)}
	if _, err := manager.PollLogin(context.Background(), "login"); err == nil {
		t.Fatal("expected untrusted ticket URL to fail")
	}
	if len(store.data) != 0 {
		t.Fatal("credentials must not be saved when ticket URL is untrusted")
	}
}
