package bilibili

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	qrGenerateURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate"
	qrPollURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll"
	navURL = "https://api.bilibili.com/x/web-interface/nav"
	qrLifetime = 180 * time.Second
)

type CredentialStore interface {
	Save([]byte) error
	Load() ([]byte, error)
	Delete() error
}

type Credentials struct {
	Cookie       string `json:"cookie"`
	RefreshToken string `json:"refresh_token,omitempty"`
	UID          string `json:"uid"`
}

type Account struct {
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar,omitempty"`
	IsLogin  bool   `json:"is_login"`
}

type LoginSession struct {
	ID            string    `json:"id"`
	QRCodeDataURL string    `json:"qr_code_data_url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type LoginStatus struct {
	Status  string   `json:"status"`
	Message string   `json:"message,omitempty"`
	Account *Account `json:"account,omitempty"`
}

type pendingLogin struct {
	key       string
	expiresAt time.Time
}

type AuthManager struct {
	store  CredentialStore
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
	pending map[string]pendingLogin
}

func NewAuthManager(store CredentialStore, client *http.Client) (*AuthManager, error) {
	if store == nil { return nil, errors.New("Bilibili credential store is required") }
	if client == nil { client = &http.Client{Timeout: 15 * time.Second} }
	return &AuthManager{store: store, client: client, now: time.Now, pending: make(map[string]pendingLogin)}, nil
}

func (m *AuthManager) BeginLogin(ctx context.Context) (LoginSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, qrGenerateURL, nil)
	if err != nil { return LoginSession{}, err }
	setWebHeaders(req)
	resp, err := m.client.Do(req)
	if err != nil { return LoginSession{}, fmt.Errorf("request Bilibili QR code: %w", err) }
	defer resp.Body.Close()
	var result struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct { URL string `json:"url"`; Key string `json:"qrcode_key"` } `json:"data"`
	}
	if err := decodeResponse(resp, &result); err != nil { return LoginSession{}, err }
	if result.Code != 0 || result.Data.URL == "" || result.Data.Key == "" {
		return LoginSession{}, fmt.Errorf("Bilibili QR generation failed: %s", safeMessage(result.Message))
	}
	png, err := qrcode.Encode(result.Data.URL, qrcode.Medium, 280)
	if err != nil { return LoginSession{}, fmt.Errorf("encode Bilibili QR code: %w", err) }
	id, err := randomID()
	if err != nil { return LoginSession{}, err }
	expires := m.now().Add(qrLifetime)
	m.mu.Lock()
	for key, session := range m.pending { if !m.now().Before(session.expiresAt) { delete(m.pending, key) } }
	m.pending[id] = pendingLogin{key: result.Data.Key, expiresAt: expires}
	m.mu.Unlock()
	return LoginSession{ID:id, QRCodeDataURL:"data:image/png;base64,"+base64.StdEncoding.EncodeToString(png), ExpiresAt:expires}, nil
}

func (m *AuthManager) PollLogin(ctx context.Context, sessionID string) (LoginStatus, error) {
	m.mu.Lock()
	session, ok := m.pending[sessionID]
	if ok && !m.now().Before(session.expiresAt) { delete(m.pending, sessionID); ok = false }
	m.mu.Unlock()
	if !ok { return LoginStatus{Status:"expired", Message:"二维码已失效，请重新生成"}, nil }

	endpoint, _ := url.Parse(qrPollURL)
	query := endpoint.Query()
	query.Set("qrcode_key", session.key)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil { return LoginStatus{}, err }
	setWebHeaders(req)
	resp, err := m.client.Do(req)
	if err != nil { return LoginStatus{}, fmt.Errorf("poll Bilibili QR login: %w", err) }
	defer resp.Body.Close()
	var result struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			Code int `json:"code"`
			Message string `json:"message"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := decodeResponse(resp, &result); err != nil { return LoginStatus{}, err }
	switch result.Data.Code {
	case 86101:
		return LoginStatus{Status:"waiting", Message:"请使用哔哩哔哩手机客户端扫码"}, nil
	case 86090:
		return LoginStatus{Status:"scanned", Message:"已扫码，请在手机上确认登录"}, nil
	case 86038:
		m.deletePending(sessionID)
		return LoginStatus{Status:"expired", Message:"二维码已失效，请重新生成"}, nil
	case 0:
		cookies := resp.Cookies()
		parts := make([]string, 0, len(cookies))
		for _, cookie := range cookies {
			if cookie.Name == "SESSDATA" || cookie.Name == "bili_jct" || cookie.Name == "DedeUserID" || cookie.Name == "DedeUserID__ckMd5" || cookie.Name == "buvid3" {
				parts = append(parts, cookie.Name+"="+cookie.Value)
			}
		}
		cookieHeader := strings.Join(parts, "; ")
		if !strings.Contains(cookieHeader, "SESSDATA=") || !strings.Contains(cookieHeader, "DedeUserID=") {
			return LoginStatus{}, errors.New("Bilibili login succeeded but required session cookies were not returned")
		}
		credentials := Credentials{Cookie:cookieHeader, RefreshToken:result.Data.RefreshToken}
		account, err := m.fetchAccount(ctx, credentials.Cookie)
		if err != nil { return LoginStatus{}, fmt.Errorf("verify Bilibili account after login: %w", err) }
		credentials.UID = account.UID
		data, err := json.Marshal(credentials)
		if err != nil { return LoginStatus{}, err }
		if err := m.store.Save(data); err != nil { return LoginStatus{}, fmt.Errorf("securely save Bilibili credentials: %w", err) }
		m.deletePending(sessionID)
		return LoginStatus{Status:"success", Message:"登录成功", Account:account}, nil
	default:
		if result.Code != 0 { return LoginStatus{}, fmt.Errorf("Bilibili QR login failed: %s", safeMessage(result.Message)) }
		return LoginStatus{Status:"waiting", Message:safeMessage(result.Data.Message)}, nil
	}
}


