package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qoder"
)

// qoderOAuthSessionTTL 是设备授权会话在服务端的有效时长，与官方 5 分钟一致。
const qoderOAuthSessionTTL = 5 * time.Minute

// QoderOAuthSession 保存一次设备流授权所需的 PKCE 与 nonce。
type QoderOAuthSession struct {
	Nonce        string
	CodeVerifier string
	CreatedAt    time.Time
}

// QoderOAuthService 负责 Qoder 设备流授权与令牌刷新。
type QoderOAuthService struct {
	client   *qoder.Client
	sessions sync.Map // sessionID -> *QoderOAuthSession
}

func NewQoderOAuthService() *QoderOAuthService {
	return &QoderOAuthService{
		client: &qoder.Client{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			UserAgent:  "qoder-cli",
		},
	}
}

// QoderAuthURLResult 返回给管理端，session_id 用于后续轮询。
type QoderAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
}

// QoderTokenResult 是授权成功后的令牌信息，access_token 即设备令牌。
type QoderTokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in"`
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *QoderOAuthService) storeSession(sessionID string, session *QoderOAuthSession) {
	s.sessions.Store(sessionID, session)
	// 过期会话按需清理，避免长期占用内存。
	time.AfterFunc(qoderOAuthSessionTTL, func() {
		if value, ok := s.sessions.Load(sessionID); ok {
			if stored, ok := value.(*QoderOAuthSession); ok && time.Since(stored.CreatedAt) >= qoderOAuthSessionTTL {
				s.sessions.Delete(sessionID)
			}
		}
	})
}

func (s *QoderOAuthService) takeSession(sessionID string) (*QoderOAuthSession, error) {
	value, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil, infraerrors.BadRequest("QODER_OAUTH_SESSION_NOT_FOUND", "Qoder authorization session not found or already used")
	}
	session, _ := value.(*QoderOAuthSession)
	s.sessions.Delete(sessionID)
	if session == nil || time.Since(session.CreatedAt) > qoderOAuthSessionTTL {
		return nil, infraerrors.BadRequest("QODER_OAUTH_SESSION_EXPIRED", "Qoder authorization session expired, please start again")
	}
	return session, nil
}

// GenerateAuthURL 生成设备授权链接并保存 PKCE 会话。
func (s *QoderOAuthService) GenerateAuthURL(ctx context.Context) (*QoderAuthURLResult, error) {
	_ = ctx
	pkce, err := qoder.GeneratePKCE()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "QODER_OAUTH_PKCE_FAILED", "failed to generate PKCE: %v", err)
	}
	nonce, err := qoder.GenerateNonce()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "QODER_OAUTH_NONCE_FAILED", "failed to generate nonce: %v", err)
	}
	machineID, err := qoder.MachineID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "QODER_OAUTH_MACHINE_ID_FAILED", "failed to generate machine id: %v", err)
	}
	sessionID, err := newSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "QODER_OAUTH_SESSION_FAILED", "failed to generate session id: %v", err)
	}
	authURL, err := qoder.BuildAuthURL(pkce.Challenge, nonce, machineID, qoder.ClientID())
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadRequest, "QODER_OAUTH_INVALID_AUTHORIZE_URL", "%v", err)
	}
	s.storeSession(sessionID, &QoderOAuthSession{
		Nonce:        nonce,
		CodeVerifier: pkce.Verifier,
		CreatedAt:    time.Now(),
	})
	return &QoderAuthURLResult{AuthURL: authURL, SessionID: sessionID}, nil
}

// PollToken 轮询授权结果；尚未完成时返回 done=false。
func (s *QoderOAuthService) PollToken(ctx context.Context, sessionID string) (*QoderTokenResult, bool, error) {
	value, ok := s.sessions.Load(sessionID)
	if !ok {
		return nil, false, infraerrors.BadRequest("QODER_OAUTH_SESSION_NOT_FOUND", "Qoder authorization session not found or expired")
	}
	session, _ := value.(*QoderOAuthSession)
	if session == nil || time.Since(session.CreatedAt) > qoderOAuthSessionTTL {
		s.sessions.Delete(sessionID)
		return nil, false, infraerrors.BadRequest("QODER_OAUTH_SESSION_EXPIRED", "Qoder authorization session expired, please start again")
	}
	result, err := s.client.PollOnce(ctx, session.Nonce, session.CodeVerifier, "S256")
	if err != nil {
		return nil, false, infraerrors.Newf(http.StatusBadGateway, "QODER_OAUTH_POLL_FAILED", "%v", err)
	}
	if !result.Done {
		return nil, false, nil
	}
	s.sessions.Delete(sessionID)
	return &QoderTokenResult{
		AccessToken:  result.Token.AccessToken,
		RefreshToken: result.Token.RefreshToken,
		ExpiresAt:    result.Token.ExpiresAt,
		ExpiresIn:    result.Token.ExpiresIn,
	}, true, nil
}

// RefreshToken 使用 refresh_token 换取新的设备令牌。
func (s *QoderOAuthService) RefreshToken(ctx context.Context, refreshToken string) (*QoderTokenResult, error) {
	result, err := s.client.Refresh(ctx, refreshToken)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "QODER_OAUTH_REFRESH_FAILED", "%v", err)
	}
	return &QoderTokenResult{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    result.ExpiresAt,
		ExpiresIn:    result.ExpiresIn,
	}, nil
}
