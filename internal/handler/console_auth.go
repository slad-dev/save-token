package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/gateway"
	"agent-gateway/internal/security"
	"agent-gateway/internal/store"

	"gorm.io/gorm"
)

type ConsoleAuthHandler struct {
	store  *store.SQLiteStore
	cfg    *config.AuthConfig
	client *http.Client
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type emailCodeRequest struct {
	Email string `json:"email"`
}

type verifyEmailCodeRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
	Name  string `json:"name"`
}

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

type githubUserResponse struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmailRecord struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func NewConsoleAuthHandler(st *store.SQLiteStore, cfg *config.AuthConfig) *ConsoleAuthHandler {
	timeout := time.Duration(cfg.GitHub.HTTPTimeout)
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &ConsoleAuthHandler{store: st, cfg: cfg, client: &http.Client{Timeout: timeout}}
}

func (h *ConsoleAuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var payload registerRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	payload.Email = strings.TrimSpace(payload.Email)
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Email == "" || payload.Password == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email and password are required")
		return
	}

	if _, err := h.store.FindUserByEmail(r.Context(), payload.Email); err == nil {
		gateway.WriteJSONError(w, http.StatusConflict, "email_exists", "email already registered")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to check existing user")
		return
	}

	hashed, err := security.HashPassword(payload.Password)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	user, err := h.store.CreateConsoleUser(r.Context(), payload.Name, payload.Email, hashed)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create user")
		return
	}

	h.issueSession(w, r, user, "email")
}

func (h *ConsoleAuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	var payload loginRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}

	payload.Email = strings.TrimSpace(payload.Email)
	if payload.Email == "" || payload.Password == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email and password are required")
		return
	}

	user, err := h.store.FindUserByEmail(r.Context(), payload.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query user")
		return
	}

	if !user.IsActive {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "account is inactive")
		return
	}

	if !security.VerifyPassword(user.PasswordHash, payload.Password) {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	h.issueSession(w, r, user, "email")
}

func (h *ConsoleAuthHandler) RequestEmailCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !h.cfg.EmailCode.Enabled {
		gateway.WriteJSONError(w, http.StatusServiceUnavailable, "email_code_disabled", "email code login is not enabled")
		return
	}

	var payload emailCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	payload.Email = strings.TrimSpace(payload.Email)
	if payload.Email == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email is required")
		return
	}
	if !isValidEmail(payload.Email) {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email format is invalid")
		return
	}
	if !h.isEmailDomainAllowed(payload.Email) {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email domain is not allowed")
		return
	}

	code, err := h.generateNumericCode(h.cfg.EmailCode.CodeLength)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to generate verification code")
		return
	}
	if _, err := h.store.CreateConsoleEmailCode(r.Context(), payload.Email, code, time.Duration(h.cfg.EmailCode.OTPTTL)); err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create verification code")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"ok":         true,
		"expire_sec": int(time.Duration(h.cfg.EmailCode.OTPTTL).Seconds()),
	}
	if h.cfg.EmailCode.DevMode {
		response["debug_code"] = code
		response["delivery"] = "mock"
	} else if err := h.sendEmailCode(payload.Email, code); err != nil {
		gateway.WriteJSONError(w, http.StatusBadGateway, "email_send_failed", "failed to send verification email")
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (h *ConsoleAuthHandler) VerifyEmailCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if !h.cfg.EmailCode.Enabled {
		gateway.WriteJSONError(w, http.StatusServiceUnavailable, "email_code_disabled", "email code login is not enabled")
		return
	}

	var payload verifyEmailCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "request body must be valid JSON")
		return
	}
	payload.Email = strings.TrimSpace(payload.Email)
	payload.Code = strings.TrimSpace(payload.Code)
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Email == "" || payload.Code == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email and code are required")
		return
	}
	if !isValidEmail(payload.Email) {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "email format is invalid")
		return
	}

	if _, err := h.store.ConsumeConsoleEmailCode(r.Context(), payload.Email, payload.Code); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_code", "verification code is invalid or expired")
			return
		}
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to verify code")
		return
	}

	user, err := h.store.FindUserByEmail(r.Context(), payload.Email)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to query user")
			return
		}
		user, err = h.store.CreateConsoleUser(r.Context(), payload.Name, payload.Email, "")
		if err != nil {
			gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create user")
			return
		}
	}
	if !user.IsActive {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "invalid_credentials", "account is inactive")
		return
	}

	h.issueSession(w, r, user, "email_code")
}

