package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	issuer       = "https://auth.openai.com"
	responses    = "https://chatgpt.com/backend-api/codex/responses"
	userAgent    = "opengo/0.1"
	instructions = "You are a concise, helpful coding agent."
)

type credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	ExpiresAt    int64  `json:"expires_at"`
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type jwtClaims struct {
	AccountID string `json:"chatgpt_account_id"`
	Residency string `json:"chatgpt_compute_residency"`
	Auth      struct {
		AccountID string `json:"chatgpt_account_id"`
		Residency string `json:"chatgpt_compute_residency"`
	} `json:"https://api.openai.com/auth"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations"`
}

type deviceCode struct {
	ID       string `json:"device_auth_id"`
	UserCode string `json:"user_code"`
	Interval string `json:"interval"`
}

type deviceToken struct {
	Code     string `json:"authorization_code"`
	Verifier string `json:"code_verifier"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type message struct {
	Role    string    `json:"role"`
	Content []content `json:"content"`
}

type responseRequest struct {
	Model        string    `json:"model"`
	Input        []message `json:"input"`
	Instructions string    `json:"instructions"`
	Store        bool      `json:"store"`
	Stream       bool      `json:"stream"`
}

type streamEvent struct {
	Type     string       `json:"type"`
	Delta    string       `json:"delta"`
	Text     string       `json:"text"`
	Error    *streamError `json:"error"`
	Response *struct {
		Status string       `json:"status"`
		Error  *streamError `json:"error"`
	} `json:"response"`
}

type streamError struct {
	Message string `json:"message"`
}

func main() {
	model := flag.String("model", "gpt-5.6-sol", "Codex model")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: opengo [-model model] [auth]")
		flag.PrintDefaults()
	}
	flag.Parse()
	authOnly := flag.NArg() == 1 && flag.Arg(0) == "auth"
	if flag.NArg() > 0 && !authOnly {
		fmt.Fprintln(os.Stderr, "usage: opengo [auth] [-model model]")
		os.Exit(2)
	}

	if err := run(context.Background(), *model, authOnly); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, model string, authOnly bool) error {
	authFile, err := authPath()
	if err != nil {
		return err
	}

	client := &http.Client{}
	auth, err := loadCredentials(authFile)
	if errors.Is(err, os.ErrNotExist) {
		auth, err = login(ctx, client)
	}
	if err != nil {
		return err
	}
	if auth.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
		auth, err = refresh(ctx, client, auth)
		if err != nil {
			return err
		}
	}
	if err := saveCredentials(authFile, auth); err != nil {
		return err
	}
	if authOnly {
		fmt.Println("opengo authenticated")
		return nil
	}

	input := bufio.NewScanner(os.Stdin)
	history := make([]message, 0, 16)
	fmt.Printf("opengo (%s). Type /exit to quit.\n", model)

	for {
		fmt.Print("> ")
		if !input.Scan() {
			return input.Err()
		}
		prompt := strings.TrimSpace(input.Text())
		if prompt == "" {
			continue
		}
		if prompt == "/exit" {
			return nil
		}

		if auth.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
			auth, err = refresh(ctx, client, auth)
			if err != nil {
				return err
			}
			if err := saveCredentials(authFile, auth); err != nil {
				return err
			}
		}

		history = append(history, message{Role: "user", Content: []content{{Type: "input_text", Text: prompt}}})
		answer, err := respond(ctx, client, model, auth, history)
		if err != nil {
			history = history[:len(history)-1]
			fmt.Fprintln(os.Stderr, "error:", err)
			continue
		}
		history = append(history, message{Role: "assistant", Content: []content{{Type: "output_text", Text: answer}}})
	}
}

func authPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "opengo", "auth.json"), nil
}

func loadCredentials(filename string) (credentials, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var auth credentials
	if err := json.Unmarshal(data, &auth); err != nil {
		return credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	if auth.RefreshToken == "" {
		return credentials{}, errors.New("decode credentials: missing refresh token")
	}
	return auth, nil
}

func saveCredentials(filename string, auth credentials) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	data, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Chmod(filename, 0o600); err != nil {
		return fmt.Errorf("secure credentials: %w", err)
	}
	return nil
}

func login(ctx context.Context, client *http.Client) (credentials, error) {
	var device deviceCode
	if err := postJSON(ctx, client, issuer+"/api/accounts/deviceauth/usercode", map[string]string{"client_id": clientID}, &device); err != nil {
		return credentials{}, fmt.Errorf("start device login: %w", err)
	}
	if device.ID == "" || device.UserCode == "" {
		return credentials{}, errors.New("start device login: incomplete response")
	}

	authURL := issuer + "/codex/device"
	fmt.Printf("Open %s and enter code %s\n", authURL, device.UserCode)
	if err := exec.Command("open", authURL).Run(); err != nil {
		return credentials{}, fmt.Errorf("open authentication page: %w", err)
	}
	wait, err := time.ParseDuration(device.Interval + "s")
	if err != nil || wait < time.Second {
		wait = 5 * time.Second
	}
	wait += 3 * time.Second

	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		request, err := json.Marshal(map[string]string{"device_auth_id": device.ID, "user_code": device.UserCode})
		if err != nil {
			return credentials{}, fmt.Errorf("encode device login request: %w", err)
		}
		response, err := do(loginCtx, client, issuer+"/api/accounts/deviceauth/token", "application/json", request)
		if err != nil {
			return credentials{}, fmt.Errorf("poll device login: %w", err)
		}
		if response.StatusCode == http.StatusOK {
			var token deviceToken
			err := decodeResponse(response, &token)
			if err != nil {
				return credentials{}, fmt.Errorf("decode device login: %w", err)
			}
			if token.Code == "" || token.Verifier == "" {
				return credentials{}, errors.New("decode device login: incomplete response")
			}
			return exchange(ctx, client, token)
		}
		if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusNotFound {
			return credentials{}, responseError("poll device login", response)
		}
		if err := response.Body.Close(); err != nil {
			return credentials{}, fmt.Errorf("close device login response: %w", err)
		}

		select {
		case <-loginCtx.Done():
			return credentials{}, fmt.Errorf("wait for device login: %w", loginCtx.Err())
		case <-time.After(wait):
		}
	}
}

func exchange(ctx context.Context, client *http.Client, device deviceToken) (credentials, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {device.Code},
		"redirect_uri":  {issuer + "/deviceauth/callback"},
		"client_id":     {clientID},
		"code_verifier": {device.Verifier},
	}
	var tokens tokenResponse
	if err := postForm(ctx, client, issuer+"/oauth/token", form, &tokens); err != nil {
		return credentials{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	return credentialsFrom(tokens, credentials{})
}

func refresh(ctx context.Context, client *http.Client, current credentials) (credentials, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
		"client_id":     {clientID},
	}
	var tokens tokenResponse
	if err := postForm(ctx, client, issuer+"/oauth/token", form, &tokens); err != nil {
		return credentials{}, fmt.Errorf("refresh access token: %w", err)
	}
	return credentialsFrom(tokens, current)
}

func credentialsFrom(tokens tokenResponse, current credentials) (credentials, error) {
	if tokens.AccessToken == "" {
		return credentials{}, errors.New("token response is missing access token")
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = current.RefreshToken
	}
	if tokens.RefreshToken == "" {
		return credentials{}, errors.New("token response is missing refresh token")
	}
	expires := tokens.ExpiresIn
	if expires == 0 {
		expires = 3600
	}
	account := accountID(tokens.IDToken)
	if account == "" {
		account = accountID(tokens.AccessToken)
	}
	if account == "" {
		account = current.AccountID
	}
	return credentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		AccountID:    account,
		ExpiresAt:    time.Now().Add(time.Duration(expires) * time.Second).Unix(),
	}, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	response, err := do(ctx, client, endpoint, "application/json", body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return responseError("request", response)
	}
	return decodeResponse(response, output)
}

func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, output any) error {
	response, err := do(ctx, client, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return responseError("request", response)
	}
	return decodeResponse(response, output)
}

func do(ctx context.Context, client *http.Client, endpoint, contentType string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", userAgent)
	return client.Do(request)
}

func decodeResponse(response *http.Response, output any) error {
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func responseError(operation string, response *http.Response) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s: status %s; read body: %w", operation, response.Status, err)
	}
	return fmt.Errorf("%s: status %s: %s", operation, response.Status, strings.TrimSpace(string(body)))
}

func respond(ctx context.Context, client *http.Client, model string, auth credentials, history []message) (string, error) {
	body, err := json.Marshal(responseRequest{
		Model:        model,
		Input:        history,
		Instructions: instructions,
		Store:        false,
		Stream:       true,
	})
	if err != nil {
		return "", fmt.Errorf("encode response request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, responses, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create response request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	if auth.AccountID != "" {
		request.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Originator", "opencode")
	request.Header.Set("User-Agent", userAgent)
	if residency := tokenResidency(auth.AccessToken); residency != "" {
		request.Header.Set("x-openai-internal-codex-residency", residency)
	}

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("create response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", responseError("create response", response)
	}
	defer response.Body.Close()

	var answer strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return "", fmt.Errorf("decode stream event: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta":
			fmt.Print(event.Delta)
			answer.WriteString(event.Delta)
		case "response.output_text.done":
			if answer.Len() == 0 {
				fmt.Print(event.Text)
				answer.WriteString(event.Text)
			}
		case "error", "response.failed":
			if event.Error != nil && event.Error.Message != "" {
				return "", errors.New(event.Error.Message)
			}
			if event.Response != nil && event.Response.Error != nil {
				return "", errors.New(event.Response.Error.Message)
			}
			return "", fmt.Errorf("response stream failed: %s", event.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read response stream: %w", err)
	}
	if answer.Len() == 0 {
		return "", errors.New("response contained no text")
	}
	fmt.Println()
	return answer.String(), nil
}

func accountID(token string) string {
	claims, ok := parseClaims(token)
	if !ok {
		return ""
	}
	if claims.AccountID != "" {
		return claims.AccountID
	}
	if claims.Auth.AccountID != "" {
		return claims.Auth.AccountID
	}
	if len(claims.Organizations) > 0 {
		return claims.Organizations[0].ID
	}
	return ""
}

func tokenResidency(token string) string {
	claims, ok := parseClaims(token)
	if !ok {
		return ""
	}
	residency := claims.Residency
	if claims.Auth.Residency != "" {
		residency = claims.Auth.Residency
	}
	if residency == "no_constraint" {
		return ""
	}
	return residency
}

func parseClaims(token string) (jwtClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, false
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, false
	}
	return claims, true
}
