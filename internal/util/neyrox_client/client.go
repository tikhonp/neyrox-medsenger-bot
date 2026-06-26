// Package neyroxclient is a client for the Neyrox API (https://adm.neyrox.com).
//
// Auth uses DRF SimpleJWT: POST /api/token/ exchanges email+password for an
// access/refresh pair; POST /api/token/refresh/ renews the access token. Health
// data is read from the paginated DRF list endpoints at /api/v1/<metric>/.
package neyroxclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultHost = "https://adm.neyrox.com"

var (
	// ErrUnauthorized means the access token is missing/expired (HTTP 401 on a data call).
	ErrUnauthorized = errors.New("neyrox: unauthorized (token invalid or expired)")
	// ErrInvalidCredentials means login failed (HTTP 401 on /api/token/).
	ErrInvalidCredentials = errors.New("neyrox: invalid email or password")
)

type Client struct {
	host string
	http *http.Client
}

func New(host string) *Client {
	if host == "" {
		host = DefaultHost
	}
	return &Client{
		host: strings.TrimRight(host, "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Login exchanges email+password for an access/refresh token pair.
func (c *Client) Login(email, password string) (*TokenPair, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, c.host+"/api/token/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidCredentials
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("login", resp)
	}
	var tp TokenPair
	if err := json.NewDecoder(resp.Body).Decode(&tp); err != nil {
		return nil, err
	}
	return &tp, nil
}

// Refresh exchanges a refresh token for a fresh access token.
func (c *Client) Refresh(refresh string) (string, error) {
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	req, err := http.NewRequest(http.MethodPost, c.host+"/api/token/refresh/", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", apiError("refresh", resp)
	}
	var out struct {
		Access string `json:"access"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Access, nil
}

// FetchMeasurements returns measurements for the given metric endpoint (e.g. "pulse"),
// optionally filtered to those with date_device after `since`, following DRF pagination.
func (c *Client) FetchMeasurements(accessToken, metric string, since *time.Time) ([]Measurement, error) {
	q := url.Values{}
	q.Set("ordering", "date_device")
	if since != nil {
		q.Set("date_device_after", since.UTC().Format(time.RFC3339))
	}
	next := fmt.Sprintf("%s/api/v1/%s/?%s", c.host, metric, q.Encode())

	var all []Measurement
	for next != "" {
		req, err := http.NewRequest(http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return nil, ErrUnauthorized
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError("fetch "+metric, resp)
			resp.Body.Close()
			return nil, err
		}
		var page paginatedMeasurements
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if page.Next != nil {
			next = *page.Next
		} else {
			next = ""
		}
	}
	return all, nil
}

func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("neyrox %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
}