func (h *ConsoleAuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		gateway.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}

	h.writeUser(w, principal.User)
}

func (h *ConsoleAuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if cookie, err := r.Cookie(h.cookieName()); err == nil && strings.TrimSpace(cookie.Value) != "" {
		_ = h.store.DeleteConsoleSessionByToken(r.Context(), cookie.Value)
	}
	h.clearSessionCookie(w)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *ConsoleAuthHandler) GitHubStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if !h.cfg.GitHub.Enabled || h.cfg.GitHub.ClientID == "" || h.cfg.GitHub.ClientSecret == "" || h.cfg.GitHub.RedirectURL == "" {
		gateway.WriteJSONError(w, http.StatusServiceUnavailable, "github_not_configured", "github login is not configured")
		return
	}

	state, err := randomHex(16)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to initialize oauth state")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     h.oauthStateCookieName(),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	authorizeURL := "https://github.com/login/oauth/authorize?client_id=" + url.QueryEscape(h.cfg.GitHub.ClientID) +
		"&redirect_uri=" + url.QueryEscape(h.cfg.GitHub.RedirectURL) +
		"&scope=" + url.QueryEscape("read:user user:email") +
		"&state=" + url.QueryEscape(state)
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

func (h *ConsoleAuthHandler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		gateway.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	if !h.cfg.GitHub.Enabled {
		gateway.WriteJSONError(w, http.StatusServiceUnavailable, "github_not_configured", "github login is not configured")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "missing code or state")
		return
	}

	cookie, err := r.Cookie(h.oauthStateCookieName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" || cookie.Value != state {
		gateway.WriteJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid oauth state")
		return
	}

	h.clearOAuthStateCookie(w)

	accessToken, err := h.exchangeGitHubCode(r.Context(), code)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadGateway, "oauth_error", err.Error())
		return
	}

	profile, err := h.fetchGitHubUser(r.Context(), accessToken)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadGateway, "oauth_error", err.Error())
		return
	}

	email, err := h.fetchGitHubPrimaryEmail(r.Context(), accessToken)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusBadGateway, "oauth_error", err.Error())
		return
	}

	githubID := fmt.Sprintf("%d", profile.ID)
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.TrimSpace(profile.Login)
	}
	user, err := h.store.UpsertGitHubUser(r.Context(), githubID, email, name, profile.AvatarURL)
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to bind github user")
		return
	}

	token, session, err := h.store.CreateConsoleSession(r.Context(), user.ID, "github", time.Duration(h.cfg.SessionTTL))
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}
	h.setSessionCookie(w, token, session.ExpiresAt)

	redirectPath := "/dashboard"
	if user.Role == store.RoleAdmin {
		redirectPath = "/admin"
	}
	redirectURL := strings.TrimRight(strings.TrimSpace(h.cfg.FrontendBaseURL), "/") + redirectPath
	if strings.TrimSpace(h.cfg.FrontendBaseURL) == "" {
		redirectURL = "http://localhost:3000" + redirectPath
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *ConsoleAuthHandler) issueSession(w http.ResponseWriter, r *http.Request, user *store.User, provider string) {
	token, session, err := h.store.CreateConsoleSession(r.Context(), user.ID, provider, time.Duration(h.cfg.SessionTTL))
	if err != nil {
		gateway.WriteJSONError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}

	h.setSessionCookie(w, token, session.ExpiresAt)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user": map[string]any{
			"id":         user.ID,
			"name":       user.Name,
			"email":      user.Email,
			"avatar_url": user.AvatarURL,
			"role":       user.Role,
		},
		"session_token": token,
		"expires_at":    session.ExpiresAt,
		"cookie_name":   h.cookieName(),
	})
}

func (h *ConsoleAuthHandler) writeUser(w http.ResponseWriter, user store.User) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         user.ID,
		"name":       user.Name,
		"email":      user.Email,
		"avatar_url": user.AvatarURL,
		"role":       user.Role,
		"plan":       user.Plan,
		"balance":    user.Balance,
	})
}

func (h *ConsoleAuthHandler) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