func (m *AuthManager) LoginWithCookie(ctx context.Context, rawCookie string) (*Account, error) {
	cookie, err := normalizeCookie(rawCookie)
	if err != nil { return nil, err }
	account, err := m.fetchAccount(ctx, cookie)
	if err != nil { var authErr *authenticationError; if errors.As(err, &authErr) { return nil, errors.New("Cookie 无效或已过期，请检查浏览器 Cookie 后重试") }; return nil, err }
	data, err := json.Marshal(Credentials{Cookie: cookie, UID: account.UID})
	if err != nil { return nil, err }
	if err := m.store.Save(data); err != nil { return nil, fmt.Errorf("securely save Bilibili credentials: %w", err) }
	return account, nil
}

func normalizeCookie(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\\r\\n") { return "", errors.New("请粘贴有效的 B 站 Cookie") }
	allowed := map[string]bool{"SESSDATA":true,"bili_jct":true,"DedeUserID":true,"DedeUserID__ckMd5":true,"buvid3":true}
	values := make(map[string]string)
	for _, item := range strings.Split(raw, ";") { item = strings.TrimSpace(item); if item == "" { continue }; name, value, ok := strings.Cut(item, "="); if !ok { return "", errors.New("Cookie 格式不正确，请复制完整的 Cookie 请求头") }; name,value = strings.TrimSpace(name),strings.TrimSpace(value); if !allowed[name] { continue }; if value == "" || strings.ContainsAny(value, "\\r\\n;") { return "", errors.New("Cookie 中包含无效字段") }; if _,exists := values[name]; exists { return "", errors.New("Cookie 中存在重复字段，请重新复制") }; values[name]=value }
	if values["SESSDATA"] == "" { return "", errors.New("Cookie 缺少 SESSDATA，请确认复制的是 B 站登录后的 Cookie") }
	order := []string{"SESSDATA","bili_jct","DedeUserID","DedeUserID__ckMd5","buvid3"}; parts := make([]string,0,len(values)); for _,name := range order { if value,ok := values[name]; ok { parts=append(parts,name+"="+value) } }; return strings.Join(parts,"; "),nil
}

func (m *AuthManager) CurrentAccount(ctx context.Context) (*Account, error) {
	data, err := m.store.Load()
	if errors.Is(err, ErrCredentialNotFound) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("read secure Bilibili credentials: %w", err) }
	var credentials Credentials
	if err := json.Unmarshal(data, &credentials); err != nil { return nil, fmt.Errorf("decode secure Bilibili credentials: %w", err) }
	account, err := m.fetchAccount(ctx, credentials.Cookie)
	if err != nil {
		var authErr *authenticationError
		if errors.As(err, &authErr) { _ = m.store.Delete(); return nil, nil }
		return nil, err
	}
	return account, nil
}

func (m *AuthManager) Logout() error {
	m.mu.Lock()
	m.pending = make(map[string]pendingLogin)
	m.mu.Unlock()
	if err := m.store.Delete(); err != nil && !errors.Is(err, ErrCredentialNotFound) { return err }
	return nil
}

func (m *AuthManager) fetchAccount(ctx context.Context, cookie string) (*Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, navURL, nil)
	if err != nil { return nil, err }
	setWebHeaders(req)
	req.Header.Set("Cookie", cookie)
	resp, err := m.client.Do(req)
	if err != nil { return nil, fmt.Errorf("request Bilibili account info: %w", err) }
	defer resp.Body.Close()
	var result struct {
		Code int `json:"code"`
		Message string `json:"message"`
		Data struct {
			IsLogin bool `json:"isLogin"`
			MID json.Number `json:"mid"`
			Uname string `json:"uname"`
			Face string `json:"face"`
		} `json:"data"`
	}
	if err := decodeResponse(resp, &result); err != nil { return nil, err }
	if result.Code == -101 || !result.Data.IsLogin { return nil, &authenticationError{} }
	if result.Code != 0 { return nil, fmt.Errorf("Bilibili account verification failed: %s", safeMessage(result.Message)) }
	return &Account{UID:result.Data.MID.String(), Name:result.Data.Uname, Avatar:result.Data.Face, IsLogin:true}, nil
}

type authenticationError struct{}
func (*authenticationError) Error() string { return "Bilibili login has expired" }

var ErrCredentialNotFound = errors.New("Bilibili credentials not found")

func (m *AuthManager) deletePending(id string) { m.mu.Lock(); delete(m.pending,id); m.mu.Unlock() }

func decodeResponse(resp *http.Response, target any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return fmt.Errorf("Bilibili API returned HTTP %d", resp.StatusCode) }
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil { return fmt.Errorf("decode Bilibili API response: %w", err) }
	return nil
}

func setWebHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/132.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
}

func safeMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" { return "未知错误" }
	if len(value) > 160 { return value[:160] }
	return value
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := io.ReadFull(randReader{}, data[:]); err != nil { return "", err }
	return fmt.Sprintf("%x", data[:]), nil
}
