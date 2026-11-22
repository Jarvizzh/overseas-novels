// Package paypal implements the StarNovel PayPal channel adapter.
package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reader-backend/internal/payment"
)

const requestTimeout = 15 * time.Second
const maxRequestKeyLength = 38

func validRequestKey(key string) bool {
	return validObjectID(key) && len(key) <= maxRequestKeyLength
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

func (p *Client) accessToken(ctx context.Context, cfg Config) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.baseURL()+"/v1/oauth2/token", bytes.NewBufferString("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(cfg.ClientID, cfg.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := readAPIError(resp)
		err.Authentication = true
		return "", err
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tokenResp); err != nil {
		return "", err
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("PayPal returned an empty access token")
	}
	return tokenResp.AccessToken, nil
}

func (p *Client) request(ctx context.Context, method, path, requestID string, body, result any) error {
	cfg, err := p.credentials(ctx)
	if err != nil {
		return err
	}
	return p.send(ctx, cfg, method, path, requestID, body, result)
}

func (p *Client) send(ctx context.Context, cfg Config, method, path, requestID string, body, result any) error {
	token, err := p.accessToken(ctx, cfg)
	if err != nil {
		return err
	}
	return p.doRequest(ctx, cfg.baseURL(), token, method, path, requestID, body, result)
}

func (p *Client) doRequest(ctx context.Context, baseURL, token, method, path, requestID string, body, result any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(path, "/v1/reporting/") {
		req.Header.Set("PayPal-Enforce-ISO8601-Format", "true")
	}
	if requestID != "" {
		req.Header.Set("PayPal-Request-Id", requestID)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(result)
}

// APIError preserves provider issue codes and the support reference.
type APIError struct {
	Status         int
	Name           string   `json:"name"`
	DebugID        string   `json:"debug_id"`
	Issues         []string `json:"-"`
	Authentication bool     `json:"-"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("PayPal API HTTP %d (%s, issues=%v, debug_id=%s)", e.Status, e.Name, e.Issues, e.DebugID)
}

func (e *APIError) HasIssue(issue string) bool {
	if e.Name == issue {
		return true
	}
	for _, candidate := range e.Issues {
		if candidate == issue {
			return true
		}
	}
	return false
}

func readAPIError(resp *http.Response) *APIError {
	var body struct {
		APIError
		Details []struct {
			Issue string `json:"issue"`
		} `json:"details"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return &APIError{Status: resp.StatusCode}
	}
	body.Status = resp.StatusCode
	for _, detail := range body.Details {
		if detail.Issue != "" {
			body.Issues = append(body.Issues, detail.Issue)
		}
	}
	return &body.APIError
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: requestTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (p *Client) read(ctx context.Context, path string, result any) (payment.WebhookAccount, error) {
	cfg, err := p.credentials(ctx)
	if err != nil {
		return payment.WebhookAccount{}, err
	}
	err = p.send(ctx, cfg, http.MethodGet, path, "", nil, result)
	return cfg.account(), err
}

type Link struct {
	Rel    string `json:"rel"`
	Href   string `json:"href"`
	Method string `json:"method"`
}

type Amount struct {
	Currency string `json:"currency_code"`
	Value    string `json:"value"`
}

func endpoint(path string, query url.Values) string {
	return (&url.URL{Path: path, RawQuery: query.Encode()}).RequestURI()
}

func validObjectID(id string) bool {
	return id != "" && len(id) <= 255 && !strings.ContainsFunc(id, func(r rune) bool {
		return r != '-' && r != '_' && !(r >= '0' && r <= '9') &&
			!(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z')
	})
}

func nextPage(links []Link, baseURL, path string) (string, error) {
	for _, link := range links {
		if link.Rel != "next" {
			continue
		}
		next, err := url.Parse(link.Href)
		if err != nil || next.Path != path || next.RawQuery == "" ||
			next.User != nil || next.Fragment != "" || next.Opaque != "" ||
			(next.Host != "" && next.Scheme+"://"+next.Host != baseURL) ||
			(next.Host == "" && next.Scheme != "") ||
			(link.Method != "" && link.Method != http.MethodGet) {
			return "", errors.New("invalid PayPal pagination link")
		}
		if _, err := url.ParseQuery(next.RawQuery); err != nil {
			return "", errors.New("invalid PayPal pagination parameters")
		}
		return next.RawQuery, nil
	}
	return "", nil
}

func pageQuery(cursor string) (url.Values, error) {
	if cursor == "" {
		return make(url.Values), nil
	}
	query, err := url.ParseQuery(cursor)
	if err != nil {
		return nil, errors.New("invalid PayPal pagination parameters")
	}
	return query, nil
}
