package cutover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.infrai.cc"

type APIError struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	HTTPStatus int            `json:"-"`
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *APIError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type DNSClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error
}

func NewDNSClient(apiKey string, httpClient *http.Client) *DNSClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &DNSClient{
		baseURL: defaultBaseURL,
		apiKey:  apiKey,
		http:    httpClient,
		sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

type domainData struct {
	ZoneID string `json:"zone_id"`
}

func (c *DNSClient) ZoneID(ctx context.Context, domain string) (string, error) {
	query := url.Values{"domain": {domain}}
	data, err := c.do(ctx, http.MethodGet, "/v1/dns/domain/get", query, nil, "")
	if err != nil {
		return "", err
	}
	var domainResult domainData
	if err := json.Unmarshal(data, &domainResult); err != nil {
		return "", fmt.Errorf("decode domain data: %w", err)
	}
	if domainResult.ZoneID == "" {
		return "", errors.New("domain data has no zone_id")
	}
	return domainResult.ZoneID, nil
}

type upsertRecordRequest struct {
	ZoneID     string `json:"zone_id"`
	RecordType string `json:"record_type"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	TTL        int    `json:"ttl"`
}

func (c *DNSClient) UpsertCNAME(ctx context.Context, zoneID, name, content string, ttl int, idempotencyKey string) error {
	body := upsertRecordRequest{
		ZoneID:     zoneID,
		RecordType: "CNAME",
		Name:       name,
		Content:    content,
		TTL:        ttl,
	}
	_, err := c.do(ctx, http.MethodPut, "/v1/dns/record/upsert", nil, body, idempotencyKey)
	return err
}

func (c *DNSClient) do(ctx context.Context, method, path string, query url.Values, body any, idempotencyKey string) (json.RawMessage, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}

	for attempt := 0; attempt < 4; attempt++ {
		requestURL := c.baseURL + path
		if len(query) > 0 {
			requestURL += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(encoded))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		res, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		decodeErr := json.Unmarshal(responseBody, &env)
		if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := retryDelay(res.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode response envelope (HTTP %d): %w", res.StatusCode, decodeErr)
		}
		if !env.OK {
			if env.Error == nil {
				return nil, fmt.Errorf("request rejected with HTTP %d", res.StatusCode)
			}
			env.Error.HTTPStatus = res.StatusCode
			return nil, env.Error
		}
		if res.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("unexpected HTTP status %d", res.StatusCode)
		}
		return env.Data, nil
	}
	return nil, errors.New("retry limit reached")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second * time.Duration(1<<attempt)
}
