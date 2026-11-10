package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type BilibiliAuthService struct {
	auth *bilibili.AuthManager
	platforms *platform.Manager
}

func NewBilibiliAuthService(auth *bilibili.AuthManager, managers ...*platform.Manager) *BilibiliAuthService {
	service := &BilibiliAuthService{auth: auth}
	if len(managers) > 0 {
		service.platforms = managers[0]
	}
	return service
}

// ServiceStartup reconnects Bilibili when a previously saved account is still
// valid. Missing or expired credentials must not prevent the desktop app from
// starting; the platform manager retains any actual connection failure.
func (s *BilibiliAuthService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	if s == nil || s.auth == nil || s.platforms == nil {
		return nil
	}
	account, err := s.auth.CurrentAccount(ctx)
	if err != nil {
		return fmt.Errorf("check saved Bilibili login before auto-connect: %w", err)
	}
	if account == nil || !account.IsLogin {
		return nil
	}
	_ = s.platforms.Connect(ctx, "bilibili")
	return nil
}

func (s *BilibiliAuthService) connectAfterLogin() {
	if s == nil || s.platforms == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Connect records failures in the platform snapshot. Login itself remains
	// successful because the verified credentials have already been saved.
	_ = s.platforms.Connect(ctx, "bilibili")
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
	status, err := s.auth.PollLogin(ctx, sessionID)
	if err == nil && status.Status == "success" {
		s.connectAfterLogin()
	}
	return status, err
}

func (s *BilibiliAuthService) LoginWithCookie(cookie string) (*bilibili.Account, error) {
	if s == nil || s.auth == nil { return nil, errors.New("Bilibili authentication service is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	account, err := s.auth.LoginWithCookie(ctx, cookie)
	if err == nil && account != nil && account.IsLogin {
		s.connectAfterLogin()
	}
	return account, err
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