func (h *ConsoleAuthHandler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func (h *ConsoleAuthHandler) clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.oauthStateCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func (h *ConsoleAuthHandler) cookieName() string {
	if strings.TrimSpace(h.cfg.CookieName) != "" {
		return strings.TrimSpace(h.cfg.CookieName)
	}
	return "ag_console_session"
}

func (h *ConsoleAuthHandler) oauthStateCookieName() string {
	return h.cookieName() + "_oauth_state"
}

func (h *ConsoleAuthHandler) exchangeGitHubCode(ctx context.Context, code string) (string, error) {
	payload := map[string]string{
		"client_id":     h.cfg.GitHub.ClientID,
		"client_secret": h.cfg.GitHub.ClientSecret,
		"code":          code,
		"redirect_uri":  h.cfg.GitHub.RedirectURL,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		if tokenResp.Error != "" {
			return "", fmt.Errorf("github token exchange failed: %s", tokenResp.Error)
		}
		return "", errors.New("github token exchange failed")
	}
	return tokenResp.AccessToken, nil
}

func (h *ConsoleAuthHandler) fetchGitHubUser(ctx context.Context, accessToken string) (*githubUserResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("github user api failed: %s", strings.TrimSpace(string(body)))
	}

	var profile githubUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, err
	}
	if profile.ID == 0 {
		return nil, errors.New("github profile missing id")
	}
	return &profile, nil
}

func (h *ConsoleAuthHandler) fetchGitHubPrimaryEmail(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("github email api failed: %s", strings.TrimSpace(string(body)))
	}

	var emails []githubEmailRecord
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", err
	}
	for _, record := range emails {
		if record.Primary && record.Verified && strings.TrimSpace(record.Email) != "" {
			return record.Email, nil
		}
	}
	for _, record := range emails {
		if record.Verified && strings.TrimSpace(record.Email) != "" {
			return record.Email, nil
		}
	}
	return "", errors.New("github account has no verified email")
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (h *ConsoleAuthHandler) sendEmailCode(toEmail, code string) error {
	host := strings.TrimSpace(h.cfg.EmailCode.SMTP.Host)
	port := h.cfg.EmailCode.SMTP.Port
	username := strings.TrimSpace(h.cfg.EmailCode.SMTP.Username)
	password := h.cfg.EmailCode.SMTP.Password
	fromEmail := strings.TrimSpace(h.cfg.EmailCode.SMTP.FromEmail)
	if host == "" || port <= 0 || username == "" || password == "" || fromEmail == "" {
		return errors.New("smtp config is incomplete")
	}
	if !isValidEmail(fromEmail) || !isValidEmail(toEmail) {
		return errors.New("email format is invalid")
	}

	fromAddress := mail.Address{Name: strings.TrimSpace(h.cfg.EmailCode.FromName), Address: fromEmail}
	subject := strings.TrimSpace(h.cfg.EmailCode.Subject)
	if subject == "" {
		subject = "Agent Gateway 登录验证码"
	}
	minutes := int(time.Duration(h.cfg.EmailCode.OTPTTL).Minutes())
	if minutes <= 0 {
		minutes = 10
	}
	body := fmt.Sprintf(
		"您的 Agent Gateway 登录验证码是：%s\n\n验证码有效期：%d 分钟。\n如果不是您本人操作，请忽略这封邮件。\n",
		code,
		minutes,
	)
	message := []byte("From: " + fromAddress.String() + "\r\n" +
		"To: " + toEmail + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body)

	address := fmt.Sprintf("%s:%d", host, port)
	auth := smtp.PlainAuth("", username, password, host)

	if port == 465 {
		conn, err := tls.Dial("tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer client.Close()

		if err := client.Auth(auth); err != nil {
			return err
		}
		if err := client.Mail(fromEmail); err != nil {
			return err
		}
		if err := client.Rcpt(toEmail); err != nil {
			return err
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := writer.Write(message); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		return client.Quit()
	}

	client, err := smtp.Dial(address)
	if err != nil {
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if err := client.Auth(auth); err != nil {
		return err
	}
	if err := client.Mail(fromEmail); err != nil {
		return err
	}
	if err := client.Rcpt(toEmail); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (h *ConsoleAuthHandler) generateNumericCode(length int) (string, error) {
	if length < 4 || length > 8 {
		length = 6
	}

	buf := strings.Builder{}
	buf.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		buf.WriteByte(byte('0' + n.Int64()))
	}
	return buf.String(), nil
}

func (h *ConsoleAuthHandler) isEmailDomainAllowed(email string) bool {
	allowed := h.cfg.EmailCode.AllowedDomains
	if len(allowed) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), domain) {
			return true
		}
	}
	return false
}

func isValidEmail(email string) bool {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		return false
	}
	parts := strings.Split(addr.Address, "@")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" && !strings.Contains(parts[1], ":")
}
