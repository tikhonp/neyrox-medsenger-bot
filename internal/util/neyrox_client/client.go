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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
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

// getPaginated GETs startURL with the bearer token and follows DRF "next" links,
// calling decode for each page body. decode returns the next page URL (nil to stop).
func (c *Client) getPaginated(accessToken, startURL string, decode func(io.Reader) (*string, error)) error {
	next := startURL
	for next != "" {
		req, err := http.NewRequest(http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			return ErrUnauthorized
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError("GET "+next, resp)
			_ = resp.Body.Close()
			return err
		}
		nextURL, err := decode(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if nextURL != nil {
			next = *nextURL
		} else {
			next = ""
		}
	}
	return nil
}

// FetchMeasurements returns measurements for the given metric endpoint (e.g. "pulse"),
// optionally filtered to those with date_device after `since`, following DRF pagination.
func (c *Client) FetchMeasurements(accessToken, metric string, since *time.Time) ([]Measurement, error) {
	q := url.Values{}
	q.Set("ordering", "date_device")
	if since != nil {
		q.Set("date_device_after", since.UTC().Format(time.RFC3339))
	}
	start := fmt.Sprintf("%s/api/v1/%s/?%s", c.host, metric, q.Encode())

	var all []Measurement
	err := c.getPaginated(accessToken, start, func(body io.Reader) (*string, error) {
		var page paginatedMeasurements
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		return page.Next, nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

// FetchTypeIndicators returns the Neyrox typeindicators reference table (id + name).
// It is small reference data; the worker resolves the systolic/diastolic rows from it.
func (c *Client) FetchTypeIndicators(accessToken string) ([]TypeIndicator, error) {
	var all []TypeIndicator
	err := c.getPaginated(accessToken, c.host+"/api/v1/typeindicators/", func(body io.Reader) (*string, error) {
		var page paginatedTypeIndicators
		if err := json.NewDecoder(body).Decode(&page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		return page.Next, nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("neyrox %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
}
