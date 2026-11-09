package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

type BilibiliAuthService struct { auth *bilibili.AuthManager }

func NewBilibiliAuthService(auth *bilibili.AuthManager) *BilibiliAuthService {
	return &BilibiliAuthService{auth:auth}
}

func (s *BilibiliAuthService) StartLogin() (bilibili.LoginSession, error) {
	if s == nil || s.auth == nil { return bilibili.LoginSession{}, errors.New("Bilibili authentication service is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.auth.BeginLogin(ctx)
}

func (s *BilibiliAuthService) PollLogin(sessionID string) (bilibili.LoginStatus, error) {
	if s == nil || s.auth == nil { return bilibili.LoginStatus{}, errors.New("Bilibili authentication service is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.auth.PollLogin(ctx, sessionID)
}

func (s *BilibiliAuthService) GetAccount() (*bilibili.Account, error) {
	if s == nil || s.auth == nil { return nil, errors.New("Bilibili authentication service is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.auth.CurrentAccount(ctx)
}

func (s *BilibiliAuthService) Logout() error {
	if s == nil || s.auth == nil { return errors.New("Bilibili authentication service is unavailable") }
	return s.auth.Logout()
}
